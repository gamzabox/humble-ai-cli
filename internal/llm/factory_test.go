package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamzabox/humble-ai-cli/internal/config"
	"github.com/gamzabox/humble-ai-cli/internal/tokenizer"
)

func testChooseFunctionDefinition() ToolDefinition {
	return ToolDefinition{
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

func testBasePrompt() string {
	return "Base prompt."
}

func testToolPrompt(server, toolName, description string) string {
	return fmt.Sprintf("# Connected Tools\n\n## MCP Server: %s\n\n- function name: **%s**\n- description: %s\n\n%s", server, toolName, description, testFunctionCallSchemaBlock)
}

func testNoToolPrompt() string {
	return "# Connected Tools\n\n**NO FUNCTION CONNECTED**\n" + testFunctionCallSchemaBlock
}

const testFunctionCallSchemaBlock = "\n# Function Call Schema and Example\n## Schema\n{\n  \"functionCall\": {\n    \"server\": \"context7\",\n    \"name\": \"context7__resolve-library-id\",\n    \"arguments\": {\n      \"libraryName\": \"golang mcp sdk\"\n    },\n    \"reason\": \"To retrieve the correct Context7-compatible library ID for the Go language MCP SDK, which is required to fetch its documentation.\"\n  }\n}\n\n## Example\n{\n  \"functionCall\": {\n    \"server\": \"context7\",\n    \"name\": \"context7__resolve-library-id\",\n    \"arguments\": {\n      \"libraryName\": \"golang mcp sdk\"\n    },\n    \"reason\": \"To retrieve the correct Context7-compatible library ID for the Go language MCP SDK, which is required to fetch its documentation.\"\n  }\n}\n"

func TestBuildOllamaRequestPreservesAssistantToolPrompt(t *testing.T) {
	t.Parallel()

	toolPrompt := testToolPrompt("weather", "weather__get_weather", "Get the weather in a given city")
	req := ChatRequest{
		Model:        "llama3.2",
		Stream:       true,
		SystemPrompt: testBasePrompt(),
		Messages: []Message{
			{Role: "assistant", Content: toolPrompt},
			{Role: "user", Content: "what is the weather in tokyo?"},
		},
		Tools: []ToolDefinition{
			testChooseFunctionDefinition(),
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

	if len(payload.Messages) != len(req.Messages)+1 {
		t.Fatalf("expected %d messages, got %d", len(req.Messages)+1, len(payload.Messages))
	}

	systemMsg := payload.Messages[0]
	if systemMsg.Role != "system" {
		t.Fatalf("expected first message to be system, got %s", systemMsg.Role)
	}
	if !strings.Contains(systemMsg.Content, "Base prompt.") {
		t.Fatalf("expected base prompt in system content, got %q", systemMsg.Content)
	}
	if strings.Contains(systemMsg.Content, "# Connected Tools") {
		t.Fatalf("system prompt should not embed tool prompt, got %q", systemMsg.Content)
	}

	assistantMsg := payload.Messages[1]
	if assistantMsg.Role != "assistant" {
		t.Fatalf("expected second message to be assistant, got %s", assistantMsg.Role)
	}
	if assistantMsg.Content != toolPrompt {
		t.Fatalf("assistant prompt mismatch:\nwant: %q\ngot:  %q", toolPrompt, assistantMsg.Content)
	}

	userMsg := payload.Messages[2]
	if userMsg.Role != "user" || userMsg.Content != "what is the weather in tokyo?" {
		t.Fatalf("expected user message preserved, got %#v", userMsg)
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

func TestBuildOllamaRequestPreservesNoToolConnectedPrompt(t *testing.T) {
	t.Parallel()

	toolPrompt := testNoToolPrompt()
	req := ChatRequest{
		Model:        "llama3.2",
		SystemPrompt: testBasePrompt(),
		Stream:       true,
		Messages: []Message{
			{Role: "assistant", Content: toolPrompt},
			{Role: "user", Content: "hello?"},
		},
		Tools: []ToolDefinition{testChooseFunctionDefinition()},
	}

	data, err := buildOllamaRequest(req)
	if err != nil {
		t.Fatalf("buildOllamaRequest returned error: %v", err)
	}

	var payload ollamaRequestPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if len(payload.Messages) != len(req.Messages)+1 {
		t.Fatalf("expected %d messages, got %d", len(req.Messages)+1, len(payload.Messages))
	}

	assistantMsg := payload.Messages[1]
	if assistantMsg.Content != toolPrompt {
		t.Fatalf("expected assistant prompt to remain unchanged, got %q", assistantMsg.Content)
	}
}

func TestParseManualToolCallHandlesChooseFunction(t *testing.T) {
	payload := "Let's pick a tool.\n```\n{\n  \"chooseFunction\": {\n    \"functionName\": \"context7__resolve-library-id\",\n    \"reason\": \"Need to resolve libraries\"\n  }\n}\n```"
	calls, cleaned := parseManualToolCall(payload)
	if len(calls) != 1 {
		t.Fatalf("expected one parsed call, got %d", len(calls))
	}
	if strings.Contains(cleaned, "chooseFunction") {
		t.Fatalf("expected chooseFunction block removed, got %q", cleaned)
	}
	call := calls[0].call
	if call.Function.Name != routeIntentToolName {
		t.Fatalf("unexpected function name %q", call.Function.Name)
	}
	args, err := toolCallRequest{Call: call}.arguments()
	if err != nil {
		t.Fatalf("arguments error: %v", err)
	}
	if got := args["functionName"]; got != "context7__resolve-library-id" {
		t.Fatalf("expected functionName argument, got %v", got)
	}
	if got := args["reason"]; got != "Need to resolve libraries" {
		t.Fatalf("expected reason argument, got %v", got)
	}
}

func TestParseManualToolCallHandlesFunctionCallObject(t *testing.T) {
	payload := `Manual call request:
{
  "functionCall": {
    "server": "context7",
    "name": "context7__resolve-library-id",
    "arguments": {
      "libraryName": "create-react-app"
    },
    "reason": "To resolve the library ID"
  }
}`
	calls, cleaned := parseManualToolCall(payload)
	if len(calls) != 1 {
		t.Fatalf("expected one parsed call, got %d", len(calls))
	}
	if strings.Contains(cleaned, "functionCall") {
		t.Fatalf("expected functionCall block removed, got %q", cleaned)
	}
	call := calls[0].call
	if call.Function.Name != "context7__resolve-library-id" {
		t.Fatalf("unexpected function name %q", call.Function.Name)
	}
	args, err := toolCallRequest{Call: call}.arguments()
	if err != nil {
		t.Fatalf("arguments error: %v", err)
	}
	if got := args["libraryName"]; got != "create-react-app" {
		t.Fatalf("expected libraryName argument, got %v", got)
	}
}

func TestFormatOllamaToolCallContentForChooseFunction(t *testing.T) {
	call := parsedToolCall{
		call: openAIToolCall{
			Function: openAIToolFunction{
				Name:      routeIntentToolName,
				Arguments: `{"functionName":"context7__resolve-library-id","reason":"Need schema"}`,
			},
		},
	}
	got := formatOllamaToolCallContent([]parsedToolCall{call}, nil)
	want := `{"chooseFunction":{"functionName":"context7__resolve-library-id","reason":"Need schema"}}`
	if got != want {
		t.Fatalf("unexpected formatted content:\nwant: %s\n got: %s", want, got)
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

	toolPrompt := testToolPrompt("weather", "weather__get_weather", "Fetch the weather for a given city")
	req := ChatRequest{
		Model:        "llama3.2",
		SystemPrompt: testBasePrompt(),
		Stream:       true,
		Messages: []Message{
			{Role: "assistant", Content: toolPrompt},
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
	if !strings.Contains(string(firstBody), "# Function Call Schema and Example") {
		t.Fatalf("first request missing function call schema guidance: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "functionCall") {
		t.Fatalf("first request missing functionCall mention: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "context7__resolve-library-id") {
		t.Fatalf("first request missing target function example: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "libraryName") {
		t.Fatalf("first request missing argument example in function call schema: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "Context7-compatible library ID for the Go language MCP SDK") {
		t.Fatalf("first request missing reason guidance in function call schema: %s", string(firstBody))
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

	toolPrompt := testToolPrompt("context7", "context7__resolve-library-id", "Resolve Context7 library IDs")
	req := ChatRequest{
		Model:        "llama3.2",
		SystemPrompt: testBasePrompt(),
		Stream:       true,
		Messages: []Message{
			{Role: "assistant", Content: toolPrompt},
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
	if !strings.Contains(string(firstBody), "# Function Call Schema and Example") {
		t.Fatalf("function call schema block missing in first request: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "functionCall") {
		t.Fatalf("functionCall mention missing in first request: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "context7__resolve-library-id") {
		t.Fatalf("function call schema missing target example in first request: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "libraryName") {
		t.Fatalf("function call example missing argument entry in first request: %s", string(firstBody))
	}
	if !strings.Contains(string(firstBody), "Context7-compatible library ID for the Go language MCP SDK") {
		t.Fatalf("function call example missing reason entry in first request: %s", string(firstBody))
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

func TestOllamaProviderPreservesManualFunctionCallJSON(t *testing.T) {
	t.Parallel()

	manualJSON := "{\n  \"functionCall\": {\n    \"server\": \"context7\",\n    \"name\": \"context7__resolve-library-id\",\n    \"arguments\": {\n      \"libraryName\": \"golang mcp sdk\"\n    },\n    \"reason\": \"To retrieve the correct Context7-compatible library ID for the Go language MCP SDK, which is required to fetch its documentation.\"\n  }\n}"

	var (
		requestCount int
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
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, fmt.Sprintf(`{"message":{"role":"assistant","content":%q}, "done": false}`+"\n", manualJSON))
			io.WriteString(w, `{"message":{"role":"assistant","content":""}, "done": true}`+"\n")
		case 2:
			secondBody = body
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"message":{"role":"assistant","content":"Done."}, "done": false}`+"\n")
			io.WriteString(w, `{"message":{"role":"assistant","content":""}, "done": true}`+"\n")
		default:
			t.Fatalf("unexpected request number %d", requestCount)
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
		Model:        model.Name,
		SystemPrompt: testBasePrompt(),
		Stream:       true,
		Messages: []Message{
			{Role: "assistant", Content: testToolPrompt("context7", "context7__resolve-library-id", "Resolve IDs")},
			{Role: "user", Content: "Use a tool."},
		},
		Tools: []ToolDefinition{
			{
				Name:        "context7__resolve-library-id",
				Description: "Resolve Context7 IDs",
				Server:      "context7",
				Method:      "resolve-library-id",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"libraryName": map[string]any{"type": "string"}},
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
		select {
		case <-ctx.Done():
			t.Fatalf("context done: %v", ctx.Err())
		case chunk, ok := <-stream:
			if !ok {
				t.Fatalf("stream closed")
			}
			return chunk
		}
		return StreamChunk{}
	}

	for {
		chunk := expect()
		if chunk.Type == ChunkToolCall {
			if chunk.ToolCall == nil {
				t.Fatalf("tool call missing payload")
			}
			if err := chunk.ToolCall.Respond(ctx, ToolResult{Content: "{}"}); err != nil {
				t.Fatalf("respond tool call: %v", err)
			}
			break
		}
	}

	for {
		chunk := expect()
		if chunk.Type == ChunkDone {
			break
		}
	}

	if len(secondBody) == 0 {
		t.Fatalf("expected follow-up request body")
	}

	var payload ollamaRequestPayload
	if err := json.Unmarshal(secondBody, &payload); err != nil {
		t.Fatalf("unmarshal second request: %v", err)
	}

	found := false
	for _, msg := range payload.Messages {
		if msg.Role == "assistant" && msg.Content == manualJSON {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected assistant message to preserve manual JSON, payload=%+v", payload.Messages)
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

func TestOpenAIProviderHandlesChooseFunctionJSON(t *testing.T) {
	t.Parallel()

	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		switch requestCount {
		case 1:
			payload := `{"chooseFunction":{"functionName":"context7__resolve-library-id","reason":"Need schema"}}`
			fmt.Fprintf(w, `data: {"choices":[{"delta":{"content":%s}}]}`+"\n\n", strconv.Quote(payload))
			if flusher != nil {
				flusher.Flush()
			}
			io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			io.WriteString(w, "data: [DONE]\n\n")
		case 2:
			io.WriteString(w, `data: {"choices":[{"delta":{"content":"Thanks"},"finish_reason":"stop"}]}`+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			io.WriteString(w, "data: [DONE]\n\n")
		default:
			t.Fatalf("unexpected request count %d", requestCount)
		}
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

	req := ChatRequest{
		Model:  model.Name,
		Stream: true,
		Messages: []Message{
			{Role: "user", Content: "Need docs"},
		},
		Tools: []ToolDefinition{
			testChooseFunctionDefinition(),
			{
				Name:        "context7__resolve-library-id",
				Description: "Resolve Context7 IDs",
				Server:      "context7",
				Method:      "resolve-library-id",
				Parameters: map[string]any{
					"type": "object",
				},
			},
		},
	}

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
				t.Fatalf("stream closed unexpectedly")
			}
			return chunk
		}
		return StreamChunk{}
	}

	if chunk := expect(); chunk.Type != ChunkThinking {
		t.Fatalf("expected thinking chunk, got %v", chunk.Type)
	}
	if chunk := expect(); chunk.Type != ChunkToken {
		t.Fatalf("expected chooseFunction token chunk, got %v", chunk.Type)
	}

	callChunk := expect()
	if callChunk.Type != ChunkToolCall {
		t.Fatalf("expected tool call chunk, got %v", callChunk.Type)
	}
	if callChunk.ToolCall == nil {
		t.Fatalf("tool call chunk missing payload")
	}
	if callChunk.ToolCall.Server != routeIntentServerName {
		t.Fatalf("expected route-intent server, got %s", callChunk.ToolCall.Server)
	}
	if callChunk.ToolCall.Method != routeIntentToolName {
		t.Fatalf("expected chooseFunction method, got %s", callChunk.ToolCall.Method)
	}
	args := callChunk.ToolCall.Arguments
	if args["functionName"] != "context7__resolve-library-id" {
		t.Fatalf("expected functionName argument, got %v", args["functionName"])
	}
	if err := callChunk.ToolCall.Respond(ctx, ToolResult{Content: `{"type":"object"}`}); err != nil {
		t.Fatalf("respond schema: %v", err)
	}

	if chunk := expect(); chunk.Type != ChunkToken || chunk.Content != "Thanks" {
		t.Fatalf("expected final answer token, got %#v", chunk)
	}
	if chunk := expect(); chunk.Type != ChunkDone {
		t.Fatalf("expected done chunk, got %v", chunk.Type)
	}
	if requestCount != 2 {
		t.Fatalf("expected two OpenAI requests, got %d", requestCount)
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

func TestOpenAIProviderChunksToolResults(t *testing.T) {
	t.Parallel()

	chunker, err := tokenizer.NewChunker(32)
	if err != nil {
		t.Fatalf("NewChunker() error = %v", err)
	}

	provider := &openAIProvider{
		chunker: chunker,
	}

	definitions := map[string]ToolDefinition{
		"context7__get": {
			Server:      "context7",
			Method:      "get",
			Description: "Get docs",
			Name:        "context7__get",
		},
	}

	call := toolCallRequest{
		Call: openAIToolCall{
			ID: "call_A",
			Function: openAIToolFunction{
				Name:      "context7__get",
				Arguments: `{"query":"chunk"}`,
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := make(chan StreamChunk, 1)
	done := make(chan []openAIMessage, 1)

	go func() {
		msgs, err := provider.awaitToolResult(ctx, stream, definitions, call)
		if err != nil {
			t.Errorf("awaitToolResult() error = %v", err)
			close(done)
			return
		}
		done <- msgs
	}()

	chunk := <-stream
	if chunk.Type != ChunkToolCall || chunk.ToolCall == nil {
		t.Fatalf("expected tool call chunk, got %#v", chunk)
	}

	longContent := strings.Repeat("chunked tool context requires splitting. ", 200)
	if err := chunk.ToolCall.Respond(ctx, ToolResult{Content: longContent}); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	msgs := <-done
	if len(msgs) < 2 {
		t.Fatalf("expected chunked tool messages, got %d", len(msgs))
	}

	var combined strings.Builder
	for _, msg := range msgs {
		if msg.Role != "tool" {
			t.Fatalf("unexpected role %q", msg.Role)
		}
		if msg.ToolCallID != call.Call.ID {
			t.Fatalf("unexpected tool call id %q", msg.ToolCallID)
		}
		combined.WriteString(msg.Content)
	}

	want := strings.TrimSpace(longContent)
	if combined.String() != want {
		t.Fatalf("combined content mismatch")
	}
}

func TestOllamaProviderChunksToolResults(t *testing.T) {
	t.Parallel()

	chunker, err := tokenizer.NewChunker(32)
	if err != nil {
		t.Fatalf("NewChunker() error = %v", err)
	}

	provider := &ollamaProvider{
		chunker: chunker,
	}

	definitions := map[string]ToolDefinition{
		"context7__get": {
			Server:      "context7",
			Method:      "get",
			Description: "Get docs",
			Name:        "context7__get",
		},
	}

	call := toolCallRequest{
		Call: openAIToolCall{
			ID: "call_B",
			Function: openAIToolFunction{
				Name:      "context7__get",
				Arguments: `{"query":"chunk"}`,
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := make(chan StreamChunk, 1)
	done := make(chan []ollamaMessage, 1)

	go func() {
		msgs, err := provider.awaitToolResult(ctx, stream, definitions, call)
		if err != nil {
			t.Errorf("awaitToolResult() error = %v", err)
			close(done)
			return
		}
		done <- msgs
	}()

	chunk := <-stream
	if chunk.ToolCall == nil {
		t.Fatalf("expected tool call chunk")
	}

	longContent := strings.Repeat("chunked ollama tool content requires splitting. ", 200)
	if err := chunk.ToolCall.Respond(ctx, ToolResult{Content: longContent}); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	msgs := <-done
	if len(msgs) < 2 {
		t.Fatalf("expected chunked messages, got %d", len(msgs))
	}

	var combined strings.Builder
	for _, msg := range msgs {
		if msg.Role != "tool" {
			t.Fatalf("unexpected role %q", msg.Role)
		}
		if msg.ToolName != call.Call.Function.Name {
			t.Fatalf("unexpected tool name %q", msg.ToolName)
		}
		combined.WriteString(msg.Content)
	}

	want := strings.TrimSpace(longContent)
	if combined.String() != want {
		t.Fatalf("combined content mismatch")
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
