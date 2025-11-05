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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"gamzabox.com/humble-ai-cli/internal/config"
	"gamzabox.com/humble-ai-cli/internal/llm"
	"gamzabox.com/humble-ai-cli/internal/logging"
	mcpkg "gamzabox.com/humble-ai-cli/internal/mcp"
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

// MCPServer describes a configured MCP server surfaced to the LLM.
type MCPServer = mcpkg.Server

// MCPFunction describes a tool exposed by an MCP server.
type MCPFunction = mcpkg.Function

// MCPExecutor resolves server metadata and executes MCP tool calls.
type MCPExecutor interface {
	EnabledServers() []MCPServer
	Describe(server string) (MCPServer, bool)
	Call(ctx context.Context, server, method string, arguments map[string]any) (llm.ToolResult, error)
	Tools(ctx context.Context, server string) ([]MCPFunction, error)
	Close() error
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
	MCP            MCPExecutor
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
	logger       *logging.Logger
	mcp          MCPExecutor
	mcpServers   map[string]MCPServer
	mcpFunctions map[string][]MCPFunction
	mcpMu        sync.RWMutex

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

var errToolDeclined = errors.New("mcp call declined by user")

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
		historyRoot = filepath.Join(home, ".humble-ai-cli", "sessions")
	}

	mcpExec := opts.MCP
	if mcpExec == nil {
		manager, err := mcpkg.NewManager(home)
		if err != nil {
			return nil, fmt.Errorf("initialize MCP manager: %w", err)
		}
		mcpExec = manager
	}
	servers := mcpExec.EnabledServers()
	serverMap := make(map[string]MCPServer, len(servers))
	for _, srv := range servers {
		serverMap[srv.Name] = srv
	}

	cfg, err := opts.Store.Load()
	if err != nil {
		if !errors.Is(err, config.ErrNotFound) {
			return nil, err
		}
		cfg = config.Config{}
	}

	logger, err := logging.NewLogger(home, cfg.LogLevel)
	if err != nil {
		return nil, fmt.Errorf("initialize logger: %w", err)
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
		systemPrompt: "",
		logger:       logger,
		mcp:          mcpExec,
		mcpServers:   serverMap,
		mcpFunctions: make(map[string][]MCPFunction),
		cfg:          cfg,
		mode:         modeInput,
	}

	app.setupSignals(opts.Interrupts)

	if err := app.loadMCPFunctions(context.Background()); err != nil {
		_ = app.mcp.Close()
		return nil, err
	}

	if err := app.initializeSystemPrompt(); err != nil {
		_ = app.mcp.Close()
		return nil, err
	}

	return app, nil
}

func ensureSystemPrompt(home string, servers []MCPServer, functions map[string][]MCPFunction) (string, error) {
	path := filepath.Join(home, ".humble-ai-cli", "system_prompt.txt")
	data, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read system prompt: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create system prompt dir: %w", err)
	}

	content := buildDefaultSystemPrompt(servers, functions)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write default system prompt: %w", err)
	}
	return strings.TrimSpace(content), nil
}

func (a *App) initializeSystemPrompt() error {
	servers := a.snapshotServers()
	functions := a.snapshotFunctions()
	prompt, err := ensureSystemPrompt(a.homeDir, servers, functions)
	if err != nil {
		return err
	}
	a.systemPrompt = prompt
	a.logDebug("System prompt initialized (length=%d)", len(prompt))
	return nil
}

func (a *App) loadMCPFunctions(ctx context.Context) error {
	if a.mcp == nil {
		return nil
	}

	functions := make(map[string][]MCPFunction, len(a.mcpServers))
	a.mcpMu.RLock()
	for key, funcs := range a.mcpFunctions {
		functions[key] = cloneMCPFunctions(funcs)
	}
	a.mcpMu.RUnlock()
	for _, srv := range a.mcpServers {
		a.logDebug("MCP initialization: loading tools for server=%s", srv.Name)
		tools, err := a.mcp.Tools(ctx, srv.Name)
		if err != nil {
			fmt.Fprintf(a.errOutput, "Failed to list tools for %s: %v\n", srv.Name, err)
			a.logError("MCP initialization failed: server=%s err=%v", srv.Name, err)
			continue
		}
		functions[srv.Name] = cloneMCPFunctions(tools)
		a.logDebug("MCP initialization: server=%s tools=%d", srv.Name, len(tools))
	}

	a.mcpMu.Lock()
	a.mcpFunctions = functions
	a.mcpMu.Unlock()
	a.logDebug("MCP initialization complete: servers=%d", len(functions))
	return nil
}

func (a *App) snapshotServers() []MCPServer {
	names := a.sortedMCPServerNames()
	servers := make([]MCPServer, 0, len(names))
	for _, name := range names {
		servers = append(servers, a.mcpServers[name])
	}
	return servers
}

func (a *App) snapshotFunctions() map[string][]MCPFunction {
	a.mcpMu.RLock()
	defer a.mcpMu.RUnlock()
	out := make(map[string][]MCPFunction, len(a.mcpFunctions))
	for key, funcs := range a.mcpFunctions {
		out[key] = cloneMCPFunctions(funcs)
	}
	return out
}

func buildDefaultSystemPrompt(servers []MCPServer, functions map[string][]MCPFunction) string {
	var builder strings.Builder

	builder.WriteString("You are the humble-ai command line assistant. MCP server tooling is available.\n")
	builder.WriteString("When you need external data or actions, request an MCP call by emitting a tool call chunk with the target server, method, and JSON arguments.\n")
	builder.WriteString("Always wait for tool results before finalizing the response.\n")

	if len(servers) > 0 {
		builder.WriteString("\nAvailable MCP servers and functions:\n")
		for _, srv := range servers {
			builder.WriteString("  - ")
			builder.WriteString(srv.Name)
			desc := strings.TrimSpace(srv.Description)
			if desc != "" {
				builder.WriteString(": ")
				builder.WriteString(desc)
			}
			builder.WriteString("\n")

			tools := append([]MCPFunction(nil), functions[srv.Name]...)
			sort.Slice(tools, func(i, j int) bool {
				return tools[i].Name < tools[j].Name
			})
			if len(tools) == 0 {
				builder.WriteString("    (no functions reported)\n")
				continue
			}
			for _, fn := range tools {
				builder.WriteString("    * ")
				builder.WriteString(fn.Name)
				fnDesc := strings.TrimSpace(fn.Description)
				if fnDesc != "" {
					builder.WriteString(": ")
					builder.WriteString(fnDesc)
				}
				builder.WriteString("\n")
			}
		}
	}

	builder.WriteString("\nAfter the tool call completes, integrate the returned data into your answer.\n")
	return builder.String()
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
	defer func() {
		if err := a.mcp.Close(); err != nil && a.logger != nil {
			a.logger.Debugf("close MCP sessions: %v", err)
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
	case "/new":
		a.startNewSession()
	case "/set-model":
		return false, a.changeActiveModel(ctx)
	case "/mcp":
		return false, a.printMCPServers(ctx)
	case "/exit":
		return true, nil
	default:
		fmt.Fprintf(a.output, "Unknown command: %s\n", line)
	}
	return false, nil
}

func (a *App) printHelp() {
	fmt.Fprintln(a.output, "Available commands:")
	fmt.Fprintln(a.output, "  /help       Show this help message.")
	fmt.Fprintln(a.output, "  /new        Start a fresh session.")
	fmt.Fprintln(a.output, "  /set-model  Select one of the configured models as active.")
	fmt.Fprintln(a.output, "  /mcp        List enabled MCP servers and their functions.")
	fmt.Fprintln(a.output, "  /exit       Exit the application.")
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
		if m.Active {
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
	for i := range cfg.Models {
		cfg.Models[i].Active = i == choice-1
	}
	selected := cfg.Models[choice-1]
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
	return filepath.Join(a.homeDir, ".humble-ai-cli", "config.json")
}

func (a *App) startNewSession() {
	a.historyMu.Lock()
	a.historyPath = ""
	a.sessionStart = time.Time{}
	a.firstUserInput = ""
	a.historyMu.Unlock()

	a.messages = nil

	fmt.Fprintln(a.output, "Started a new session.")
}

func (a *App) handleUserMessage(ctx context.Context, content string) error {
	a.cfgMu.RLock()
	cfg := a.cfg
	a.cfgMu.RUnlock()

	activeModel, ok := cfg.ActiveModel()
	if !ok {
		fmt.Fprintln(a.output, "No active model is configured. Use /set-model to choose a model.")
		if len(cfg.Models) == 0 {
			fmt.Fprintf(a.output, "Add model configuration to %s and try again.\n", a.configFilePath())
		}
		return nil
	}

	if a.firstUserInput == "" {
		a.firstUserInput = content
	}

	fmt.Fprintln(a.output, "Waiting for response...")

	provider, err := a.factory.Create(activeModel)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	requestMessages := append([]llm.Message{}, a.messages...)
	requestMessages = append(requestMessages, llm.Message{Role: "user", Content: content})

	req := llm.ChatRequest{
		Model:        activeModel.Name,
		Messages:     requestMessages,
		SystemPrompt: a.systemPrompt,
		Stream:       true,
		Tools:        a.availableToolDefinitions(),
	}
	if data, err := json.Marshal(req); err == nil {
		a.logDebug("LLM request: %s", string(data))
	} else {
		a.logError("LLM request marshal error: %v", err)
	}

	reqCtx, cancel := context.WithCancel(ctx)
	reqCtx = llm.WithLogger(reqCtx, a.logger)
	a.enterResponding(cancel)
	defer a.leaveResponding()

	stream, err := provider.Stream(reqCtx, req)
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}

	var assistant strings.Builder
	thinking := struct {
		active         bool
		needsLineBreak bool
	}{
		active:         false,
		needsLineBreak: false,
	}
	openThinking := func() {
		if thinking.active {
			return
		}
		fmt.Fprintln(a.output, "<<< Thinking >>>")
		thinking.active = true
		thinking.needsLineBreak = false
	}
	closeThinking := func() {
		if !thinking.active {
			return
		}
		if thinking.needsLineBreak {
			fmt.Fprintln(a.output)
		}
		fmt.Fprintln(a.output, "<<< End Thinking >>>")
		thinking.active = false
		thinking.needsLineBreak = false
	}
	errored := false
	cancelledByUser := false

loop:
	for chunk := range stream {
		if chunk.Err != nil {
			closeThinking()
			fmt.Fprintf(a.errOutput, "Stream error: %v\n", chunk.Err)
			a.logError("LLM stream error: %v", chunk.Err)
			errored = true
			continue
		}

		switch chunk.Type {
		case llm.ChunkThinking:
			openThinking()
			if chunk.Content != "" {
				fmt.Fprint(a.output, chunk.Content)
				if strings.HasSuffix(chunk.Content, "\n") {
					thinking.needsLineBreak = false
				} else {
					thinking.needsLineBreak = true
				}
			}
		case llm.ChunkToken:
			closeThinking()
			fmt.Fprint(a.output, chunk.Content)
			assistant.WriteString(chunk.Content)
		case llm.ChunkToolCall:
			closeThinking()
			if chunk.ToolCall == nil {
				continue
			}
			a.logDebug("LLM requested MCP tool: server=%s method=%s", chunk.ToolCall.Server, chunk.ToolCall.Method)
			if err := a.processToolCall(reqCtx, cancel, chunk.ToolCall); err != nil {
				if errors.Is(err, errToolDeclined) {
					cancelledByUser = true
				} else {
					fmt.Fprintf(a.errOutput, "MCP call failed: %v\n", err)
					a.logError("MCP call handling failed: %v", err)
				}
				errored = true
				break loop
			}
		case llm.ChunkError:
			closeThinking()
			fmt.Fprintf(a.errOutput, "Stream error: %v\n", chunk.Err)
			a.logError("LLM stream error chunk: %v", chunk.Err)
			errored = true
		case llm.ChunkDone:
			closeThinking()
			// finished
		}
	}

	closeThinking()

	if cancelledByUser {
		a.logDebug("LLM response cancelled by user")
		return nil
	}

	if reqCtx.Err() != nil {
		fmt.Fprintln(a.output, "\nResponse cancelled.")
		a.logDebug("LLM response context cancelled: %v", reqCtx.Err())
		return nil
	}

	if assistant.Len() > 0 {
		fmt.Fprintln(a.output)
	}

	if errored {
		a.logDebug("LLM response aborted due to stream error")
		return nil
	}
	a.logDebug("LLM response: %s", assistant.String())

	now := a.clock.Now()

	a.messages = append(a.messages,
		llm.Message{Role: "user", Content: content},
		llm.Message{Role: "assistant", Content: assistant.String()},
	)

	if err := a.persistHistory(activeModel.Name, now); err != nil {
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
	dir := a.historyRoot
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

func (a *App) printMCPServers(ctx context.Context) error {
	if a.mcp == nil {
		fmt.Fprintln(a.output, "MCP integration is not configured.")
		return nil
	}

	if err := a.loadMCPFunctions(ctx); err != nil {
		return err
	}

	names := a.sortedMCPServerNames()
	if len(names) == 0 {
		fmt.Fprintln(a.output, "No MCP servers are currently enabled.")
		return nil
	}

	fmt.Fprintln(a.output, "Enabled MCP servers:")
	a.mcpMu.RLock()
	for _, name := range names {
		srv := a.mcpServers[name]
		desc := strings.TrimSpace(srv.Description)
		if desc == "" {
			desc = "No description provided."
		}
		fmt.Fprintf(a.output, "%s - %s\n", srv.Name, desc)

		tools := append([]MCPFunction(nil), a.mcpFunctions[srv.Name]...)
		sort.Slice(tools, func(i, j int) bool {
			return tools[i].Name < tools[j].Name
		})
		if len(tools) == 0 {
			fmt.Fprintln(a.output, "  (no functions reported)")
			continue
		}

		for _, tool := range tools {
			desc := strings.TrimSpace(tool.Description)
			if desc == "" {
				desc = "No description provided."
			}
			fmt.Fprintf(a.output, "  - %s: %s\n", tool.Name, desc)
		}
	}
	a.mcpMu.RUnlock()
	return nil
}

func (a *App) availableToolDefinitions() []llm.ToolDefinition {
	names := a.sortedMCPServerNames()
	if len(names) == 0 {
		return nil
	}

	a.mcpMu.RLock()
	defer a.mcpMu.RUnlock()
	defs := make([]llm.ToolDefinition, 0)
	for _, name := range names {
		srv := a.mcpServers[name]
		serverDesc := strings.TrimSpace(srv.Description)
		functions := append([]MCPFunction(nil), a.mcpFunctions[name]...)
		sort.Slice(functions, func(i, j int) bool {
			return functions[i].Name < functions[j].Name
		})
		for _, fn := range functions {
			desc := strings.TrimSpace(fn.Description)
			if desc == "" {
				desc = "No description provided."
			}
			if serverDesc != "" {
				desc = fmt.Sprintf("%s — %s", serverDesc, desc)
			}
			desc = fmt.Sprintf("%s (server: %s)", desc, srv.Name)
			toolName := fmt.Sprintf("%s__%s", srv.Name, fn.Name)
			defs = append(defs, llm.ToolDefinition{
				Name:        toolName,
				Description: desc,
				Server:      srv.Name,
				Method:      fn.Name,
				Parameters:  cloneParameters(fn.Parameters),
			})
		}
	}
	return defs
}

func (a *App) functionDescription(server, method string) string {
	a.mcpMu.RLock()
	defer a.mcpMu.RUnlock()
	for _, fn := range a.mcpFunctions[server] {
		if fn.Name == method {
			return fn.Description
		}
	}
	return ""
}

func (a *App) logDebug(format string, args ...any) {
	if a.logger != nil {
		a.logger.Debugf(format, args...)
	}
}

func (a *App) logError(format string, args ...any) {
	if a.logger != nil {
		a.logger.Errorf(format, args...)
	}
}

func (a *App) processToolCall(ctx context.Context, cancel context.CancelFunc, call *llm.ToolCall) error {
	if call == nil {
		return nil
	}
	a.logDebug("MCP call request received: server=%s method=%s args=%v", call.Server, call.Method, call.Arguments)

	fmt.Fprintln(a.output, "\nMCP tool call")
	fmt.Fprintf(a.output, "Server: %s\n", call.Server)
	fmt.Fprintf(a.output, "Tool: %s\n", call.Method)
	fmt.Fprintln(a.output, "Arguments:")

	keys := make([]string, 0, len(call.Arguments))
	for key := range call.Arguments {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		fmt.Fprintln(a.output, "  (none)")
	} else {
		for _, key := range keys {
			fmt.Fprintf(a.output, "  %s: %s\n", key, formatToolArgument(call.Arguments[key]))
		}
	}

	for {
		fmt.Fprint(a.output, "Call now? (Y/N): ")
		answer, err := a.readLine()
		if err != nil {
			return err
		}

		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			if a.mcp == nil {
				return errors.New("mcp executor not configured")
			}

			a.logDebug("MCP call start: server=%s method=%s args=%v", call.Server, call.Method, call.Arguments)
			result, err := a.mcp.Call(ctx, call.Server, call.Method, call.Arguments)
			if err != nil {
				if call.Respond != nil {
					_ = call.Respond(ctx, llm.ToolResult{Content: err.Error(), IsError: true})
				}
				a.logError("MCP call error: server=%s method=%s err=%v", call.Server, call.Method, err)
				return err
			}

			if call.Respond != nil {
				if err := call.Respond(ctx, result); err != nil && !errors.Is(err, context.Canceled) {
					return fmt.Errorf("deliver MCP result: %w", err)
				}
			}

			a.logDebug("MCP call success: server=%s method=%s result=%s", call.Server, call.Method, strings.TrimSpace(result.Content))
			fmt.Fprintln(a.output, "MCP call completed.")
			return nil
		case "n", "no":
			if call.Respond != nil {
				_ = call.Respond(ctx, llm.ToolResult{Content: "user cancelled MCP call", IsError: true})
			}
			cancel()
			a.logDebug("MCP call cancelled by user: server=%s method=%s", call.Server, call.Method)
			fmt.Fprintln(a.output, "MCP call cancelled by user.")
			return errToolDeclined
		default:
			fmt.Fprintln(a.output, "Please answer with Y or N.")
		}
	}
}

func formatToolArgument(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case json.Number:
		return v.String()
	case float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, bool:
		return fmt.Sprint(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
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
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "session"
	}

	const maxLen = 10
	count := 0
	var builder strings.Builder
	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			count++
			if count >= maxLen {
				break
			}
		}
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

func (a *App) sortedMCPServerNames() []string {
	if len(a.mcpServers) == 0 {
		return nil
	}
	names := make([]string, 0, len(a.mcpServers))
	for name := range a.mcpServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func cloneParameters(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	data, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func cloneMCPFunctions(funcs []MCPFunction) []MCPFunction {
	if len(funcs) == 0 {
		return nil
	}
	out := make([]MCPFunction, len(funcs))
	for i, fn := range funcs {
		out[i] = MCPFunction{
			Name:        fn.Name,
			Description: fn.Description,
			Parameters:  cloneParameters(fn.Parameters),
		}
	}
	return out
}
