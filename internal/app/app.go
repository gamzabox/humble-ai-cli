package app

import (
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

	"github.com/gamzabox/humble-ai-cli/internal/config"
	"github.com/gamzabox/humble-ai-cli/internal/llm"
	"github.com/gamzabox/humble-ai-cli/internal/logging"
	mcpkg "github.com/gamzabox/humble-ai-cli/internal/mcp"
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

// MCPConfiguredServer describes a stored MCP server entry.
type MCPConfiguredServer = mcpkg.ConfiguredServer

// MCPExecutor resolves server metadata and executes MCP tool calls.
type MCPExecutor interface {
	EnabledServers() []MCPServer
	Describe(server string) (MCPServer, bool)
	Call(ctx context.Context, server, method string, arguments map[string]any) (llm.ToolResult, error)
	Tools(ctx context.Context, server string) ([]MCPFunction, error)
	Close() error
	Reload() error
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
	output      io.Writer
	errOutput   io.Writer
	lineReader  lineReader
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

const (
	routeIntentServerName = "route-intent"
	routeIntentToolName   = "chooseFunction"
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

	app.lineReader = createLineReader(opts.Input, app.output, func() {
		app.handleInterrupt()
	})

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
	return "You are a **tool-enabled Humble AI Agent** operating with MCP (Model Context Protocol) servers.  \n" +
		"A **tool** corresponds to an MCP server, and a **function** is an action exposed by that tool.\n\n" +
		"Your goal is to achieve the user’s intent **safely, accurately, and efficiently using available functions**.\n\n" +
		"---\n\n" +
		"# 1) Core Behavior Rules\n" +
		"1. **Call a function only when required by the user's request.**  \n" +
		"   If a function is unnecessary, provide a natural-language answer immediately.\n" +
		"2. **Never call a function that is not declared in the system prompt.**  \n" +
		"   If no functions are available, answer the user directly.\n" +
		"3. **Do NOT call the same function with the same arguments more than once.**\n" +
		"4. **If ANY function call returns an error:**\n" +
		"   - stop calling functions  \n" +
		"   - summarize the issue briefly  \n" +
		"   - ask the user how to continue (retry, alternative, more info)\n" +
		"5. **Function-call messages must contain ONLY valid JSON for the call.**  \n" +
		"   No natural language before or after it.\n" +
		"6. **Ask the user for missing information before calling functions.**  \n" +
		"   Do not guess required parameters.\n" +
		"7. **If you already have enough information to answer, do not call a function.**\n" +
		"8. Final natural-language answers (not function calls) must include:\n" +
		"   - short reasoning summary  \n" +
		"   - assumptions or limitations  \n" +
		"   - optional next steps  \n" +
		"9. Keep final answers **clear and concise**.\n\n" +
		"---\n\n" +
		"# 2) Function Selection Flow (chooseFunction MUST be used)\n" +
		"Before calling EACH MCP function:\n" +
		"1. Call **chooseFunction** with:\n" +
		"   - `functionName`: the selected function  \n" +
		"   - `reason`: why this function is necessary  \n" +
		"2. Receive that function’s input schema.\n" +
		"3. Create a function call using the schema and required properties.\n" +
		"4. Wait for its response and incorporate results into the final answer. If more function call is needed then starts function selection flow again.\n\n" +
		"## Choose Function Call Example\n" +
		"{\n" +
		"  \"chooseFunction\": {\n" +
		"    \"functionName\": \"chooseFunction\",\n" +
		"    \"reason\": \"Need to perform the awesome action\"\n" +
		"  }\n" +
		"}\n\n" +
		"---\n\n" +
		"# 3) Function Call Protocol\n" +
		"- One message = one function call JSON only.\n" +
		"- Do NOT combine multiple calls in the same message.\n" +
		"- Review previous calls to avoid duplication.\n\n" +
		"---\n\n" +
		"# 4) Error Handling\n" +
		"If a function response contains an error:\n" +
		"1. Stop all further function calls\n" +
		"2. Provide a short and user-friendly summary\n" +
		"3. Ask how they want to proceed\n" +
		"Do not reveal internal logs or stack traces; keep it simple and relevant.\n\n" +
		"---\n\n" +
		"# 5) Handling Multiple Functions\n" +
		"If multiple functions are used:\n" +
		"- Validate and cross-check results when possible\n" +
		"- Explain conflicts using natural-language only in the final answer\n" +
		"- Do not mix any explanation into function call messages\n\n" +
		"---\n\n" +
		"# 6) Asking for Missing Information\n" +
		"When user input is incomplete or ambiguous, ask for only what is strictly necessary to proceed.\n" +
		"Examples:\n" +
		"- “Which browser should I use?”\n" +
		"- “Do you have login credentials?”\n" +
		"- “Which selector should I extract data from?”\n" +
		"Ask minimal questions required to make the next legitimate function call.\n\n" +
		"---\n"
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

		line, err := a.readLine("humble-ai> ")
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

func (a *App) readLine(prompt string) (string, error) {
	if a.lineReader == nil {
		return "", errors.New("line reader not configured")
	}
	return a.lineReader.ReadLine(prompt)
}

func (a *App) handleCommand(ctx context.Context, line string) (bool, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false, nil
	}

	cmd := fields[0]
	args := fields[1:]

	switch cmd {
	case "/help":
		a.printHelp()
	case "/new":
		a.startNewSession()
	case "/set-model":
		return false, a.changeActiveModel(ctx)
	case "/set-tool-mode":
		return false, a.setToolMode(args)
	case "/mcp":
		return false, a.printMCPServers(ctx)
	case "/toggle-mcp":
		return false, a.toggleMCPServer(ctx)
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
	fmt.Fprintln(a.output, "  /set-tool-mode [auto|manual]  Choose whether MCP tools run automatically.")
	fmt.Fprintln(a.output, "  /mcp        List enabled MCP servers and their functions.")
	fmt.Fprintln(a.output, "  /toggle-mcp Toggle whether an MCP server is enabled.")
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
	choiceLine, err := a.readLine("Choice: ")
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

func (a *App) setToolMode(args []string) error {
	if len(args) != 1 {
		fmt.Fprintln(a.output, "Usage: /set-tool-mode [auto|manual]")
		return nil
	}

	mode := strings.ToLower(strings.TrimSpace(args[0]))
	if mode != string(config.ToolCallModeAuto) && mode != string(config.ToolCallModeManual) {
		fmt.Fprintln(a.output, "Please enter either auto or manual.")
		return nil
	}

	a.cfgMu.RLock()
	cfg := a.cfg
	a.cfgMu.RUnlock()

	cfg.ToolCallMode = mode
	if err := a.store.Save(cfg); err != nil {
		return err
	}

	a.cfgMu.Lock()
	a.cfg = cfg
	a.cfgMu.Unlock()

	fmt.Fprintf(a.output, "Tool call mode set to %s.\n", mode)
	return nil
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

	toolDefs := a.availableToolDefinitions()
	history := append([]llm.Message{}, a.messages...)
	requestMessages := make([]llm.Message, 0, len(history)+2)
	if contextPrompt := toolContextPrompt(toolDefs); strings.TrimSpace(contextPrompt) != "" {
		requestMessages = append(requestMessages, llm.Message{Role: "assistant", Content: contextPrompt})
	}
	requestMessages = append(requestMessages, history...)
	requestMessages = append(requestMessages, llm.Message{Role: "user", Content: content})

	req := llm.ChatRequest{
		Model:        activeModel.Name,
		Messages:     requestMessages,
		SystemPrompt: a.systemPrompt,
		Stream:       true,
		Tools:        toolDefs,
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
			if strings.TrimSpace(chunk.Content) == "" {
				continue
			}
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
			assistant.Reset()
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

func (a *App) toggleMCPServer(ctx context.Context) error {
	entries, err := mcpkg.ListConfiguredServers(a.homeDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(a.output, "No MCP servers configured in mcp-servers.json.")
		return nil
	}

	fmt.Fprintln(a.output, "MCP servers found in mcp-servers.json:")
	for idx, entry := range entries {
		status := "disabled"
		if entry.Enabled {
			status = "enabled"
		}
		fmt.Fprintf(a.output, "  %d) %s: %s\n", idx+1, entry.Name, status)
	}

	choiceLine, err := a.readLine("Choose the MCP server to enable/disable (0 to cancel): ")
	if err != nil {
		return err
	}

	choiceLine = strings.TrimSpace(choiceLine)
	if choiceLine == "" {
		fmt.Fprintln(a.output, "Toggle cancelled.")
		return nil
	}

	choice, err := strconv.Atoi(choiceLine)
	if err != nil || choice < 0 || choice > len(entries) {
		fmt.Fprintln(a.output, "Invalid selection.")
		return nil
	}
	if choice == 0 {
		fmt.Fprintln(a.output, "Toggle cancelled.")
		return nil
	}

	selected := entries[choice-1]
	updated, err := mcpkg.SetServerEnabled(a.homeDir, selected.Key, !selected.Enabled)
	if err != nil {
		return err
	}

	status := "disabled"
	if updated.Enabled {
		status = "enabled"
	}
	fmt.Fprintf(a.output, "Server %q is now %s.\n", updated.Name, status)

	if a.mcp != nil {
		if err := a.mcp.Reload(); err != nil {
			fmt.Fprintf(a.errOutput, "Failed to reload MCP servers: %v\n", err)
		}
	}

	refreshed, err := mcpkg.ListConfiguredServers(a.homeDir)
	if err != nil {
		fmt.Fprintf(a.errOutput, "Failed to refresh MCP server list: %v\n", err)
		return nil
	}
	if err := a.applyConfiguredMCPServers(ctx, refreshed); err != nil {
		fmt.Fprintf(a.errOutput, "Failed to update MCP server cache: %v\n", err)
	}
	return nil
}

func (a *App) applyConfiguredMCPServers(ctx context.Context, entries []MCPConfiguredServer) error {
	updated := make(map[string]MCPServer, len(entries))
	for _, entry := range entries {
		if !entry.Enabled {
			continue
		}
		updated[entry.Name] = MCPServer{
			Name:        entry.Name,
			Description: entry.Description,
		}
	}

	a.mcpServers = updated

	a.mcpMu.Lock()
	for name := range a.mcpFunctions {
		if _, ok := updated[name]; !ok {
			delete(a.mcpFunctions, name)
		}
	}
	a.mcpMu.Unlock()

	return a.loadMCPFunctions(ctx)
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
		fmt.Fprintf(a.output, "%s\n", srv.Name)

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
	defs := []llm.ToolDefinition{routeIntentToolDefinition()}
	names := a.sortedMCPServerNames()
	if len(names) == 0 {
		return defs
	}

	a.mcpMu.RLock()
	defer a.mcpMu.RUnlock()
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
			serverName := strings.TrimSpace(srv.Name)
			toolName := strings.TrimSpace(fn.Name)
			namespaced := fn.Name
			if serverName != "" && toolName != "" {
				namespaced = fmt.Sprintf("%s__%s", serverName, toolName)
			} else if toolName != "" {
				namespaced = toolName
			}
			if serverDesc != "" {
				desc = fmt.Sprintf("%s — %s", serverDesc, desc)
			}
			params := cloneParameters(fn.Parameters)
			if params == nil {
				params = defaultToolParameters()
			}
			defs = append(defs, llm.ToolDefinition{
				Name:        namespaced,
				Description: desc,
				Server:      srv.Name,
				Method:      fn.Name,
				Parameters:  params,
			})
		}
	}
	return defs
}

func toolContextPrompt(defs []llm.ToolDefinition) string {
	type toolEntry struct {
		name        string
		description string
	}

	builder := strings.Builder{}
	builder.WriteString("# Connected Tools\n\n")

	groups := make(map[string][]toolEntry)
	for _, def := range defs {
		if def.Server == routeIntentServerName && def.Name == routeIntentToolName {
			continue
		}
		server := strings.TrimSpace(def.Server)
		if server == "" {
			server = "default"
		}
		desc := strings.TrimSpace(def.Description)
		if desc == "" {
			desc = "No description provided."
		}
		groups[server] = append(groups[server], toolEntry{
			name:        def.Name,
			description: desc,
		})
	}

	if len(groups) == 0 {
		builder.WriteString("**NO FUNCTION CONNECTED**\n")
	} else {
		serverNames := make([]string, 0, len(groups))
		for server := range groups {
			serverNames = append(serverNames, server)
		}
		sort.Strings(serverNames)

		for _, server := range serverNames {
			builder.WriteString("## MCP Server: ")
			builder.WriteString(server)
			builder.WriteString("\n\n")

			entries := groups[server]
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].name < entries[j].name
			})

			for _, entry := range entries {
				builder.WriteString("- function name: **")
				builder.WriteString(entry.name)
				builder.WriteString("**\n")
				builder.WriteString("- description: ")
				builder.WriteString(entry.description)
				builder.WriteString("\n\n")
			}
		}
	}

	builder.WriteString(functionCallSchemaPrompt)
	return strings.TrimRight(builder.String(), "\n")
}

const functionCallSchemaPrompt = "\n# Function Call Schema and Example\n## Schema\n{\n  \"functionCall\": {\n    \"server\": \"server_name\",\n    \"name\": \"tool name\",\n    \"arguments\": {\n      \"arg1 name\": \"argument1 value\",\n      \"arg2 name\": \"argument2 value\",\n    },\n    \"reason\": \"reason why calling this tool\"\n  }\n}\n\n## Example\n{\n  \"functionCall\": {\n    \"server\": \"good-server\",\n    \"name\": \"good-tool\",\n    \"arguments\": {\n      \"goodArg\": \"nice\"\n    },\n    \"reason\": \"why this tool call is needed\"\n  }\n}\n"

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

func routeIntentToolDefinition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        routeIntentToolName,
		Description: "Choose the MCP function whose schema should be returned before execution.",
		Server:      routeIntentServerName,
		Method:      routeIntentToolName,
		Parameters: map[string]any{
			"$schema":              "http://json-schema.org/draft-07/schema#",
			"additionalProperties": false,
			"properties": map[string]any{
				"functionName": map[string]any{
					"description": "The fully-qualified MCP function name (e.g., server__function) to fetch the schema for.",
					"type":        "string",
				},
				"reason": map[string]any{
					"description": "A short justification that explains why this function was selected.",
					"type":        "string",
				},
			},
			"required": []any{"functionName"},
			"type":     "object",
		},
	}
}

func defaultToolParameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": true,
	}
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
	if call.Server == routeIntentServerName && call.Method == routeIntentToolName {
		return a.handleChooseFunctionCall(ctx, call)
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

	if a.toolCallMode() == config.ToolCallModeAuto {
		return a.executeToolCall(ctx, call)
	}
	return a.confirmToolCall(ctx, cancel, call)
}

func (a *App) toolCallMode() config.ToolCallMode {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg.EffectiveToolCallMode()
}

func (a *App) executeToolCall(ctx context.Context, call *llm.ToolCall) error {
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
}

func (a *App) confirmToolCall(ctx context.Context, cancel context.CancelFunc, call *llm.ToolCall) error {
	for {
		answer, err := a.readLine("Call now? (Y/N): ")
		if err != nil {
			return err
		}

		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			return a.executeToolCall(ctx, call)
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

func (a *App) handleChooseFunctionCall(ctx context.Context, call *llm.ToolCall) error {
	functionName := extractChooseFunctionName(call.Arguments)
	if functionName == "" {
		return a.respondChooseFunctionError(ctx, call, "functionName argument is required")
	}

	definition, ok := a.toolDefinitionByName(functionName)
	if !ok {
		return a.respondChooseFunctionError(ctx, call, fmt.Sprintf("function %q is not available", functionName))
	}

	schema := cloneParameters(definition.Parameters)
	if schema == nil {
		schema = defaultToolParameters()
	}

	payload := map[string]any{
		"functionName": functionName,
		"inputSchema":  schema,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return a.respondChooseFunctionError(ctx, call, fmt.Sprintf("failed to encode schema: %v", err))
	}

	if call.Respond != nil {
		if err := call.Respond(ctx, llm.ToolResult{Content: string(data)}); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("deliver chooseFunction schema: %w", err)
		}
	}

	a.logDebug("chooseFunction schema provided for %s", functionName)
	fmt.Fprintf(a.output, "Provided schema for function %q.\n", functionName)
	return nil
}

func (a *App) respondChooseFunctionError(ctx context.Context, call *llm.ToolCall, message string) error {
	if call != nil && call.Respond != nil {
		_ = call.Respond(ctx, llm.ToolResult{Content: message, IsError: true})
	}
	fmt.Fprintf(a.errOutput, "chooseFunction error: %s\n", message)
	a.logError("chooseFunction error: %s", message)
	return nil
}

func extractChooseFunctionName(args map[string]any) string {
	for _, key := range []string{"functionName", "toolName", "tool"} {
		value, ok := args[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			if name := strings.TrimSpace(v); name != "" {
				return name
			}
		default:
			if name := strings.TrimSpace(fmt.Sprint(v)); name != "" {
				return name
			}
		}
	}
	return ""
}

func (a *App) toolDefinitionByName(name string) (llm.ToolDefinition, bool) {
	for _, def := range a.availableToolDefinitions() {
		if def.Name == name && def.Server != routeIntentServerName {
			return def, true
		}
	}
	return llm.ToolDefinition{}, false
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
