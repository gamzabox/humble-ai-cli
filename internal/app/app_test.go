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

	"gamzabox.com/humble-ai-cli/internal/app"
	"gamzabox.com/humble-ai-cli/internal/config"
	"gamzabox.com/humble-ai-cli/internal/llm"
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

func TestAppPromptsToSetModelWhenActiveModelMissing(t *testing.T) {
	store := &stubStore{
		cfg: config.Config{
			Provider:    "openai",
			ActiveModel: "",
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
		cfg: config.Config{
			ActiveModel: "",
		},
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
	for _, cmd := range []string{"/help", "/set-model", "/exit"} {
		if !strings.Contains(got, cmd) {
			t.Fatalf("expected help output to include %s, got:\n%s", cmd, got)
		}
	}
}

func TestAppStreamsResponseAndWritesHistory(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, ".humble-ai-cli", "sessions")
	store := &stubStore{
		cfg: config.Config{
			Provider:    "openai",
			ActiveModel: "stub-model",
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk-xxx"},
			},
		},
	}
	provider := &recordingProvider{
		chunks: []llm.StreamChunk{
			{Type: llm.ChunkThinking},
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
	for _, phrase := range []string{
		"Waiting for response...",
		"Thinking...",
		"Hello World",
	} {
		if !strings.Contains(got, phrase) {
			t.Fatalf("expected output to contain %q, got:\n%s", phrase, got)
		}
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
			Provider:    "openai",
			ActiveModel: "stub-model",
			Models: []config.Model{
				{Name: "stub-model", Provider: "openai", APIKey: "sk-xxx"},
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
		Provider:    "openai",
		ActiveModel: "model-a",
		Models: []config.Model{
			{Name: "model-a", Provider: "openai", APIKey: "key-a"},
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

	if err := instance.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	updated, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if updated.ActiveModel != "model-b" {
		t.Fatalf("expected active model to be model-b, got %s", updated.ActiveModel)
	}
}

type fixedClock time.Time

func (c fixedClock) Now() time.Time {
	return time.Time(c)
}

var _ app.Clock = fixedClock(time.Time{})
