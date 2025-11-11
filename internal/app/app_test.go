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

func (f *stubFactory) Create(model config.Model) (llm.ChatProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.providers[model.Name]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return p, nil
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
	if !strings.Contains(got, "MCP call completed.") {
		t.Fatalf("expected call completion message, got:\n%s", got)
	}
	if !strings.Contains(got, "Final answer: 5") {
		t.Fatalf("expected final answer output, got:\n%s", got)
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

func TestAppCreatesDefaultSystemPrompt(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".humble-ai-cli")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	systemPromptPath := filepath.Join(configDir, "system_prompt.txt")
	if _, err := os.Stat(systemPromptPath); err == nil {
		t.Fatalf("expected system prompt to be absent before test")
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
	_ = instance

	if _, err := os.Stat(systemPromptPath); err != nil {
		t.Fatalf("expected system prompt to be created, got error: %v", err)
	}

	data, err := os.ReadFile(systemPromptPath)
	if err != nil {
		t.Fatalf("failed to read system prompt: %v", err)
	}

	content := string(bytes.TrimSpace(data))
	if !strings.Contains(content, "MCP server tooling") {
		t.Fatalf("expected default prompt to mention MCP tooling, got:\n%s", content)
	}
	// Running once should not overwrite existing content.
	custom := []byte("custom prompt")
	if err := os.WriteFile(systemPromptPath, custom, 0o644); err != nil {
		t.Fatalf("failed to write custom prompt: %v", err)
	}

	inst2, err := app.New(opts)
	if err != nil {
		t.Fatalf("New() second time error = %v", err)
	}
	_ = inst2

	after, err := os.ReadFile(systemPromptPath)
	if err != nil {
		t.Fatalf("failed to read prompt after second init: %v", err)
	}
	if !bytes.Equal(after, custom) {
		t.Fatalf("expected prompt to remain unchanged on subsequent init")
	}
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
	if !strings.Contains(got, "Arguments:") || !strings.Contains(got, "  a: 2") || !strings.Contains(got, "  b: 3") {
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
