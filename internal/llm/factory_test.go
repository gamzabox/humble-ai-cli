package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
