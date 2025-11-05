package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gamzabox.com/humble-ai-cli/internal/config"
)

func TestBuildOllamaRequestIncludesTools(t *testing.T) {
	t.Parallel()

	req := ChatRequest{
		Model:  "llama3.2",
		Stream: true,
		Messages: []Message{
			{Role: "user", Content: "what is the weather in tokyo?"},
		},
		Tools: []ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Get the weather in a given city",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{
							"type":        "string",
							"description": "The city to get the weather for",
						},
					},
					"required": []any{"city"},
				},
			},
		},
	}

	data, err := buildOllamaRequest(req)
	if err != nil {
		t.Fatalf("buildOllamaRequest returned error: %v", err)
	}

	var payload ollamaRequestPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if len(payload.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(payload.Tools))
	}

	tool := payload.Tools[0]
	if tool.Type != "function" {
		t.Fatalf("expected tool type function, got %q", tool.Type)
	}
	if tool.Function.Name != "get_weather" {
		t.Fatalf("expected tool name get_weather, got %q", tool.Function.Name)
	}
	if len(tool.Function.Parameters) == 0 {
		t.Fatalf("expected tool parameters to be present")
	}
}

func TestOllamaProviderStreamWithToolCalls(t *testing.T) {
	t.Parallel()

	var (
		requestCount int
		firstBody    []byte
		secondBody   []byte
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		defer r.Body.Close()

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		requestCount++
		switch requestCount {
		case 1:
			firstBody = body
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Tokyo"}}}]}, "done": false}`+"\n")
			io.WriteString(w, `{"message":{"role":"assistant","content":""}, "done": true}`+"\n")
		case 2:
			secondBody = body
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"message":{"role":"assistant","content":"It is "}, "done": false}`+"\n")
			io.WriteString(w, `{"message":{"role":"assistant","content":"sunny."}, "done": false}`+"\n")
			io.WriteString(w, `{"message":{"role":"assistant","content":""}, "done": true}`+"\n")
		default:
			t.Fatalf("unexpected request count: %d", requestCount)
		}
	}))
	defer server.Close()

	factory := NewFactory(server.Client())
	model := config.Model{
		Name:     "llama3.2",
		Provider: "ollama",
		BaseURL:  server.URL,
	}
	provider, err := factory.Create(model)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	req := ChatRequest{
		Model:  "llama3.2",
		Stream: true,
		Messages: []Message{
			{Role: "user", Content: "Use tools if you can."},
		},
		Tools: []ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Fetch the weather for a given city",
				Server:      "weather",
				Method:      "get_weather",
				Parameters: map[string]any{
					"type": "object",
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := provider.Stream(ctx, req)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	expectChunk := func() StreamChunk {
		t.Helper()
		select {
		case <-ctx.Done():
			t.Fatalf("context done before chunk: %v", ctx.Err())
		case chunk, ok := <-stream:
			if !ok {
				t.Fatalf("stream closed unexpectedly")
			}
			return chunk
		}
		return StreamChunk{}
	}

	if chunk := expectChunk(); chunk.Type != ChunkThinking {
		t.Fatalf("expected thinking chunk, got %v", chunk.Type)
	}

	callChunk := expectChunk()
	if callChunk.Type != ChunkToolCall {
		t.Fatalf("expected tool call chunk, got %v", callChunk.Type)
	}
	if callChunk.ToolCall == nil {
		t.Fatalf("tool call chunk missing payload")
	}
	if callChunk.ToolCall.Server != "weather" {
		t.Fatalf("expected server weather, got %q", callChunk.ToolCall.Server)
	}
	if callChunk.ToolCall.Method != "get_weather" {
		t.Fatalf("expected method get_weather, got %q", callChunk.ToolCall.Method)
	}

	if err := callChunk.ToolCall.Respond(ctx, ToolResult{Content: "It is sunny in Tokyo"}); err != nil {
		t.Fatalf("respond tool call: %v", err)
	}

	var builder strings.Builder
	for {
		chunk := expectChunk()
		switch chunk.Type {
		case ChunkToken:
			builder.WriteString(chunk.Content)
		case ChunkDone:
			goto finished
		case ChunkError:
			t.Fatalf("unexpected error chunk: %v", chunk.Err)
		}
	}

finished:
	if got := builder.String(); got != "It is sunny." {
		t.Fatalf("unexpected assistant output: %q", got)
	}

	if len(firstBody) == 0 {
		t.Fatalf("first request body not captured")
	}
	if !strings.Contains(string(firstBody), `"tools"`) {
		t.Fatalf("first request missing tools: %s", string(firstBody))
	}
	if len(secondBody) == 0 {
		t.Fatalf("second request body not captured")
	}

	var secondPayload ollamaRequestPayload
	if err := json.Unmarshal(secondBody, &secondPayload); err != nil {
		t.Fatalf("second payload unmarshal: %v", err)
	}
	if len(secondPayload.Messages) == 0 {
		t.Fatalf("second payload missing messages")
	}

	var (
		foundToolCall bool
		foundToolRole bool
	)

	for _, msg := range secondPayload.Messages {
		if len(msg.ToolCalls) > 0 {
			foundToolCall = true
			argCity, ok := msg.ToolCalls[0].Function.Arguments["city"]
			if !ok {
				t.Fatalf("expected city argument in tool call: %#v", msg.ToolCalls[0].Function.Arguments)
			}
			if argCity != "Tokyo" {
				t.Fatalf("unexpected city value: %v", argCity)
			}
		}
		if msg.Role == "tool" {
			foundToolRole = true
			if msg.ToolName != "get_weather" {
				t.Fatalf("unexpected tool_name: %s", msg.ToolName)
			}
		}
	}

	if !foundToolCall {
		t.Fatalf("expected assistant message with tool_calls in second request")
	}
	if !foundToolRole {
		t.Fatalf("expected tool role message in second request")
	}
}

func TestOpenAIProviderStreamsThinkingTokens(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		r.Body.Close()
		if !strings.Contains(string(body), `"stream":true`) {
			t.Fatalf("expected streaming request body, got: %s", string(body))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		io.WriteString(w, `data: {"choices":[{"delta":{"reasoning":{"tokens":[{"token":"Analyzing"}]}}}]}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		io.WriteString(w, `data: {"choices":[{"delta":{"reasoning":{"tokens":[{"token":" context"}]}}}]}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"Answer"}}]}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	factory := NewFactory(server.Client())
	model := config.Model{
		Name:     "gpt-4.1",
		Provider: "openai",
		APIKey:   "sk-test",
		BaseURL:  server.URL,
	}

	provider, err := factory.Create(model)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := provider.Stream(ctx, ChatRequest{
		Model:  model.Name,
		Stream: true,
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	expectChunk := func() StreamChunk {
		t.Helper()
		select {
		case <-ctx.Done():
			t.Fatalf("context done: %v", ctx.Err())
		case chunk, ok := <-stream:
			if !ok {
				t.Fatalf("stream closed unexpectedly")
			}
			return chunk
		}
		return StreamChunk{}
	}

	if chunk := expectChunk(); chunk.Type != ChunkThinking || chunk.Content != "" {
		t.Fatalf("expected initial thinking chunk without content, got %#v", chunk)
	}
	if chunk := expectChunk(); chunk.Type != ChunkThinking || chunk.Content != "Analyzing" {
		t.Fatalf("expected first reasoning token, got %#v", chunk)
	}
	if chunk := expectChunk(); chunk.Type != ChunkThinking || chunk.Content != " context" {
		t.Fatalf("expected second reasoning token, got %#v", chunk)
	}
	if chunk := expectChunk(); chunk.Type != ChunkToken || chunk.Content != "Answer" {
		t.Fatalf("expected answer token, got %#v", chunk)
	}
	if chunk := expectChunk(); chunk.Type != ChunkDone {
		t.Fatalf("expected done chunk, got %#v", chunk)
	}
}

func TestOpenAIProviderStreamsReasoningContentVariants(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"delta":{"reasoning":{"content":[{"type":"text","text":"Step 1"}]}}}]}`+"\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{"reasoning":{"output_text":"\nConclusion."}}}]}`+"\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"Final"}}]}`+"\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	factory := NewFactory(server.Client())
	model := config.Model{
		Name:     "gpt-4.1",
		Provider: "openai",
		APIKey:   "sk-test",
		BaseURL:  server.URL,
	}

	provider, err := factory.Create(model)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream, err := provider.Stream(ctx, ChatRequest{Model: model.Name, Stream: true})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	expect := func() StreamChunk {
		select {
		case <-ctx.Done():
			t.Fatalf("context done: %v", ctx.Err())
		case chunk, ok := <-stream:
			if !ok {
				t.Fatalf("stream closed early")
			}
			return chunk
		}
		return StreamChunk{}
	}

	if chunk := expect(); chunk.Type != ChunkThinking || chunk.Content != "" {
		t.Fatalf("expected initial thinking chunk, got %#v", chunk)
	}
	if chunk := expect(); chunk.Type != ChunkThinking || chunk.Content != "Step 1" {
		t.Fatalf("expected first reasoning content, got %#v", chunk)
	}
	if chunk := expect(); chunk.Type != ChunkThinking || chunk.Content != "\nConclusion." {
		t.Fatalf("expected second reasoning content, got %#v", chunk)
	}
	if chunk := expect(); chunk.Type != ChunkToken || chunk.Content != "Final" {
		t.Fatalf("expected final token, got %#v", chunk)
	}
	if chunk := expect(); chunk.Type != ChunkDone {
		t.Fatalf("expected done chunk, got %#v", chunk)
	}
}

func TestOpenAIProviderLogsFollowupRequestsForToolCalls(t *testing.T) {
	t.Parallel()

	var (
		requestCount int
		mu           sync.Mutex
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		mu.Lock()
		requestCount++
		current := requestCount
		mu.Unlock()

		defer r.Body.Close()

		w.Header().Set("Content-Type", "text/event-stream")

		switch current {
		case 1:
			io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"add","arguments":""}}]},"finish_reason":""}]}`+"\n\n")
			io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"add","arguments":"{\"a\":1,\"b\":2}"}}]},"finish_reason":""}]}`+"\n\n")
			io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
			io.WriteString(w, "data: [DONE]\n\n")
		case 2:
			io.WriteString(w, `data: {"choices":[{"delta":{"content":"Result"}}]}`+"\n\n")
			io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
			io.WriteString(w, "data: [DONE]\n\n")
		default:
			t.Fatalf("unexpected request number: %d", current)
		}
	}))
	defer server.Close()

	factory := NewFactory(server.Client())
	model := config.Model{
		Name:     "gpt-tools",
		Provider: "openai",
		APIKey:   "sk-tool",
		BaseURL:  server.URL,
	}

	provider, err := factory.Create(model)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	logger := &recordingLogger{}
	ctx, cancel := context.WithTimeout(WithLogger(context.Background(), logger), 5*time.Second)
	defer cancel()

	stream, err := provider.Stream(ctx, ChatRequest{
		Model:  model.Name,
		Stream: true,
		Messages: []Message{
			{Role: "user", Content: "Add two numbers"},
		},
		Tools: []ToolDefinition{
			{
				Name:        "add",
				Description: "Add numbers",
				Server:      "calculator",
				Method:      "add",
				Parameters: map[string]any{
					"type": "object",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var (
		receivedResult bool
	)

	for chunk := range stream {
		switch chunk.Type {
		case ChunkThinking:
			continue
		case ChunkToolCall:
			if chunk.ToolCall == nil {
				t.Fatalf("missing tool call payload")
			}
			if err := chunk.ToolCall.Respond(ctx, ToolResult{Content: `{"sum":3}`}); err != nil {
				t.Fatalf("respond tool call: %v", err)
			}
		case ChunkToken:
			if chunk.Content == "Result" {
				receivedResult = true
			}
		case ChunkDone:
			goto finished
		case ChunkError:
			t.Fatalf("unexpected error chunk: %v", chunk.Err)
		default:
			t.Fatalf("unexpected chunk type: %v", chunk.Type)
		}
	}

finished:
	if !receivedResult {
		t.Fatalf("expected assistant tokens from follow-up request")
	}

	entries := logger.Entries()
	requestLogs := 0
	responseLogs := 0
	hasToolPayload := false
	for _, entry := range entries {
		if strings.Contains(entry, "LLM request") {
			requestLogs++
			if strings.Contains(entry, `"role":"tool"`) {
				hasToolPayload = true
			}
		}
		if strings.Contains(entry, "LLM response") {
			responseLogs++
		}
	}

	if requestLogs < 2 {
		t.Fatalf("expected at least two LLM request logs, got %d (entries=%v)", requestLogs, entries)
	}
	if responseLogs < 2 {
		t.Fatalf("expected at least two LLM response logs, got %d (entries=%v)", responseLogs, entries)
	}
	if !hasToolPayload {
		t.Fatalf("expected tool role payload in logs, entries=%v", entries)
	}
}

type recordingLogger struct {
	mu      sync.Mutex
	entries []string
}

func (l *recordingLogger) Debugf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
}

func (l *recordingLogger) Entries() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.entries))
	copy(out, l.entries)
	return out
}
