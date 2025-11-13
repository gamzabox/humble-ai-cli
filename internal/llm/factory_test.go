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

	"github.com/gamzabox/humble-ai-cli/internal/config"
)

func testChooseToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        routeIntentToolName,
		Description: "Choose tool first which you want call .",
		Server:      routeIntentServerName,
		Method:      routeIntentToolName,
		Parameters: map[string]any{
			"$schema":              "http://json-schema.org/draft-07/schema#",
			"additionalProperties": false,
			"properties": map[string]any{
				"toolName": map[string]any{
					"description": "The name of the tool that the agent should route to, based on the user’s intent. This value identifies which tool’s Input Schema should be returned for validation before execution.",
					"type":        "string",
				},
			},
			"required": []any{"toolName"},
			"type":     "object",
		},
	}
}

func TestBuildOllamaRequestEmbedsToolSchemaInSystemPrompt(t *testing.T) {
	t.Parallel()

	req := ChatRequest{
		Model:        "llama3.2",
		Stream:       true,
		SystemPrompt: "Base prompt.",
		Messages: []Message{
			{Role: "user", Content: "what is the weather in tokyo?"},
		},
		Tools: []ToolDefinition{
			testChooseToolDefinition(),
			{
				Name:        "weather__get_weather",
				Description: "Get the weather in a given city",
				Server:      "weather",
				Method:      "get_weather",
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

	if len(payload.Tools) != 0 {
		t.Fatalf("expected tools field to be omitted, got %d entries", len(payload.Tools))
	}

	if len(payload.Messages) == 0 {
		t.Fatalf("expected at least one message")
	}

	systemMsg := payload.Messages[0]
	if systemMsg.Role != "system" {
		t.Fatalf("expected first message to be system, got %s", systemMsg.Role)
	}
	if !strings.Contains(systemMsg.Content, "Base prompt.") {
		t.Fatalf("expected base prompt in system content, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "# Connected Tools") {
		t.Fatalf("expected connected tools heading in system prompt, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "## Internal Tool: route-intent") {
		t.Fatalf("expected route-intent internal tool heading, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "- name: **choose-tool**") {
		t.Fatalf("expected choose-tool entry in system prompt, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "\"toolName\"") {
		t.Fatalf("expected choose-tool input schema in system prompt, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "## MCP Server: weather") {
		t.Fatalf("expected weather server details in system prompt, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "- name: **weather__get_weather**") {
		t.Fatalf("expected tool description bullet in system prompt, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "- description: Get the weather in a given city") {
		t.Fatalf("expected tool description line in system prompt, got %q", systemMsg.Content)
	}
	if strings.Contains(systemMsg.Content, "\"city\":") {
		t.Fatalf("MCP tool input schema should not be embedded, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "TOOL_CALL:") {
		t.Fatalf("expected TOOL_CALL block in system prompt, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, `"name": "tool name"`) {
		t.Fatalf("expected TOOL_CALL schema example in system prompt, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, `"reason": "reason why calling this tool"`) {
		t.Fatalf("expected TOOL_CALL schema to describe reason placeholder, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, `"name": "good-tool"`) {
		t.Fatalf("expected TOOL_CALL example to show resolve-library-id, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, `"reason": "why this tool call is needed"`) {
		t.Fatalf("expected TOOL_CALL example to include reason, got %q", systemMsg.Content)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal payload into map: %v", err)
	}
	options, ok := root["options"].(map[string]any)
	if !ok {
		t.Fatalf("expected options object in payload, got %T", root["options"])
	}
	if got := options["temperature"]; got != 0.1 {
		t.Fatalf("expected temperature 0.1, got %v", got)
	}
}

func TestBuildOllamaRequestWithoutToolsAddsNoToolConnectedMessage(t *testing.T) {
	t.Parallel()

	req := ChatRequest{
		Model:        "llama3.2",
		SystemPrompt: "Base prompt.",
		Stream:       true,
		Messages: []Message{
			{Role: "user", Content: "hello?"},
		},
		Tools: []ToolDefinition{testChooseToolDefinition()},
	}

	data, err := buildOllamaRequest(req)
	if err != nil {
		t.Fatalf("buildOllamaRequest returned error: %v", err)
	}

	var payload ollamaRequestPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if len(payload.Messages) == 0 {
		t.Fatalf("expected at least one message in payload")
	}

	systemMsg := payload.Messages[0]
	if systemMsg.Role != "system" {
		t.Fatalf("expected first message to have system role, got %s", systemMsg.Role)
	}
	if !strings.Contains(systemMsg.Content, "# Connected Tools") {
		t.Fatalf("expected connected tools heading in system prompt, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "## Internal Tool: route-intent") {
		t.Fatalf("expected route-intent section even without MCP servers, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "- name: **choose-tool**") {
		t.Fatalf("expected choose-tool entry even without MCP servers, got %q", systemMsg.Content)
	}
	if !strings.Contains(systemMsg.Content, "**NO TOOL CONNECTED**") {
		t.Fatalf("expected NO TOOL CONNECTED notice in system prompt, got %q", systemMsg.Content)
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
			io.WriteString(w, `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"weather__get_weather","arguments":{"city":"Tokyo"}}}]}, "done": false}`+"\n")
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
				Name:        "weather__get_weather",
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
	if strings.Contains(string(firstBody), `"tools"`) {
		t.Fatalf("first request should not send tools field: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "TOOL_CALL:") {
		t.Fatalf("first request missing TOOL_CALL instructions: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), `\"reason\": \"reason why calling this tool\"`) {
		t.Fatalf("first request missing reason placeholder in TOOL_CALL schema: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), `\"reason\": \"why this tool call is needed\"`) {
		t.Fatalf("first request missing reason example in TOOL_CALL block: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "# Connected Tools") {
		t.Fatalf("first request missing connected tools heading: %s", string(firstBody))
	}
	var firstMap map[string]any
	if err := json.Unmarshal(firstBody, &firstMap); err != nil {
		t.Fatalf("unmarshal first request: %v", err)
	}
	options, ok := firstMap["options"].(map[string]any)
	if !ok {
		t.Fatalf("expected options object in first request, got %T", firstMap["options"])
	}
	if options["temperature"] != 0.1 {
		t.Fatalf("expected first request temperature 0.1, got %v", options["temperature"])
	}
	if len(secondBody) == 0 {
		t.Fatalf("second request body not captured")
	}

	var secondPayload ollamaRequestPayload
	if err := json.Unmarshal(secondBody, &secondPayload); err != nil {
		t.Fatalf("second payload unmarshal: %v", err)
	}
	var secondMap map[string]any
	if err := json.Unmarshal(secondBody, &secondMap); err != nil {
		t.Fatalf("second payload map unmarshal: %v", err)
	}
	secondOptions, ok := secondMap["options"].(map[string]any)
	if !ok {
		t.Fatalf("expected options object in second request, got %T", secondMap["options"])
	}
	if secondOptions["temperature"] != 0.1 {
		t.Fatalf("expected second request temperature 0.1, got %v", secondOptions["temperature"])
	}
	if len(secondPayload.Messages) == 0 {
		t.Fatalf("second payload missing messages")
	}

	var (
		toolCallContent string
		foundToolRole   bool
	)

	for _, msg := range secondPayload.Messages {
		if len(msg.ToolCalls) > 0 {
			t.Fatalf("tool_calls field should be empty in context payload: %+v", msg.ToolCalls)
		}
		if msg.Role == "tool" && msg.ToolName == "weather__get_weather" {
			foundToolRole = true
			continue
		}
		if msg.Role == "assistant" {
			content := strings.TrimSpace(msg.Content)
			if strings.Contains(content, `"name":"weather__get_weather"`) {
				toolCallContent = content
			}
		}
	}

	if toolCallContent == "" {
		t.Fatalf("expected assistant content with tool call JSON in second request")
	}

	var callPayload struct {
		Server    string         `json:"server"`
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(toolCallContent), &callPayload); err != nil {
		t.Fatalf("unmarshal assistant tool call content: %v", err)
	}
	if callPayload.Server != "weather" {
		t.Fatalf("unexpected call server: %s", callPayload.Server)
	}
	if callPayload.Name != "weather__get_weather" {
		t.Fatalf("unexpected call name: %s", callPayload.Name)
	}
	if callPayload.Arguments["city"] != "Tokyo" {
		t.Fatalf("unexpected city argument: %v", callPayload.Arguments["city"])
	}
	if !foundToolRole {
		t.Fatalf("expected tool role message in second request")
	}
}

func TestOllamaProviderHandlesManualFunctionCallJSON(t *testing.T) {
	t.Parallel()

	manualContent := "I will retrieve docs first.\n```json\n{\n\t\"name\": \"context7__resolve-library-id\",\n\t\"arguments\": {\n\t\t\"libraryName\": \"react-select\"\n\t}\n}\n```\nLet me check what I find next."

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
			io.WriteString(w, fmt.Sprintf(`{"message":{"role":"assistant","content":%q}, "done": false}`+"\n", manualContent))
			io.WriteString(w, `{"message":{"role":"assistant","content":""}, "done": true}`+"\n")
		case 2:
			secondBody = body
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"message":{"role":"assistant","content":"Docs summary."}, "done": false}`+"\n")
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
			{Role: "user", Content: "Please use available tools."},
		},
		Tools: []ToolDefinition{
			{
				Name:        "context7__resolve-library-id",
				Description: "Resolve Context7 library IDs",
				Server:      "context7",
				Method:      "resolve-library-id",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"libraryName": map[string]any{"type": "string"},
					},
					"required": []any{"libraryName"},
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

	expect := func() StreamChunk {
		t.Helper()
		select {
		case <-ctx.Done():
			t.Fatalf("context done before chunk: %v", ctx.Err())
		case chunk, ok := <-stream:
			if !ok {
				t.Fatalf("stream closed")
			}
			return chunk
		}
		return StreamChunk{}
	}

	if chunk := expect(); chunk.Type != ChunkThinking {
		t.Fatalf("expected thinking chunk, got %v", chunk.Type)
	}
	if chunk := expect(); chunk.Type != ChunkToken {
		t.Fatalf("expected token chunk before manual tool call, got %v", chunk.Type)
	}

	callChunk := expect()
	if callChunk.Type != ChunkToolCall {
		t.Fatalf("expected tool call chunk, got %v", callChunk.Type)
	}
	if callChunk.ToolCall == nil {
		t.Fatalf("tool call payload missing")
	}
	if callChunk.ToolCall.Server != "context7" {
		t.Fatalf("unexpected server %s", callChunk.ToolCall.Server)
	}
	if callChunk.ToolCall.Method != "resolve-library-id" {
		t.Fatalf("unexpected method %s", callChunk.ToolCall.Method)
	}

	if err := callChunk.ToolCall.Respond(ctx, ToolResult{Content: `{"context7CompatibleLibraryID":"/context/react-select"}`}); err != nil {
		t.Fatalf("respond tool call: %v", err)
	}

	tokenChunk := expect()
	if tokenChunk.Type != ChunkToken || tokenChunk.Content != "Docs summary." {
		t.Fatalf("unexpected assistant token chunk: %#v", tokenChunk)
	}
	if doneChunk := expect(); doneChunk.Type != ChunkDone {
		t.Fatalf("expected done chunk, got %v", doneChunk.Type)
	}

	if len(firstBody) == 0 {
		t.Fatalf("expected first request body")
	}
	if !strings.Contains(string(firstBody), "TOOL_CALL:") {
		t.Fatalf("TOOL_CALL block missing in first request: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), `\"reason\": \"reason why calling this tool\"`) {
		t.Fatalf("TOOL_CALL schema missing reason placeholder in first request: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), `\"reason\": \"why this tool call is needed\"`) {
		t.Fatalf("TOOL_CALL example missing reason entry in first request: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "# Connected Tools") {
		t.Fatalf("connected tools heading missing in first request: %s", string(firstBody))
	}

	var secondPayload ollamaRequestPayload
	if err := json.Unmarshal(secondBody, &secondPayload); err != nil {
		t.Fatalf("unmarshal second payload: %v", err)
	}
	foundTool := false
	for _, msg := range secondPayload.Messages {
		if msg.Role == "tool" && msg.ToolName == "context7__resolve-library-id" {
			foundTool = true
			break
		}
	}
	if !foundTool {
		t.Fatalf("expected tool role message in second pass payload: %+v", secondPayload.Messages)
	}
}

func TestOllamaProviderStreamsThinkingFields(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		defer r.Body.Close()

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"message":{"role":"assistant","content":"","thinking":"Analyzing idea"},"done":false}`+"\n")
		io.WriteString(w, `{"thinking":"Refining plan","done":false}`+"\n")
		io.WriteString(w, `{"message":{"role":"assistant","content":"Answer"},"done":false}`+"\n")
		io.WriteString(w, `{"done":true}`+"\n")
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
	if chunk := expect(); chunk.Type != ChunkThinking || chunk.Content != "Analyzing idea" {
		t.Fatalf("expected message thinking payload, got %#v", chunk)
	}
	if chunk := expect(); chunk.Type != ChunkThinking || chunk.Content != "Refining plan" {
		t.Fatalf("expected top-level thinking payload, got %#v", chunk)
	}
	if chunk := expect(); chunk.Type != ChunkToken || chunk.Content != "Answer" {
		t.Fatalf("expected final answer token, got %#v", chunk)
	}
	if chunk := expect(); chunk.Type != ChunkDone {
		t.Fatalf("expected done chunk, got %#v", chunk)
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
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		if payload["temperature"] != 0.1 {
			t.Fatalf("expected temperature 0.1, got %v", payload["temperature"])
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
	if chunk := expectChunk(); chunk.Type != ChunkThinking || chunk.Content != "context" {
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
		io.WriteString(w, `data: {"choices":[{"delta":{"reasoning_content":"Additional insight"}}]}`+"\n\n")
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
	if chunk := expect(); chunk.Type != ChunkThinking || chunk.Content != "Conclusion." {
		t.Fatalf("expected second reasoning content, got %#v", chunk)
	}
	if chunk := expect(); chunk.Type != ChunkThinking || chunk.Content != "Additional insight" {
		t.Fatalf("expected reasoning_content payload, got %#v", chunk)
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
			if strings.Contains(entry, `"role":"tool"`) && strings.Contains(entry, `"name":"add"`) {
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
