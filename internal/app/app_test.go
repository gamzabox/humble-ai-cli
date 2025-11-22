package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkoukk/tiktoken-go"

	"github.com/gamzabox/humble-ai-cli/internal/app"
	"github.com/gamzabox/humble-ai-cli/internal/config"
	"github.com/gamzabox/humble-ai-cli/internal/llm"
)

type stubStore struct {
	mu         sync.Mutex
	cfg        config.Config
	shouldFail bool
}

func (s *stubStore) Load() (config.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shouldFail {
		return config.Config{}, errors.New("load failed")
	}
	return s.cfg, nil
}

func (s *stubStore) Save(cfg config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shouldFail {
		return errors.New("save failed")
	}
	s.cfg = cfg
	return nil
}

type stubFactory struct {
	mu        sync.Mutex
	providers map[string]llm.ChatProvider
}

func newStubFactory() *stubFactory {
	return &stubFactory{providers: make(map[string]llm.ChatProvider)}
}

func (f *stubFactory) Register(modelName string, provider llm.ChatProvider) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers[modelName] = provider
}

func (f *stubFactory) Create(model config.Model, chunkLimit int) (llm.ChatProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.providers[model.Name]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return p, nil
}

func writeMCPServersConfig(t *testing.T, home string, servers map[string]map[string]any) {
	t.Helper()
	configDir := filepath.Join(home, ".humble-ai-cli")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config directory: %v", err)
	}

	payload := map[string]any{
		"mcpServers": servers,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal MCP server config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp-servers.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("failed to write MCP server config: %v", err)
	}
}

func intPtr(v int) *int {
	return &v
}

func runContextRetentionRequests(t *testing.T, retention *int, prompts []string) []llm.ChatRequest {
	t.Helper()

	home := t.TempDir()
	sessionDir := filepath.Join(home, ".humble-ai-cli", "sessions")
	cfg := config.Config{
		ContextRetentionTurns: retention,
		Models: []config.Model{
			{Name: "retention-model", Provider: "openai", APIKey: "sk-xxx", Active: true},
		},
	}
	store := &stubStore{cfg: cfg}

	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkToken, Content: "OK"},
		},
	}
	factory := newStubFactory()
	factory.Register("retention-model", provider)

	lines := append([]string{}, prompts...)
	lines = append(lines, "/exit")
	input := strings.Join(lines, "\n") + "\n"
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          strings.NewReader(input),
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: sessionDir,
		HomeDir:        home,
		Clock:          fixedClock(time.Now()),
		MCP:            &stubMCP{},
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return provider.Requests()
}

type recordingProvider struct {
	mu       sync.Mutex
	requests []llm.ChatRequest
	chunks   []llm.StreamChunk
}

func (p *recordingProvider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()

	out := make(chan llm.StreamChunk, len(p.chunks)+1)
	go func() {
		for _, chunk := range p.chunks {
			out <- chunk
		}
		out <- llm.StreamChunk{Type: llm.ChunkDone}
		close(out)
	}()
	return out, nil
}

func (p *recordingProvider) Requests() []llm.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]llm.ChatRequest, len(p.requests))
	copy(out, p.requests)
	return out
}

type sequencedRecordingProvider struct {
	mu        sync.Mutex
	responses []string
	requests  []llm.ChatRequest
	index     int
}

func newSequencedRecordingProvider(responses ...string) *sequencedRecordingProvider {
	return &sequencedRecordingProvider{responses: responses}
}

func (p *sequencedRecordingProvider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	idx := p.index
	p.index++
	var resp string
	if idx < len(p.responses) {
		resp = p.responses[idx]
	}
	p.mu.Unlock()

	out := make(chan llm.StreamChunk, 2)
	go func() {
		if resp != "" {
			out <- llm.StreamChunk{Type: llm.ChunkToken, Content: resp}
		}
		out <- llm.StreamChunk{Type: llm.ChunkDone}
		close(out)
	}()
	return out, nil
}

func (p *sequencedRecordingProvider) Requests() []llm.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]llm.ChatRequest, len(p.requests))
	copy(out, p.requests)
	return out
}

type toolRequestProvider struct {
	mu          sync.Mutex
	requests    []llm.ChatRequest
	call        llm.ToolCall
	after       []llm.StreamChunk
	onResponded func(llm.ToolResult)
}

func (p *toolRequestProvider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	call := p.call
	after := append([]llm.StreamChunk(nil), p.after...)
	onResponded := p.onResponded
	p.mu.Unlock()

	out := make(chan llm.StreamChunk)
	go func() {
		defer close(out)

		resultCh := make(chan llm.ToolResult, 1)
		previousResponder := call.Respond
		call.Respond = func(ctx context.Context, result llm.ToolResult) error {
			if previousResponder != nil {
				if err := previousResponder(ctx, result); err != nil {
					return err
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case resultCh <- result:
				return nil
			}
		}

		out <- llm.StreamChunk{Type: llm.ChunkThinking}
		out <- llm.StreamChunk{Type: llm.ChunkToolCall, ToolCall: &call}

		res := <-resultCh
		if onResponded != nil {
			onResponded(res)
		}

		for _, chunk := range after {
			out <- chunk
		}
		out <- llm.StreamChunk{Type: llm.ChunkDone}
	}()
	return out, nil
}

func (p *toolRequestProvider) Requests() []llm.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]llm.ChatRequest, len(p.requests))
	copy(out, p.requests)
	return out
}

type recordedMCPCall struct {
	Server    string
	Method    string
	Arguments map[string]any
}

type stubMCP struct {
	mu            sync.Mutex
	calls         []recordedMCPCall
	description   app.MCPServer
	servers       []app.MCPServer
	toolset       map[string][]app.MCPFunction
	response      llm.ToolResult
	responseError error
}

func cloneTestParams(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func (s *stubMCP) Describe(server string) (app.MCPServer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.description.Name != "" && s.description.Name == server {
		return s.description, true
	}
	for _, srv := range s.servers {
		if srv.Name == server {
			return srv, true
		}
	}
	return app.MCPServer{}, false
}

func (s *stubMCP) EnabledServers() []app.MCPServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]app.MCPServer, len(s.servers))
	copy(out, s.servers)
	return out
}

func (s *stubMCP) Call(ctx context.Context, server, method string, arguments map[string]any) (llm.ToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := recordedMCPCall{
		Server:    server,
		Method:    method,
		Arguments: arguments,
	}
	s.calls = append(s.calls, call)
	return s.response, s.responseError
}

func (s *stubMCP) Calls() []recordedMCPCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedMCPCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *stubMCP) Tools(ctx context.Context, server string) ([]app.MCPFunction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tools := s.toolset[server]
	out := make([]app.MCPFunction, len(tools))
	for i, fn := range tools {
		out[i] = app.MCPFunction{
			Name:        fn.Name,
			Description: fn.Description,
			Parameters:  cloneTestParams(fn.Parameters),
		}
	}
	return out, nil
}

func (s *stubMCP) Close() error { return nil }

func (s *stubMCP) Reload() error { return nil }

func TestAppPromptsToSetModelWhenActiveModelMissing(t *testing.T) {
	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "gpt-4o", Provider: "openai", APIKey: "sk-xx"},
			},
		},
	}
	factory := newStubFactory()
	input := strings.NewReader("안녕?\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: t.TempDir(),
		Clock:          fixedClock(time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)),
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "No active model is configured.") {
		t.Fatalf("expected guidance output, got:\n%s", got)
	}
	if strings.Contains(got, "Waiting for response") {
		t.Fatalf("should not start response when model missing")
	}
}

func TestAppDisplaysHelpCommand(t *testing.T) {
	store := &stubStore{
		cfg: config.Config{},
	}
	factory := newStubFactory()
	input := strings.NewReader("/help\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: t.TempDir(),
		Clock:          fixedClock(time.Now()),
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := output.String()
	for _, cmd := range []string{"/help", "/set-model", "/set-tool-mode", "/exit"} {
		if !strings.Contains(got, cmd) {
			t.Fatalf("expected help output to include %s, got:\n%s", cmd, got)
		}
	}
}

func TestAppPrintsStartupGuide(t *testing.T) {
	home := t.TempDir()
	store := &stubStore{
		cfg: config.Config{},
	}
	factory := newStubFactory()
	input := strings.NewReader("/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		Version:        "9.9.9-test",
		Clock:          fixedClock(time.Now()),
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "Humble AI CLI version 9.9.9-test") {
		t.Fatalf("expected startup guide to include version, got:\n%s", got)
	}
	if !strings.Contains(got, "- Use /help for detailed commands.") {
		t.Fatalf("expected startup guide to include help hint, got:\n%s", got)
	}
	if !strings.Contains(got, "- Press CTRL+C to stop, CTRL+D to exit.") {
		t.Fatalf("expected startup guide to include shortcut hint, got:\n%s", got)
	}
}

func TestAppSetToolModeCommandUpdatesConfig(t *testing.T) {
	store := &stubStore{
		cfg: config.Config{
			ToolCallMode: "manual",
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk", Active: true},
			},
		},
	}
	factory := newStubFactory()
	input := strings.NewReader("/set-tool-mode auto\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: t.TempDir(),
		Clock:          fixedClock(time.Now()),
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if store.cfg.ToolCallMode != "auto" {
		t.Fatalf("expected store to persist auto mode, got %s", store.cfg.ToolCallMode)
	}

	got := output.String()
	if !strings.Contains(got, "Tool call mode set to auto") {
		t.Fatalf("expected confirmation output, got:\n%s", got)
	}
}

func TestAppSetToolModeCommandRejectsInvalidValue(t *testing.T) {
	store := &stubStore{
		cfg: config.Config{
			ToolCallMode: "manual",
		},
	}
	factory := newStubFactory()
	input := strings.NewReader("/set-tool-mode maybe\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: t.TempDir(),
		Clock:          fixedClock(time.Now()),
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if store.cfg.ToolCallMode != "manual" {
		t.Fatalf("expected tool call mode to remain manual, got %s", store.cfg.ToolCallMode)
	}

	got := output.String()
	if !strings.Contains(got, "Please enter either auto or manual") {
		t.Fatalf("expected validation message, got:\n%s", got)
	}
}

func TestAppToolCallAutoModeSkipsPrompt(t *testing.T) {
	home := t.TempDir()
	store := &stubStore{
		cfg: config.Config{
			ToolCallMode: "auto",
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk", Active: true},
			},
		},
	}

	resultCh := make(chan llm.ToolResult, 1)
	provider := &toolRequestProvider{
		call: llm.ToolCall{
			Server:      "calculator",
			Method:      "add",
			Description: "Add numbers.",
			Arguments: map[string]any{
				"a": float64(2),
				"b": float64(3),
			},
		},
		after: []llm.StreamChunk{
			{Type: llm.ChunkToken, Content: "Final answer: 5"},
		},
		onResponded: func(res llm.ToolResult) {
			resultCh <- res
		},
	}
	factory := newStubFactory()
	factory.Register("stub-model", provider)

	mcpExec := &stubMCP{
		servers: []app.MCPServer{
			{Name: "calculator", Description: "Adds numbers via MCP."},
		},
		toolset: map[string][]app.MCPFunction{
			"calculator": {
				{Name: "add", Description: "Add two numbers."},
			},
		},
		response: llm.ToolResult{Content: "5"},
	}

	input := strings.NewReader("Please add\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		MCP:            mcpExec,
		Clock:          fixedClock(time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)),
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	select {
	case res := <-resultCh:
		if res.Content != "5" {
			t.Fatalf("unexpected tool result content: %s", res.Content)
		}
	default:
		t.Fatalf("expected tool result to be delivered")
	}

	if len(mcpExec.Calls()) != 1 {
		t.Fatalf("expected exactly one MCP call, got %d", len(mcpExec.Calls()))
	}

	got := output.String()
	if strings.Contains(got, "Call now?") {
		t.Fatalf("auto mode should not prompt for confirmation, got:\n%s", got)
	}
	if !strings.Contains(got, "> MCP call completed.") {
		t.Fatalf("expected call completion message, got:\n%s", got)
	}
	if !strings.Contains(got, "Final answer: 5") {
		t.Fatalf("expected final answer output, got:\n%s", got)
	}
}

func TestAppRespondsWithSchemaForChooseFunctionCall(t *testing.T) {
	home := t.TempDir()
	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk", Active: true},
			},
		},
	}

	resultCh := make(chan llm.ToolResult, 1)
	provider := &toolRequestProvider{
		call: llm.ToolCall{
			Server: "route-intent",
			Method: "chooseFunction",
			Arguments: map[string]any{
				"functionName": "calculator__add",
			},
		},
		onResponded: func(res llm.ToolResult) {
			resultCh <- res
		},
	}
	factory := newStubFactory()
	factory.Register("stub-model", provider)

	mcpExec := &stubMCP{
		servers: []app.MCPServer{
			{Name: "calculator", Description: "Adds numbers via MCP."},
		},
		toolset: map[string][]app.MCPFunction{
			"calculator": {
				{
					Name:        "add",
					Description: "Add two numbers.",
					Parameters: map[string]any{
						"$schema": "http://json-schema.org/draft-07/schema#",
						"type":    "object",
						"properties": map[string]any{
							"a": map[string]any{"type": "number"},
							"b": map[string]any{"type": "number"},
						},
						"required": []any{"a", "b"},
					},
				},
			},
		},
	}

	input := strings.NewReader("Please add\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		MCP:            mcpExec,
		Clock:          fixedClock(time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)),
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	select {
	case res := <-resultCh:
		if res.IsError {
			t.Fatalf("expected schema response without error, got: %+v", res)
		}
		var parsed struct {
			FunctionName string         `json:"functionName"`
			InputSchema  map[string]any `json:"inputSchema"`
		}
		if err := json.Unmarshal([]byte(res.Content), &parsed); err != nil {
			t.Fatalf("failed to parse schema payload: %v\ncontent: %s", err, res.Content)
		}
		if parsed.FunctionName != "calculator__add" {
			t.Fatalf("expected functionName calculator__add, got %q", parsed.FunctionName)
		}
		props, ok := parsed.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("expected properties in schema, got %v", parsed.InputSchema)
		}
		if _, ok := props["a"]; !ok {
			t.Fatalf("expected property a in schema: %v", props)
		}
		if _, ok := props["b"]; !ok {
			t.Fatalf("expected property b in schema: %v", props)
		}
	default:
		t.Fatalf("expected chooseFunction result to be delivered")
	}

	if len(mcpExec.Calls()) != 0 {
		t.Fatalf("expected no MCP calls when only requesting schema")
	}
}

func TestAppChooseFunctionErrorWhenFunctionMissing(t *testing.T) {
	home := t.TempDir()
	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk", Active: true},
			},
		},
	}

	resultCh := make(chan llm.ToolResult, 1)
	provider := &toolRequestProvider{
		call: llm.ToolCall{
			Server: "route-intent",
			Method: "chooseFunction",
			Arguments: map[string]any{
				"functionName": "missing__tool",
			},
		},
		onResponded: func(res llm.ToolResult) {
			resultCh <- res
		},
	}
	factory := newStubFactory()
	factory.Register("stub-model", provider)

	mcpExec := &stubMCP{
		servers: []app.MCPServer{},
		toolset: map[string][]app.MCPFunction{},
	}

	input := strings.NewReader("Please add\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		MCP:            mcpExec,
		Clock:          fixedClock(time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)),
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	select {
	case res := <-resultCh:
		if !res.IsError {
			t.Fatalf("expected error response for missing tool, got %+v", res)
		}
		if !strings.Contains(res.Content, "missing__tool") {
			t.Fatalf("expected error content to mention missing tool, got %q", res.Content)
		}
	default:
		t.Fatalf("expected chooseFunction error result")
	}
}

func TestAppStreamsResponseAndWritesHistory(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, ".humble-ai-cli", "sessions")
	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk-xxx", Active: true},
			},
		},
	}
	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkThinking, Content: "Analyzing the prompt"},
			{Type: llm.ChunkThinking, Content: "...checking context"},
			{Type: llm.ChunkToken, Content: "Hello"},
			{Type: llm.ChunkToken, Content: " "},
			{Type: llm.ChunkToken, Content: "World"},
		},
	}
	factory := newStubFactory()
	factory.Register("stub-model", provider)

	input := strings.NewReader("Hello?! there...\n/exit\n")
	var output bytes.Buffer

	now := time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: sessionDir,
		HomeDir:        home,
		Clock:          fixedClock(now),
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "Waiting for response...") {
		t.Fatalf("expected waiting indicator, got:\n%s", got)
	}

	startIdx := strings.Index(got, "<<< Thinking >>>")
	if startIdx == -1 {
		t.Fatalf("missing thinking start marker in output:\n%s", got)
	}

	reasoningIdx := strings.Index(got, "Analyzing the prompt...checking context")
	if reasoningIdx == -1 {
		t.Fatalf("expected concatenated thinking content in output:\n%s", got)
	}
	if reasoningIdx < startIdx {
		t.Fatalf("thinking content appears before start marker:\n%s", got)
	}
	endIdx := strings.Index(got, "<<< End Thinking >>>")
	if endIdx == -1 {
		t.Fatalf("missing thinking end marker in output:\n%s", got)
	}
	if endIdx < reasoningIdx {
		t.Fatalf("thinking end marker appears before content:\n%s", got)
	}

	answerIdx := strings.Index(got, "Hello World")
	if answerIdx == -1 {
		t.Fatalf("expected answer content in output:\n%s", got)
	}
	if answerIdx < endIdx {
		t.Fatalf("answer content appears before thinking finished:\n%s", got)
	}

	historyFiles, err := filepath.Glob(filepath.Join(sessionDir, "*.json"))
	if err != nil {
		t.Fatalf("failed to glob history: %v", err)
	}
	if len(historyFiles) != 1 {
		t.Fatalf("expected 1 history file, got %d", len(historyFiles))
	}

	base := filepath.Base(historyFiles[0])
	expectedPattern := regexp.MustCompile(`^\d{8}_\d{6}_[A-Za-z0-9]+\.json$`)
	if !expectedPattern.MatchString(base) {
		t.Fatalf("history filename %q does not match expected pattern", base)
	}

	data, err := os.ReadFile(historyFiles[0])
	if err != nil {
		t.Fatalf("failed to read history: %v", err)
	}

	var record struct {
		Messages []llm.Message `json:"messages"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("failed to decode history: %v", err)
	}
	if len(record.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(record.Messages))
	}
	if record.Messages[1].Content != "Hello World" {
		t.Fatalf("unexpected assistant message: %s", record.Messages[1].Content)
	}
}

func TestAppIncludesOllamaNumCtxOption(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, ".humble-ai-cli", "sessions")
	store := &stubStore{
		cfg: config.Config{
			OllamaNumCtx: 6144,
			Models: []config.Model{
				{Name: "ollama-model", Provider: "ollama", BaseURL: "http://localhost:11434", Active: true},
			},
		},
	}
	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkToken, Content: "Done"},
		},
	}
	factory := newStubFactory()
	factory.Register("ollama-model", provider)

	input := strings.NewReader("num ctx?\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: sessionDir,
		HomeDir:        home,
		Clock:          fixedClock(time.Now()),
		MCP:            &stubMCP{},
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected single request, got %d", len(requests))
	}

	options := requests[0].Options
	if options == nil {
		t.Fatalf("expected request options to include num_ctx")
	}
	got, ok := options["num_ctx"]
	if !ok {
		t.Fatalf("expected num_ctx option to be present, got %#v", options)
	}
	if got != 6144 {
		t.Fatalf("unexpected num_ctx value: %v", got)
	}
}

func TestAppDefaultsOllamaNumCtxWhenUnset(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, ".humble-ai-cli", "sessions")
	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "ollama-model", Provider: "ollama", BaseURL: "http://localhost:11434", Active: true},
			},
		},
	}
	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkToken, Content: "Done"},
		},
	}
	factory := newStubFactory()
	factory.Register("ollama-model", provider)

	input := strings.NewReader("num ctx?\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: sessionDir,
		HomeDir:        home,
		Clock:          fixedClock(time.Now()),
		MCP:            &stubMCP{},
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected single request, got %d", len(requests))
	}

	options := requests[0].Options
	if options == nil {
		t.Fatalf("expected request options to include default num_ctx")
	}
	got, ok := options["num_ctx"]
	if !ok {
		t.Fatalf("expected default num_ctx option to be present, got %#v", options)
	}
	if got != 30000 {
		t.Fatalf("unexpected default num_ctx value: %v", got)
	}
}

func TestAppDefaultsOllamaNumCtxWhenZero(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, ".humble-ai-cli", "sessions")
	store := &stubStore{
		cfg: config.Config{
			OllamaNumCtx: 0,
			Models: []config.Model{
				{Name: "ollama-model", Provider: "ollama", BaseURL: "http://localhost:11434", Active: true},
			},
		},
	}
	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkToken, Content: "Done"},
		},
	}
	factory := newStubFactory()
	factory.Register("ollama-model", provider)

	input := strings.NewReader("num ctx?\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: sessionDir,
		HomeDir:        home,
		Clock:          fixedClock(time.Now()),
		MCP:            &stubMCP{},
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected single request, got %d", len(requests))
	}

	options := requests[0].Options
	if options == nil {
		t.Fatalf("expected request options to include default num_ctx")
	}
	got, ok := options["num_ctx"]
	if !ok {
		t.Fatalf("expected default num_ctx option to be present, got %#v", options)
	}
	if got != 30000 {
		t.Fatalf("unexpected default num_ctx value: %v", got)
	}
}

func TestAppDoesNotChunkUserContextWhenLimitDisabled(t *testing.T) {
	t.Parallel()

	const chunkLimit = 1500

	encoder, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		t.Fatalf("failed to load tokenizer: %v", err)
	}

	longUserInput := strings.Repeat("Chunk context verification requires BPE based splitting. ", 2800)
	expectedUserInput := strings.TrimSpace(longUserInput)
	if tokenCount := len(encoder.Encode(expectedUserInput, nil, nil)); tokenCount <= chunkLimit {
		t.Fatalf("test input does not exceed chunk limit, got %d tokens", tokenCount)
	}

	testCases := []struct {
		name           string
		setChunkSize   bool
		contextChunkSz int
	}{
		{name: "limitUnset"},
		{name: "limitExplicitZero", setChunkSize: true, contextChunkSz: 0},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			sessionDir := filepath.Join(home, ".humble-ai-cli", "sessions")
			cfg := config.Config{
				Models: []config.Model{
					{Name: "chunk-model", Provider: "ollama", Active: true},
				},
			}
			if tc.setChunkSize {
				cfg.OllamaContextChunkSize = tc.contextChunkSz
			}
			store := &stubStore{cfg: cfg}
			provider := &recordingProvider{
				chunks: []llm.StreamChunk{
					{Type: llm.ChunkToken, Content: "Done"},
				},
			}
			factory := newStubFactory()
			factory.Register("chunk-model", provider)

			input := strings.NewReader(longUserInput + "\n/exit\n")
			var output bytes.Buffer

			opts := app.Options{
				Store:          store,
				Factory:        factory,
				Input:          input,
				Output:         &output,
				ErrorOutput:    &output,
				HistoryRootDir: sessionDir,
				HomeDir:        home,
				Clock:          fixedClock(time.Now()),
			}

			a, err := app.New(opts)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			if err := a.Run(context.Background()); err != nil {
				t.Fatalf("Run() error = %v", err)
			}

			requests := provider.Requests()
			if len(requests) != 1 {
				t.Fatalf("expected a single request, got %d", len(requests))
			}

			var userMessages []llm.Message
			for _, msg := range requests[0].Messages {
				if msg.Role == "user" {
					userMessages = append(userMessages, msg)
				}
			}

			if len(userMessages) != 1 {
				t.Fatalf("expected a single user message when chunking is disabled, got %d", len(userMessages))
			}

			userMsg := userMessages[0]
			if tokens := len(encoder.Encode(userMsg.Content, nil, nil)); tokens <= chunkLimit {
				t.Fatalf("test input should remain over the default limit, got %d tokens", tokens)
			}

			if userMsg.Content != expectedUserInput {
				idx := firstDifference(userMsg.Content, expectedUserInput)
				t.Fatalf("user content mismatch at %d (gotLen=%d wantLen=%d)", idx, len(userMsg.Content), len(expectedUserInput))
			}
		})
	}
}

func TestAppChunksOverlongUserContextWithCustomLimit(t *testing.T) {
	t.Parallel()

	const customLimit = 512

	encoder, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		t.Fatalf("failed to load tokenizer: %v", err)
	}

	longUserInput := strings.Repeat("Chunk context verification requires BPE based splitting. ", 2800)
	expectedUserInput := strings.TrimSpace(longUserInput)
	if tokenCount := len(encoder.Encode(expectedUserInput, nil, nil)); tokenCount <= customLimit {
		t.Fatalf("test input does not exceed custom chunk limit, got %d tokens", tokenCount)
	}

	home := t.TempDir()
	sessionDir := filepath.Join(home, ".humble-ai-cli", "sessions")
	store := &stubStore{
		cfg: config.Config{
			OllamaContextChunkSize: customLimit,
			Models: []config.Model{
				{Name: "chunk-model", Provider: "ollama", Active: true},
			},
		},
	}
	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkToken, Content: "Done"},
		},
	}
	factory := newStubFactory()
	factory.Register("chunk-model", provider)

	input := strings.NewReader(longUserInput + "\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: sessionDir,
		HomeDir:        home,
		Clock:          fixedClock(time.Now()),
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected a single request, got %d", len(requests))
	}

	req := requests[0]
	if len(req.Messages) < 3 {
		t.Fatalf("expected assistant prompt plus chunked user messages, got %d messages", len(req.Messages))
	}

	var reconstructed strings.Builder
	userChunks := 0
	for _, msg := range req.Messages {
		if msg.Role != "user" {
			continue
		}
		userChunks++
		reconstructed.WriteString(msg.Content)
		if tokenCount := len(encoder.Encode(msg.Content, nil, nil)); tokenCount > customLimit {
			t.Fatalf("chunk exceeds custom limit: %d tokens", tokenCount)
		}
	}

	if userChunks < 2 {
		t.Fatalf("expected multiple user chunks, got %d", userChunks)
	}

	if reconstructed.String() != expectedUserInput {
		idx := firstDifference(reconstructed.String(), expectedUserInput)
		t.Fatalf("reconstructed content mismatch at %d (gotLen=%d wantLen=%d)", idx, len(reconstructed.String()), len(expectedUserInput))
	}
}

func TestAppDoesNotChunkUserContextWithCustomLimitForOpenAI(t *testing.T) {
	t.Parallel()

	const customLimit = 512

	encoder, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		t.Fatalf("failed to load tokenizer: %v", err)
	}

	longUserInput := strings.Repeat("Chunk context verification requires BPE based splitting. ", 2800)
	expectedUserInput := strings.TrimSpace(longUserInput)
	if tokenCount := len(encoder.Encode(expectedUserInput, nil, nil)); tokenCount <= customLimit {
		t.Fatalf("test input does not exceed custom chunk limit, got %d tokens", tokenCount)
	}

	home := t.TempDir()
	sessionDir := filepath.Join(home, ".humble-ai-cli", "sessions")
	store := &stubStore{
		cfg: config.Config{
			OllamaContextChunkSize: customLimit,
			Models: []config.Model{
				{Name: "chunk-model", Provider: "openai", APIKey: "sk-xxx", Active: true},
			},
		},
	}
	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkToken, Content: "Done"},
		},
	}
	factory := newStubFactory()
	factory.Register("chunk-model", provider)

	input := strings.NewReader(longUserInput + "\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: sessionDir,
		HomeDir:        home,
		Clock:          fixedClock(time.Now()),
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("expected a single request, got %d", len(requests))
	}

	var userMessages []llm.Message
	for _, msg := range requests[0].Messages {
		if msg.Role == "user" {
			userMessages = append(userMessages, msg)
		}
	}

	if len(userMessages) != 1 {
		t.Fatalf("expected openai user content to be unchunked, got %d messages", len(userMessages))
	}

	userMsg := userMessages[0]
	if tokens := len(encoder.Encode(userMsg.Content, nil, nil)); tokens <= customLimit {
		t.Fatalf("test input should remain above chunk limit, got %d tokens", tokens)
	}

	if userMsg.Content != expectedUserInput {
		idx := firstDifference(userMsg.Content, expectedUserInput)
		t.Fatalf("user content mismatch at %d (gotLen=%d wantLen=%d)", idx, len(userMsg.Content), len(expectedUserInput))
	}
}

func TestAppContextRetentionTurns(t *testing.T) {
	t.Run("defaultsToLastThreeTurns", func(t *testing.T) {
		requests := runContextRetentionRequests(t, nil, []string{"one", "two", "three", "four", "five"})
		if len(requests) != 5 {
			t.Fatalf("expected 5 requests for 5 prompts, got %d", len(requests))
		}
		req := requests[4]
		if len(req.Messages) != 7 {
			t.Fatalf("expected 7 messages (3 turns + new user), got %d", len(req.Messages))
		}
		if req.Messages[0].Content != "two" {
			t.Fatalf("expected first retained message to be 'two', got %q", req.Messages[0].Content)
		}
		if containsMessageContent(req.Messages, "one") {
			t.Fatalf("expected default retention to drop the oldest turn")
		}
		if last := req.Messages[len(req.Messages)-1]; last.Role != "user" || last.Content != "five" {
			t.Fatalf("expected last message to be the current user prompt, got %#v", last)
		}
	})

	t.Run("limitsToConfiguredTurns", func(t *testing.T) {
		requests := runContextRetentionRequests(t, intPtr(2), []string{"one", "two", "three", "four"})
		if len(requests) != 4 {
			t.Fatalf("expected 4 requests, got %d", len(requests))
		}
		req := requests[3]
		if len(req.Messages) != 5 {
			t.Fatalf("expected 5 messages (2 turns + new user), got %d", len(req.Messages))
		}
		if req.Messages[0].Content != "two" {
			t.Fatalf("expected retained context to start at the second prompt, got %q", req.Messages[0].Content)
		}
		if containsMessageContent(req.Messages, "one") {
			t.Fatalf("expected configured retention to drop the oldest turn")
		}
	})

	t.Run("zeroDisablesHistory", func(t *testing.T) {
		requests := runContextRetentionRequests(t, intPtr(0), []string{"one", "two"})
		if len(requests) != 2 {
			t.Fatalf("expected 2 requests, got %d", len(requests))
		}
		req := requests[1]
		if len(req.Messages) != 1 {
			t.Fatalf("expected only the current user prompt when retention is disabled, got %d messages", len(req.Messages))
		}
		if msg := req.Messages[0]; msg.Role != "user" || msg.Content != "two" {
			t.Fatalf("expected request to only include the second prompt, got %#v", msg)
		}
	})

	t.Run("negativeKeepsFullHistory", func(t *testing.T) {
		requests := runContextRetentionRequests(t, intPtr(-1), []string{"one", "two", "three"})
		if len(requests) != 3 {
			t.Fatalf("expected 3 requests, got %d", len(requests))
		}
		req := requests[2]
		if len(req.Messages) != 5 {
			t.Fatalf("expected entire history (2 turns) plus new user, got %d messages", len(req.Messages))
		}
		if req.Messages[0].Content != "one" {
			t.Fatalf("expected retained context to keep the earliest prompt when retention is negative, got %q", req.Messages[0].Content)
		}
	})
}

func TestAppContextRetentionHandlesChunkedAssistantTurns(t *testing.T) {
	retention := 1
	const legacyMarker = "legacy context payload"
	const recentMarker = "recent turn content"

	store := &stubStore{
		cfg: config.Config{
			OllamaContextChunkSize: 5,
			ContextRetentionTurns:  &retention,
			Models: []config.Model{
				{Name: "chunk-model", Provider: "ollama", Active: true},
			},
		},
	}
	provider := newSequencedRecordingProvider(
		legacyMarker,
		strings.Repeat(recentMarker+" ", 200),
		"final turn",
	)
	factory := newStubFactory()
	factory.Register("chunk-model", provider)

	input := strings.NewReader("first question\nsecond question\nthird question\n/exit\n")
	home := t.TempDir()
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		Clock:          fixedClock(time.Now()),
		MCP:            &stubMCP{},
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(requests))
	}
	finalReq := requests[2]
	if len(finalReq.Messages) < 2 {
		t.Fatalf("expected prior context plus new user in final request, got %d messages", len(finalReq.Messages))
	}
	last := finalReq.Messages[len(finalReq.Messages)-1]
	if last.Role != "user" || last.Content != "third question" {
		t.Fatalf("expected final message to be the third prompt, got %#v", last)
	}

	assistantSegments := 0
	for _, msg := range finalReq.Messages[:len(finalReq.Messages)-1] {
		if strings.Contains(msg.Content, legacyMarker) {
			t.Fatalf("expected retention=1 to drop legacy turn, but found content %q", msg.Content)
		}
		if msg.Role == "assistant" {
			assistantSegments++
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			if !strings.Contains(msg.Content, recentMarker) {
				t.Fatalf("expected assistant chunk to belong to recent turn, got %q", msg.Content)
			}
		}
	}
	if assistantSegments < 2 {
		t.Fatalf("expected assistant response to be chunked into multiple segments, got %d chunks", assistantSegments)
	}
}

func TestAppNewCommandStartsFreshSession(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, ".humble-ai-cli", "sessions")
	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk-xxx", Active: true},
			},
		},
	}
	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkThinking},
			{Type: llm.ChunkToken, Content: "Hi"},
			{Type: llm.ChunkToken, Content: "!"},
		},
	}
	factory := newStubFactory()
	factory.Register("stub-model", provider)

	input := strings.NewReader("First message\n/new\nSecond message\n/exit\n")
	var output bytes.Buffer

	now := time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: sessionDir,
		HomeDir:        home,
		Clock:          fixedClock(now),
	}

	a, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if strings.Contains(output.String(), "Unknown command: /new") {
		t.Fatalf("expected /new to be recognised, got output:\n%s", output.String())
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 streamed requests, got %d", len(requests))
	}
	for i, req := range requests {
		if len(req.Messages) != 1 {
			t.Fatalf("expected request %d to contain 1 message, got %d", i, len(req.Messages))
		}
		if strings.Contains(req.SystemPrompt, "# Connected Tools") {
			t.Fatalf("did not expect connected tools section for openai request %d, got %q", i, req.SystemPrompt)
		}
		if strings.Contains(req.SystemPrompt, "# Function Call Schema and Example") {
			t.Fatalf("did not expect function call schema guidance in request %d, got %q", i, req.SystemPrompt)
		}
		if !strings.Contains(req.SystemPrompt, "NO FUNCTION CONNECTED") {
			t.Fatalf("expected fallback notice in request %d, got %q", i, req.SystemPrompt)
		}
		if req.Messages[0].Role != "user" {
			t.Fatalf("expected user prompt as first message in request %d, got %#v", i, req.Messages[0])
		}
	}

	historyFiles, err := filepath.Glob(filepath.Join(sessionDir, "*.json"))
	if err != nil {
		t.Fatalf("failed to glob history: %v", err)
	}
	if len(historyFiles) != 2 {
		t.Fatalf("expected 2 session files, got %d", len(historyFiles))
	}

	found := map[string]bool{}
	for _, path := range historyFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read history %s: %v", filepath.Base(path), err)
		}

		var record struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatalf("failed to decode history %s: %v", filepath.Base(path), err)
		}

		if len(record.Messages) != 2 {
			t.Fatalf("expected 2 messages in %s, got %d", filepath.Base(path), len(record.Messages))
		}

		user := record.Messages[0].Content
		if user != "First message" && user != "Second message" {
			t.Fatalf("unexpected first message %q in %s", user, filepath.Base(path))
		}
		if found[user] {
			t.Fatalf("duplicate history file for %q", user)
		}
		found[user] = true
	}

	if !found["First message"] || !found["Second message"] {
		t.Fatalf("missing expected session files: %#v", found)
	}
}

func TestAppSetModelUpdatesConfig(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".humble-ai-cli")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to prepare config dir: %v", err)
	}
	cfg := config.Config{
		Models: []config.Model{
			{Name: "model-a", Provider: "openai", APIKey: "key-a", Active: true},
			{Name: "model-b", Provider: "ollama", BaseURL: "http://localhost:11434"},
		},
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), raw, 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	store := config.NewFileStore(home)
	factory := newStubFactory()
	factory.Register("model-a", &recordingProvider{})
	factory.Register("model-b", &recordingProvider{})

	input := strings.NewReader("/set-model\n2\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: t.TempDir(),
		HomeDir:        home,
		Clock:          fixedClock(time.Now()),
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_ = instance

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	updated, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	active, ok := updated.ActiveModel()
	if !ok {
		t.Fatalf("expected an active model after selection")
	}
	if active.Name != "model-b" {
		t.Fatalf("expected active model to be model-b, got %s", active.Name)
	}
	if updated.Models[0].Active {
		t.Fatalf("expected original model to be inactive after selection")
	}
}

func TestAppInitializesSystemPromptFromCodeAndCreatesUserRulesFile(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".humble-ai-cli")
	systemPromptPath := filepath.Join(configDir, "system_prompt.txt")

	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk-xxx", Active: true},
			},
		},
	}

	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkThinking},
			{Type: llm.ChunkToken, Content: "Hello"},
		},
	}
	factory := newStubFactory()
	factory.Register("stub-model", provider)

	mcpExecutor := &stubMCP{
		description: app.MCPServer{
			Name:        "calculator",
			Description: "Performs simple calculations",
		},
		servers: []app.MCPServer{
			{Name: "calculator", Description: "Performs simple calculations"},
		},
		toolset: map[string][]app.MCPFunction{
			"calculator": {
				{Name: "add", Description: "Add numbers."},
			},
		},
	}

	input := strings.NewReader("Hi\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		MCP:            mcpExecutor,
		Clock:          fixedClock(time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)),
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := os.Stat(systemPromptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy system prompt file to remain absent, got err=%v", err)
	}

	userRulesPath := filepath.Join(configDir, "user-rules.md")
	data, err := os.ReadFile(userRulesPath)
	if err != nil {
		t.Fatalf("expected user-rules.md to be created, read error: %v", err)
	}
	if len(bytes.TrimSpace(data)) != 0 {
		t.Fatalf("expected empty user-rules.md on first launch, got %q", string(data))
	}

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) == 0 {
		t.Fatalf("expected at least one LLM request to capture system prompt")
	}
	prompt := requests[0].SystemPrompt
	if !strings.Contains(prompt, "You are a **tool-enabled Humble AI Agent** operating with MCP (Model Context Protocol) servers.") {
		t.Fatalf("expected default prompt to mention humble AI agent guidance, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "# 1) Core Behavior Rules") {
		t.Fatalf("expected default prompt to include core behavior rules heading, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "# 2) Function Selection Flow (chooseFunction MUST be used)") {
		t.Fatalf("expected default prompt to describe chooseFunction flow, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Before calling EACH MCP function:") {
		t.Fatalf("expected default prompt to emphasize each MCP function, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "# Choose Function Example") {
		t.Fatalf("expected default prompt to include choose function example, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "# 6) Asking for Missing Information") {
		t.Fatalf("expected default prompt to include missing information heading, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Ask minimal questions required to make the next legitimate function call.") {
		t.Fatalf("expected default prompt to include targeted question reminder, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "# Connected Tools") {
		t.Fatalf("did not expect connected tools section in openai prompt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "# Function Call Schema and Example") {
		t.Fatalf("did not expect function call schema guidance in prompt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "NO FUNCTION CONNECTED") {
		t.Fatalf("did not expect fallback notice when tools exist, got:\n%s", prompt)
	}
}

func TestAppAppendsUserRulesToSystemPrompt(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".humble-ai-cli")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	userRulesPath := filepath.Join(configDir, "user-rules.md")
	customRules := "## Custom Guardrails\n- Always respond politely."
	if err := os.WriteFile(userRulesPath, []byte(customRules), 0o644); err != nil {
		t.Fatalf("failed to seed user-rules.md: %v", err)
	}

	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk-xxx", Active: true},
			},
		},
	}

	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkThinking},
			{Type: llm.ChunkToken, Content: "Hi"},
		},
	}
	factory := newStubFactory()
	factory.Register("stub-model", provider)

	mcpExecutor := &stubMCP{
		description: app.MCPServer{
			Name:        "calculator",
			Description: "Performs simple calculations",
		},
		servers: []app.MCPServer{
			{Name: "calculator", Description: "Performs simple calculations"},
		},
		toolset: map[string][]app.MCPFunction{
			"calculator": {
				{Name: "add", Description: "Add numbers."},
			},
		},
	}

	input := strings.NewReader("Hello\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		MCP:            mcpExecutor,
		Clock:          fixedClock(time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)),
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requests := provider.Requests()
	if len(requests) == 0 {
		t.Fatalf("expected LLM request to inspect system prompt")
	}
	prompt := requests[0].SystemPrompt
	idxRules := strings.Index(prompt, customRules)
	if idxRules == -1 {
		t.Fatalf("expected system prompt to contain user rules, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "# Function Call Schema and Example") {
		t.Fatalf("did not expect schema guidance in final prompt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "NO FUNCTION CONNECTED") {
		t.Fatalf("did not expect fallback notice when tools exist, got:\n%s", prompt)
	}
}

func containsMessageContent(messages []llm.Message, target string) bool {
	for _, msg := range messages {
		if msg.Content == target {
			return true
		}
	}
	return false
}

func firstDifference(a, b string) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return limit
	}
	return -1
}

func TestAppHandlesMCPToolRequests(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".humble-ai-cli")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config directory: %v", err)
	}

	serverConfig := map[string]any{
		"description": "Adds two numbers for you.",
		"enabled":     true,
	}
	payload := map[string]any{
		"mcpServers": map[string]any{
			"calculator": serverConfig,
		},
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal server config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp-servers.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("failed to write server config: %v", err)
	}

	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk-xxx", Active: true},
			},
		},
	}

	resultCh := make(chan llm.ToolResult, 1)

	provider := &toolRequestProvider{
		call: llm.ToolCall{
			Server:      "calculator",
			Method:      "add",
			Description: "Add the provided numbers.",
			Arguments: map[string]any{
				"a": float64(2),
				"b": float64(3),
			},
		},
		after: []llm.StreamChunk{
			{Type: llm.ChunkToken, Content: "Final answer: 5"},
		},
		onResponded: func(res llm.ToolResult) {
			resultCh <- res
		},
	}

	factory := newStubFactory()
	factory.Register("stub-model", provider)

	mcpExec := &stubMCP{
		description: app.MCPServer{
			Name:        "calculator",
			Description: "Adds numbers via MCP.",
		},
		servers: []app.MCPServer{
			{Name: "calculator", Description: "Adds numbers via MCP."},
		},
		toolset: map[string][]app.MCPFunction{
			"calculator": {
				{Name: "add", Description: "Add two numbers."},
			},
		},
		response: llm.ToolResult{
			Content: "5",
		},
	}

	input := strings.NewReader("Please add\nY\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		MCP:            mcpExec,
		Clock:          fixedClock(time.Date(2025, 10, 16, 16, 20, 30, 0, time.UTC)),
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	select {
	case res := <-resultCh:
		if res.Content != "5" {
			t.Fatalf("expected provider to receive tool result '5', got %q", res.Content)
		}
	default:
		t.Fatalf("expected provider to receive tool result")
	}

	calls := mcpExec.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 MCP call, got %d", len(calls))
	}
	call := calls[0]
	if call.Server != "calculator" || call.Method != "add" {
		t.Fatalf("unexpected call: %#v", call)
	}
	if call.Arguments["a"] != float64(2) || call.Arguments["b"] != float64(3) {
		t.Fatalf("unexpected arguments: %#v", call.Arguments)
	}

	got := output.String()
	if !strings.Contains(got, "MCP tool call") {
		t.Fatalf("expected output to announce MCP tool call, got:\n%s", got)
	}
	if !strings.Contains(got, "Server: calculator") {
		t.Fatalf("expected output to include server name, got:\n%s", got)
	}
	if !strings.Contains(got, "Tool: add") {
		t.Fatalf("expected output to include tool name, got:\n%s", got)
	}
	if !strings.Contains(got, "Arguments:") || !strings.Contains(got, "  - a: 2") || !strings.Contains(got, "  - b: 3") {
		t.Fatalf("expected output to list arguments, got:\n%s", got)
	}
	if !strings.Contains(got, "Final answer: 5") {
		t.Fatalf("expected final answer to be printed, got:\n%s", got)
	}
}

func TestAppMCPCommandPrintsEnabledServers(t *testing.T) {
	store := &stubStore{}
	factory := newStubFactory()
	input := strings.NewReader("/mcp\n/exit\n")
	var output bytes.Buffer

	mcpExec := &stubMCP{
		servers: []app.MCPServer{
			{Name: "calculator", Description: "Performs math operations"},
			{Name: "docs", Description: "Finds documentation snippets"},
		},
		toolset: map[string][]app.MCPFunction{
			"calculator": {
				{Name: "add", Description: "Add two numbers."},
				{Name: "subtract", Description: "Subtract second number from first."},
			},
			"docs": {
				{Name: "search", Description: "Search documentation by keyword."},
			},
		},
	}

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: t.TempDir(),
		MCP:            mcpExec,
		Clock:          fixedClock(time.Now()),
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := output.String()
	for _, phrase := range []string{
		"Enabled MCP servers",
		"calculator",
		"  - add: Add two numbers.",
		"  - subtract: Subtract second number from first.",
		"docs",
		"  - search: Search documentation by keyword.",
	} {
		if !strings.Contains(got, phrase) {
			t.Fatalf("expected output to contain %q, got:\n%s", phrase, got)
		}
	}
}

func TestAppToggleMCPCommandUpdatesServerState(t *testing.T) {
	home := t.TempDir()
	writeMCPServersConfig(t, home, map[string]map[string]any{
		"context7": {
			"description": "Context search",
			"enabled":     true,
			"command":     "/usr/bin/env",
			"args":        []any{"echo"},
		},
		"playwright": {
			"description": "Browser automation",
			"enabled":     false,
			"command":     "/usr/bin/env",
			"args":        []any{"playwright"},
		},
	})

	store := &stubStore{}
	factory := newStubFactory()
	input := strings.NewReader("/toggle-mcp\n2\n/toggle-mcp\n0\n/exit\n")
	var output bytes.Buffer

	mcpExec := &stubMCP{
		servers: []app.MCPServer{
			{Name: "context7", Description: "Context search"},
		},
	}

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		MCP:            mcpExec,
		Clock:          fixedClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".humble-ai-cli", "mcp-servers.json"))
	if err != nil {
		t.Fatalf("failed to read MCP server config: %v", err)
	}

	var configFile struct {
		Servers map[string]struct {
			Enabled *bool `json:"enabled,omitempty"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &configFile); err != nil {
		t.Fatalf("failed to parse MCP server config: %v", err)
	}

	entry, ok := configFile.Servers["playwright"]
	if !ok {
		t.Fatalf("expected playwright entry in MCP config")
	}
	if entry.Enabled == nil || !*entry.Enabled {
		t.Fatalf("expected playwright server to be enabled after toggle")
	}

	got := output.String()
	for _, phrase := range []string{
		"MCP servers found in mcp-servers.json:",
		"  1) context7: enabled",
		"  2) playwright: disabled",
		"Server \"playwright\" is now enabled.",
		"  2) playwright: enabled",
		"Toggle cancelled.",
	} {
		if !strings.Contains(got, phrase) {
			t.Fatalf("expected output to contain %q, got:\n%s", phrase, got)
		}
	}
}

func TestAppWritesDebugLogs(t *testing.T) {
	home := t.TempDir()
	logDir := filepath.Join(home, ".humble-ai-cli", "logs")
	if err := os.MkdirAll(filepath.Join(home, ".humble-ai-cli"), 0o755); err != nil {
		t.Fatalf("failed to prepare config dir: %v", err)
	}

	store := &stubStore{
		cfg: config.Config{
			LogLevel: "debug",
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk-xxx", Active: true},
			},
		},
	}

	resultCh := make(chan llm.ToolResult, 1)

	provider := &toolRequestProvider{
		call: llm.ToolCall{
			Server:      "calculator",
			Method:      "add",
			Description: "Add numbers.",
			Arguments: map[string]any{
				"a": float64(1),
				"b": float64(2),
			},
		},
		after: []llm.StreamChunk{
			{Type: llm.ChunkToken, Content: "Done"},
		},
		onResponded: func(res llm.ToolResult) {
			resultCh <- res
		},
	}
	factory := newStubFactory()
	factory.Register("stub-model", provider)

	mcpExec := &stubMCP{
		servers: []app.MCPServer{
			{Name: "calculator", Description: "Simple math"},
		},
		toolset: map[string][]app.MCPFunction{
			"calculator": {
				{Name: "add", Description: "Add two numbers."},
			},
		},
		response: llm.ToolResult{Content: "3"},
	}

	input := strings.NewReader("Hi\nY\n/exit\n")
	var output bytes.Buffer

	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Input:          input,
		Output:         &output,
		ErrorOutput:    &output,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		HomeDir:        home,
		MCP:            mcpExec,
		Clock:          fixedClock(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)),
	}

	instance, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	select {
	case <-resultCh:
	default:
		t.Fatalf("expected tool result to be delivered")
	}

	files, err := filepath.Glob(filepath.Join(logDir, "application-hac-*.log"))
	if err != nil {
		t.Fatalf("glob logs error: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("expected log file to be created in %s", logDir)
	}

	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	logContent := string(data)
	for _, phrase := range []string{
		"LLM request",
		"LLM response",
		"MCP initialization",
		"MCP call start",
		"MCP call success",
	} {
		if !strings.Contains(logContent, phrase) {
			t.Fatalf("expected log to contain %q, got:\n%s", phrase, logContent)
		}
	}
}

type fixedClock time.Time

func (c fixedClock) Now() time.Time {
	return time.Time(c)
}

var _ app.Clock = fixedClock(time.Time{})
