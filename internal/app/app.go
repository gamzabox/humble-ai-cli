package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gamzabox.com/humble-ai-cli/internal/config"
	"gamzabox.com/humble-ai-cli/internal/llm"
)

// Clock abstracts time access for testability.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

// ProviderFactory resolves model configurations to streaming providers.
type ProviderFactory interface {
	Create(config.Model) (llm.ChatProvider, error)
}

// Options configures App creation.
type Options struct {
	Store          config.Store
	Factory        ProviderFactory
	Input          io.Reader
	Output         io.Writer
	ErrorOutput    io.Writer
	HistoryRootDir string
	HomeDir        string
	Clock          Clock
	Interrupts     chan os.Signal
}

// App coordinates CLI behaviour.
type App struct {
	store       config.Store
	factory     ProviderFactory
	reader      *bufio.Reader
	output      io.Writer
	errOutput   io.Writer
	historyRoot string
	homeDir     string
	clock       Clock

	systemPrompt string

	cfgMu sync.RWMutex
	cfg   config.Config

	messages []llm.Message

	historyMu      sync.Mutex
	historyPath    string
	firstUserInput string
	sessionStart   time.Time

	modeMu        sync.Mutex
	mode          appMode
	cancelCurrent context.CancelFunc
	exitRequested bool

	signalCh   chan os.Signal
	stopSignal func()
}

type appMode int

const (
	modeInput appMode = iota
	modeResponding
)

// New constructs an App from options.
func New(opts Options) (*App, error) {
	if opts.Store == nil {
		return nil, errors.New("store is required")
	}
	if opts.Factory == nil {
		return nil, errors.New("factory is required")
	}
	if opts.Input == nil {
		return nil, errors.New("input is required")
	}
	if opts.Output == nil {
		return nil, errors.New("output is required")
	}

	errOutput := opts.ErrorOutput
	if errOutput == nil {
		errOutput = opts.Output
	}

	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}

	home := opts.HomeDir
	if home == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("determine home dir: %w", err)
		}
		home = dir
	}

	historyRoot := opts.HistoryRootDir
	if historyRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("determine working directory: %w", err)
		}
		historyRoot = wd
	}

	cfg, err := opts.Store.Load()
	if err != nil {
		if !errors.Is(err, config.ErrNotFound) {
			return nil, err
		}
		cfg = config.Config{}
	}

	systemPrompt, err := loadSystemPrompt(home)
	if err != nil {
		return nil, err
	}

	app := &App{
		store:        opts.Store,
		factory:      opts.Factory,
		reader:       bufio.NewReader(opts.Input),
		output:       opts.Output,
		errOutput:    errOutput,
		historyRoot:  historyRoot,
		homeDir:      home,
		clock:        clock,
		systemPrompt: systemPrompt,
		cfg:          cfg,
		mode:         modeInput,
	}

	app.setupSignals(opts.Interrupts)

	return app, nil
}

func loadSystemPrompt(home string) (string, error) {
	path := filepath.Join(home, ".config", "hunble-ai-cli", "system_prompt.txt")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read system prompt: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func (a *App) setupSignals(ch chan os.Signal) {
	if ch != nil {
		a.signalCh = ch
		return
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	a.signalCh = sigCh
	a.stopSignal = func() { signal.Stop(sigCh) }

	go func() {
		for range sigCh {
			a.handleInterrupt()
		}
	}()
}

// Run starts the interactive CLI loop.
func (a *App) Run(ctx context.Context) error {
	defer func() {
		if a.stopSignal != nil {
			a.stopSignal()
		}
	}()

	for {
		if a.shouldExit() {
			return nil
		}

		if err := a.printPrompt(); err != nil {
			return err
		}

		line, err := a.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			exit, err := a.handleCommand(ctx, line)
			if err != nil {
				fmt.Fprintf(a.errOutput, "Error: %v\n", err)
			}
			if a.shouldExit() {
				return nil
			}
			if exit {
				return nil
			}
			continue
		}

		if err := a.handleUserMessage(ctx, line); err != nil {
			fmt.Fprintf(a.errOutput, "Error: %v\n", err)
		}

		if a.shouldExit() {
			return nil
		}
	}
}

func (a *App) printPrompt() error {
	_, err := fmt.Fprint(a.output, "humble-ai> ")
	return err
}

func (a *App) readLine() (string, error) {
	text, err := a.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(text, "\r\n"), nil
}

func (a *App) handleCommand(ctx context.Context, line string) (bool, error) {
	switch line {
	case "/help":
		a.printHelp()
	case "/set-model":
		return false, a.changeActiveModel(ctx)
	case "/exti":
		return true, nil
	default:
		fmt.Fprintf(a.output, "Unknown command: %s\n", line)
	}
	return false, nil
}

func (a *App) printHelp() {
	fmt.Fprintln(a.output, "Available commands:")
	fmt.Fprintln(a.output, "  /help       Show this help message.")
	fmt.Fprintln(a.output, "  /set-model  Select one of the configured models as active.")
	fmt.Fprintln(a.output, "  /exti       Exit the application.")
}

func (a *App) changeActiveModel(ctx context.Context) error {
	a.cfgMu.RLock()
	cfg := a.cfg
	a.cfgMu.RUnlock()

	if len(cfg.Models) == 0 {
		fmt.Fprintf(a.output, "No models configured. Please add entries to %s.\n", a.configFilePath())
		return nil
	}

	fmt.Fprintln(a.output, "Select a model (0 to cancel):")
	for idx, m := range cfg.Models {
		activeMarker := ""
		if cfg.ActiveModel == m.Name {
			activeMarker = " *"
		}
		fmt.Fprintf(a.output, "  %d) %s (%s)%s\n", idx+1, m.Name, m.Provider, activeMarker)
	}
	fmt.Fprint(a.output, "Choice: ")

	choiceLine, err := a.readLine()
	if err != nil {
		return err
	}

	choiceLine = strings.TrimSpace(choiceLine)
	if choiceLine == "" {
		return nil
	}

	choice, err := strconv.Atoi(choiceLine)
	if err != nil || choice < 0 || choice > len(cfg.Models) {
		fmt.Fprintln(a.output, "Invalid selection.")
		return nil
	}

	if choice == 0 {
		fmt.Fprintln(a.output, "Model selection cancelled.")
		return nil
	}

	selected := cfg.Models[choice-1]

	cfg.ActiveModel = selected.Name
	if err := a.store.Save(cfg); err != nil {
		return err
	}

	a.cfgMu.Lock()
	a.cfg = cfg
	a.cfgMu.Unlock()

	fmt.Fprintf(a.output, "Active model set to %s (%s).\n", selected.Name, selected.Provider)
	return nil
}

func (a *App) configFilePath() string {
	return filepath.Join(a.homeDir, ".config", "hunble-ai-cli", "config.json")
}

func (a *App) handleUserMessage(ctx context.Context, content string) error {
	a.cfgMu.RLock()
	cfg := a.cfg
	a.cfgMu.RUnlock()

	if cfg.ActiveModel == "" {
		fmt.Fprintln(a.output, "No active model is configured. Use /set-model to choose a model.")
		if len(cfg.Models) == 0 {
			fmt.Fprintf(a.output, "Add model configuration to %s and try again.\n", a.configFilePath())
		}
		return nil
	}

	modelCfg, ok := cfg.FindModel(cfg.ActiveModel)
	if !ok {
		fmt.Fprintf(a.errOutput, "Active model %q not found. Update configuration.\n", cfg.ActiveModel)
		return nil
	}

	if a.firstUserInput == "" {
		a.firstUserInput = content
	}

	fmt.Fprintln(a.output, "Waiting for response...")

	provider, err := a.factory.Create(modelCfg)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	requestMessages := append([]llm.Message{}, a.messages...)
	requestMessages = append(requestMessages, llm.Message{Role: "user", Content: content})

	req := llm.ChatRequest{
		Model:        modelCfg.Name,
		Messages:     requestMessages,
		SystemPrompt: a.systemPrompt,
		Stream:       true,
	}

	reqCtx, cancel := context.WithCancel(ctx)
	a.enterResponding(cancel)
	defer a.leaveResponding()

	stream, err := provider.Stream(reqCtx, req)
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}

	var assistant strings.Builder
	thinkingShown := false
	errored := false

	for chunk := range stream {
		if chunk.Err != nil {
			fmt.Fprintf(a.errOutput, "Stream error: %v\n", chunk.Err)
			errored = true
			continue
		}

		switch chunk.Type {
		case llm.ChunkThinking:
			if !thinkingShown {
				fmt.Fprintln(a.output, "Thinking...")
				thinkingShown = true
			}
		case llm.ChunkToken:
			if !thinkingShown {
				fmt.Fprintln(a.output, "Thinking...")
				thinkingShown = true
			}
			fmt.Fprint(a.output, chunk.Content)
			assistant.WriteString(chunk.Content)
		case llm.ChunkError:
			fmt.Fprintf(a.errOutput, "Stream error: %v\n", chunk.Err)
			errored = true
		case llm.ChunkDone:
			// finished
		}
	}

	if reqCtx.Err() != nil {
		fmt.Fprintln(a.output, "\nResponse cancelled.")
		return nil
	}

	if assistant.Len() > 0 {
		fmt.Fprintln(a.output)
	}

	if errored {
		return nil
	}

	now := a.clock.Now()

	a.messages = append(a.messages,
		llm.Message{Role: "user", Content: content},
		llm.Message{Role: "assistant", Content: assistant.String()},
	)

	if err := a.persistHistory(modelCfg.Name, now); err != nil {
		fmt.Fprintf(a.errOutput, "Failed to persist history: %v\n", err)
	}

	return nil
}

func (a *App) persistHistory(model string, when time.Time) error {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()

	if a.historyPath == "" {
		a.sessionStart = when
		path, err := a.createHistoryFile(model, when)
		if err != nil {
			return err
		}
		a.historyPath = path
	} else if a.sessionStart.IsZero() {
		a.sessionStart = when
	}

	record := map[string]any{
		"model":     model,
		"startedAt": a.sessionStart.Format(time.RFC3339),
		"messages":  a.messages,
	}

	data, err := jsonMarshalIndent(record)
	if err != nil {
		return err
	}
	return os.WriteFile(a.historyPath, data, 0o644)
}

func (a *App) createHistoryFile(model string, when time.Time) (string, error) {
	dir := filepath.Join(a.historyRoot, "chat_history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create history dir: %w", err)
	}

	start := a.sessionStart
	if start.IsZero() {
		start = when
	}

	filename := fmt.Sprintf("%s_%s.json", start.Format("20060102_150405"), sanitizeTitle(a.firstUserInput))
	path := filepath.Join(dir, filename)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	initial := map[string]any{
		"model":     model,
		"startedAt": start.Format(time.RFC3339),
		"messages":  a.messages,
	}

	data, err := jsonMarshalIndent(initial)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write history: %w", err)
	}
	return path, nil
}

func (a *App) enterResponding(cancel context.CancelFunc) {
	a.modeMu.Lock()
	a.mode = modeResponding
	a.cancelCurrent = cancel
	a.modeMu.Unlock()
}

func (a *App) leaveResponding() {
	a.modeMu.Lock()
	a.mode = modeInput
	a.cancelCurrent = nil
	a.modeMu.Unlock()
}

func (a *App) handleInterrupt() {
	a.modeMu.Lock()
	defer a.modeMu.Unlock()

	switch a.mode {
	case modeResponding:
		if a.cancelCurrent != nil {
			a.cancelCurrent()
		}
	case modeInput:
		a.exitRequested = true
	}
}

func (a *App) shouldExit() bool {
	a.modeMu.Lock()
	defer a.modeMu.Unlock()
	return a.exitRequested
}

func sanitizeTitle(input string) string {
	if strings.TrimSpace(input) == "" {
		return "session"
	}
	runes := []rune(strings.TrimSpace(input))
	if len(runes) > 10 {
		runes = runes[:10]
	}
	builder := strings.Builder{}
	for _, r := range runes {
		if r == '/' || r == '\\' || r == ':' {
			builder.WriteRune('_')
			continue
		}
		if strings.ContainsRune("\n\r\t", r) {
			builder.WriteRune('_')
			continue
		}
		if r == ' ' {
			builder.WriteRune('_')
			continue
		}
		builder.WriteRune(r)
	}
	title := builder.String()
	if title == "" {
		return "session"
	}
	return title
}

func jsonMarshalIndent(v any) ([]byte, error) {
	data, err := jsonMarshal(v)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func jsonMarshal(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal history: %w", err)
	}
	return data, nil
}
