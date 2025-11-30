package app_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gamzabox/humble-ai-cli/internal/app"
	"github.com/gamzabox/humble-ai-cli/internal/config"
	"github.com/gamzabox/humble-ai-cli/internal/llm"
	"github.com/gamzabox/humble-ai-cli/internal/workflow"
)

func TestExecuteWorkflowRunsStepsWithWorkflowConfig(t *testing.T) {
	home := t.TempDir()
	wf := workflow.Definition{
		BasicConfig: &config.Config{
			ToolCallMode: "auto",
			Models: []config.Model{
				{Name: "wf-model", Provider: "openai", APIKey: "sk-wf", Active: true},
			},
		},
		Steps: []workflow.Step{
			{Title: "first", Prompt: "First prompt"},
			{Title: "second", Prompt: "Second prompt"},
		},
	}

	provider := newSequencedRecordingProvider("Answer one", "Answer two")
	factory := newStubFactory()
	factory.Register("wf-model", provider)

	var output bytes.Buffer
	opts := app.Options{
		Factory:        factory,
		Output:         &output,
		ErrorOutput:    &output,
		Input:          strings.NewReader(""),
		HomeDir:        home,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		Clock:          fixedClock(time.Now()),
	}

	if err := app.ExecuteWorkflow(context.Background(), opts, wf); err != nil {
		t.Fatalf("ExecuteWorkflow() error = %v", err)
	}

	got := output.String()
	for _, phrase := range []string{
		"Answer one",
		"Answer two",
	} {
		if !strings.Contains(got, phrase) {
			t.Fatalf("expected output to contain %q, got:\n%s", phrase, got)
		}
	}
	for _, unexpected := range []string{
		"Waiting for response",
		"Humble AI CLI version",
		"<<< Thinking >>>",
		"> MCP tool call",
	} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("did not expect %q in workflow output, got:\n%s", unexpected, got)
		}
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("expected two LLM requests, got %d", len(requests))
	}
	if !containsMessageContent(requests[1].Messages, "First prompt") {
		t.Fatalf("expected second request to include prior user context, got %#v", requests[1].Messages)
	}
	if requests[1].Messages[len(requests[1].Messages)-1].Content != "Second prompt" {
		t.Fatalf("expected second prompt as last message, got %#v", requests[1].Messages[len(requests[1].Messages)-1])
	}
}

func TestExecuteWorkflowManualModeShowsToolPrompt(t *testing.T) {
	home := t.TempDir()
	wf := workflow.Definition{
		BasicConfig: &config.Config{
			ToolCallMode: "manual",
			Models: []config.Model{
				{Name: "wf-model", Provider: "openai", APIKey: "sk-wf", Active: true},
			},
		},
		Steps: []workflow.Step{
			{Title: "call", Prompt: "Call the tool"},
		},
	}

	resultCh := make(chan llm.ToolResult, 1)
	provider := &toolRequestProvider{
		call: llm.ToolCall{
			Server: "calc",
			Method: "add",
			Arguments: map[string]any{
				"a": float64(1),
				"b": float64(2),
			},
		},
		after: []llm.StreamChunk{
			{Type: llm.ChunkToken, Content: "Sum is 3"},
		},
		onResponded: func(res llm.ToolResult) {
			resultCh <- res
		},
	}
	factory := newStubFactory()
	factory.Register("wf-model", provider)

	mcpExec := &stubMCP{
		servers: []app.MCPServer{
			{Name: "calc", Description: "calculator"},
		},
		toolset: map[string][]app.MCPFunction{
			"calc": {
				{Name: "add", Description: "add numbers"},
			},
		},
		response: llm.ToolResult{Content: "3"},
	}

	var output bytes.Buffer
	opts := app.Options{
		Factory:        factory,
		Output:         &output,
		ErrorOutput:    &output,
		Input:          strings.NewReader("Y\n"),
		HomeDir:        home,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		MCP:            mcpExec,
		Clock:          fixedClock(time.Now()),
	}

	if err := app.ExecuteWorkflow(context.Background(), opts, wf); err != nil {
		t.Fatalf("ExecuteWorkflow() error = %v", err)
	}

	select {
	case res := <-resultCh:
		if res.Content != "3" {
			t.Fatalf("expected tool result '3', got %q", res.Content)
		}
	default:
		t.Fatalf("expected tool result to be delivered")
	}

	got := output.String()
	if !strings.Contains(got, "> MCP tool call") {
		t.Fatalf("expected MCP call summary in manual mode, got:\n%s", got)
	}
	if !strings.Contains(got, "Call now?") {
		t.Fatalf("expected confirmation prompt, got:\n%s", got)
	}
	if !strings.Contains(got, "Sum is 3") {
		t.Fatalf("expected final answer output, got:\n%s", got)
	}
	if strings.Contains(got, "Waiting for response") {
		t.Fatalf("did not expect waiting message in workflow output, got:\n%s", got)
	}
}

func TestExecuteWorkflowFallsBackToStoredConfigWhenWorkflowConfigMissing(t *testing.T) {
	home := t.TempDir()
	wf := workflow.Definition{
		Steps: []workflow.Step{
			{Title: "hello", Prompt: "Hello"},
		},
	}

	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "stored-model", Provider: "openai", APIKey: "sk-store", Active: true},
			},
		},
	}
	provider := &recordingProvider{
		chunks: []llm.StreamChunk{{Type: llm.ChunkToken, Content: "Stored config used"}},
	}
	factory := newStubFactory()
	factory.Register("stored-model", provider)

	var output bytes.Buffer
	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Output:         &output,
		ErrorOutput:    &output,
		Input:          strings.NewReader(""),
		HomeDir:        home,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		Clock:          fixedClock(time.Now()),
	}

	if err := app.ExecuteWorkflow(context.Background(), opts, wf); err != nil {
		t.Fatalf("ExecuteWorkflow() error = %v", err)
	}

	if !strings.Contains(output.String(), "Stored config used") {
		t.Fatalf("expected output to use stored config provider, got:\n%s", output.String())
	}
}

func TestExecuteWorkflowFailsWhenBasicConfigMissingRequiredFields(t *testing.T) {
	home := t.TempDir()
	wf := workflow.Definition{
		BasicConfig: &config.Config{
			Models: []config.Model{
				{Name: "wf-model", Provider: "openai"},
			},
		},
		Steps: []workflow.Step{
			{Title: "only", Prompt: "Hello"},
		},
	}

	// Store includes a valid model but should not be used to patch workflow config.
	store := &stubStore{
		cfg: config.Config{
			Models: []config.Model{
				{Name: "stored-model", Provider: "openai", APIKey: "sk-store", Active: true},
			},
		},
	}
	factory := newStubFactory()
	factory.Register("stored-model", &recordingProvider{})

	var output bytes.Buffer
	opts := app.Options{
		Store:          store,
		Factory:        factory,
		Output:         &output,
		ErrorOutput:    &output,
		Input:          strings.NewReader(""),
		HomeDir:        home,
		HistoryRootDir: filepath.Join(home, ".humble-ai-cli", "sessions"),
		Clock:          fixedClock(time.Now()),
	}

	if err := app.ExecuteWorkflow(context.Background(), opts, wf); err == nil {
		t.Fatalf("expected ExecuteWorkflow to error due to missing required fields")
	}

	got := output.String()
	if !strings.Contains(got, "workflow") || !strings.Contains(got, "model") {
		t.Fatalf("expected workflow config error message, got:\n%s", got)
	}
}
