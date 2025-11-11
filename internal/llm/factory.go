package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gamzabox/humble-ai-cli/internal/config"
)

// HTTPClient abstracts http.Client for testability.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Factory wires config models to providers.
type Factory struct {
	client HTTPClient
}

const defaultTemperature = 0.1

func toolRole(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return "tool"
	}
	return "tool:" + server
}

// NewFactory builds a Factory with optional custom HTTP client.
func NewFactory(client HTTPClient) *Factory {
	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	return &Factory{client: client}
}

// Create instantiates a provider for a model.
func (f *Factory) Create(model config.Model) (ChatProvider, error) {
	switch strings.ToLower(model.Provider) {
	case "openai":
		if model.APIKey == "" {
			return nil, errors.New("openai provider requires apiKey")
		}
		base := model.BaseURL
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		return &openAIProvider{
			client:  f.client,
			baseURL: strings.TrimRight(base, "/"),
			apiKey:  model.APIKey,
		}, nil
	case "ollama":
		base := model.BaseURL
		if base == "" {
			base = "http://localhost:11434"
		}
		return &ollamaProvider{
			client:  f.client,
			baseURL: strings.TrimRight(base, "/"),
		}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", model.Provider)
	}
}

var _ ChatProvider = (*openAIProvider)(nil)
var _ ChatProvider = (*ollamaProvider)(nil)

type openAIProvider struct {
	client  HTTPClient
	baseURL string
	apiKey  string
}

func (p *openAIProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	stream := make(chan StreamChunk)
	go func() {
		defer close(stream)

		messages := buildOpenAIMessages(req)
		openAITools, definitions := buildOpenAITools(req.Tools)
		thinkingSent := false

		for {
			result, err := p.streamOnce(ctx, req.Model, messages, openAITools, stream, &thinkingSent)
			if err != nil {
				if err != context.Canceled {
					stream <- StreamChunk{Type: ChunkError, Err: err}
				}
				return
			}

			messages = append(messages, result.assistantMessage)

			if len(result.toolCalls) == 0 {
				stream <- StreamChunk{Type: ChunkDone}
				return
			}

			for _, call := range result.toolCalls {
				toolMessage, err := p.awaitToolResult(ctx, stream, definitions, call)
				if err != nil {
					if err != context.Canceled {
						stream <- StreamChunk{Type: ChunkError, Err: err}
					}
					return
				}
				messages = append(messages, toolMessage)
			}
		}
	}()
	return stream, nil
}

type openAIPassResult struct {
	assistantMessage openAIMessage
	toolCalls        []toolCallRequest
}

func (p *openAIProvider) streamOnce(ctx context.Context, model string, messages []openAIMessage, tools []openAITool, stream chan<- StreamChunk, thinkingSent *bool) (*openAIPassResult, error) {
	payload, err := json.Marshal(openAIRequestPayload{
		Model:       model,
		Stream:      true,
		Messages:    messages,
		Tools:       tools,
		Temperature: defaultTemperature,
	})
	if err != nil {
		return nil, err
	}

	logger := LoggerFromContext(ctx)
	if logger != nil {
		logger.Debugf("Open AI LLM request: %s", string(payload))
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("openai response %d: %s", resp.StatusCode, string(body))
	}

	defer resp.Body.Close()

	if !*thinkingSent {
		stream <- StreamChunk{Type: ChunkThinking}
		*thinkingSent = true
	}

	var (
		builder       strings.Builder
		accumulator   = newToolAccumulator()
		assistantCall = openAIMessage{Role: "assistant"}
	)

	logResponse := func(toolCalls []toolCallRequest) {
		if logger == nil {
			return
		}
		if len(toolCalls) > 0 {
			logger.Debugf("LLM response (tool_calls): content=%q toolCalls=%d", assistantCall.Content, len(toolCalls))
			return
		}
		logger.Debugf("LLM response: %s", assistantCall.Content)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, err
		}

		for _, choice := range chunk.Choices {
			emitReasoningChunks(stream, choice.Delta.Reasoning, choice.Delta.ReasoningContent)

			if choice.Delta.Content != "" {
				stream <- StreamChunk{Type: ChunkToken, Content: choice.Delta.Content}
				builder.WriteString(choice.Delta.Content)
			}

			if len(choice.Delta.ToolCalls) > 0 {
				accumulator.add(choice.Delta.ToolCalls)
			}

			if choice.FinishReason == "stop" {
				assistantCall.Content = builder.String()
				logResponse(nil)
				return &openAIPassResult{assistantMessage: assistantCall}, nil
			}
			if choice.FinishReason == "tool_calls" {
				assistantCall.Content = builder.String()
				assistantCall.ToolCalls = accumulator.complete()
				toolRequests := accumulator.requests()
				logResponse(toolRequests)
				return &openAIPassResult{
					assistantMessage: assistantCall,
					toolCalls:        toolRequests,
				}, nil
			}
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}

	assistantCall.Content = builder.String()
	logResponse(nil)
	return &openAIPassResult{assistantMessage: assistantCall}, nil
}

func (p *openAIProvider) awaitToolResult(ctx context.Context, stream chan<- StreamChunk, defs map[string]ToolDefinition, call toolCallRequest) (openAIMessage, error) {
	definition, ok := defs[call.Call.Function.Name]
	if !ok {
		return openAIMessage{}, fmt.Errorf("unknown tool requested: %s", call.Call.Function.Name)
	}

	args, err := call.arguments()
	if err != nil {
		return openAIMessage{}, err
	}

	resultCh := make(chan ToolResult, 1)
	tc := &ToolCall{
		Server:      definition.Server,
		Method:      definition.Method,
		Description: definition.Description,
		Arguments:   args,
		Respond: func(ctx context.Context, result ToolResult) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case resultCh <- result:
				return nil
			}
		},
	}

	stream <- StreamChunk{Type: ChunkToolCall, ToolCall: tc}

	var result ToolResult
	select {
	case <-ctx.Done():
		return openAIMessage{}, ctx.Err()
	case result = <-resultCh:
	}

	content := strings.TrimSpace(result.Content)
	if content == "" {
		content = "{}"
	}

	toolMessage := openAIMessage{
		Role:       toolRole(definition.Server),
		Content:    content,
		ToolCallID: call.Call.ID,
		Name:       definition.Name,
	}
	return toolMessage, nil
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string              `json:"type"`
	Function openAIToolSignature `json:"function"`
}

type openAIToolSignature struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openAIRequestPayload struct {
	Model       string          `json:"model"`
	Stream      bool            `json:"stream"`
	Messages    []openAIMessage `json:"messages"`
	Tools       []openAITool    `json:"tools,omitempty"`
	Temperature float64         `json:"temperature"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta        openAIDelta `json:"delta"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

type openAIDelta struct {
	Content          string                `json:"content"`
	ToolCalls        []openAIToolCallDelta `json:"tool_calls"`
	Reasoning        json.RawMessage       `json:"reasoning"`
	ReasoningContent json.RawMessage       `json:"reasoning_content"`
}

type openAIToolCallDelta struct {
	Index    int                `json:"index"`
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

func buildOpenAIMessages(req ChatRequest) []openAIMessage {
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, openAIMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}
	for _, msg := range req.Messages {
		messages = append(messages, openAIMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return messages
}

func buildOpenAITools(defs []ToolDefinition) ([]openAITool, map[string]ToolDefinition) {
	if len(defs) == 0 {
		return nil, nil
	}

	out := make([]openAITool, 0, len(defs))
	index := make(map[string]ToolDefinition, len(defs))

	for _, def := range defs {
		parameters := cloneAnyMap(def.Parameters)
		if parameters == nil {
			parameters = defaultToolSchema()
		}

		out = append(out, openAITool{
			Type: "function",
			Function: openAIToolSignature{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  parameters,
			},
		})
		index[def.Name] = def
	}
	return out, index
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return nil
	}
	var dst map[string]any
	if err := json.Unmarshal(data, &dst); err != nil {
		return nil
	}
	return dst
}

func defaultToolSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": true,
	}
}

type toolAccumulator struct {
	items map[int]*openAIToolCall
	order []int
}

func newToolAccumulator() *toolAccumulator {
	return &toolAccumulator{
		items: make(map[int]*openAIToolCall),
	}
}

func (a *toolAccumulator) add(deltas []openAIToolCallDelta) {
	for _, delta := range deltas {
		entry, ok := a.items[delta.Index]
		if !ok {
			entry = &openAIToolCall{
				ID:       delta.ID,
				Type:     delta.Type,
				Function: openAIToolFunction{Name: delta.Function.Name},
			}
			a.items[delta.Index] = entry
			a.order = append(a.order, delta.Index)
		}
		if delta.Function.Name != "" {
			entry.Function.Name = delta.Function.Name
		}
		entry.Function.Arguments += delta.Function.Arguments
		if entry.ID == "" {
			entry.ID = delta.ID
		}
	}
}

func (a *toolAccumulator) complete() []openAIToolCall {
	if len(a.items) == 0 {
		return nil
	}
	out := make([]openAIToolCall, 0, len(a.order))
	for _, idx := range a.order {
		out = append(out, *a.items[idx])
	}
	return out
}

func (a *toolAccumulator) requests() []toolCallRequest {
	if len(a.items) == 0 {
		return nil
	}
	out := make([]toolCallRequest, 0, len(a.order))
	for _, idx := range a.order {
		out = append(out, toolCallRequest{Call: *a.items[idx]})
	}
	return out
}

type toolCallRequest struct {
	Call openAIToolCall
}

func (r toolCallRequest) arguments() (map[string]any, error) {
	var args map[string]any
	raw := strings.TrimSpace(r.Call.Function.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("parse tool arguments: %w", err)
	}
	return args, nil
}

type ollamaProvider struct {
	client  HTTPClient
	baseURL string
}

type ollamaMessage struct {
	Role      string                   `json:"role"`
	Content   string                   `json:"content"`
	ToolCalls []ollamaOutgoingToolCall `json:"tool_calls,omitempty"`
	ToolName  string                   `json:"tool_name,omitempty"`
}

type ollamaRequestPayload struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
}

type ollamaToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ollamaRawToolCall struct {
	ID        string              `json:"id"`
	Type      string              `json:"type"`
	Name      string              `json:"name"`
	Arguments json.RawMessage     `json:"arguments"`
	Function  *ollamaToolFunction `json:"function"`
}

type ollamaStreamChunk struct {
	Done    bool `json:"done"`
	Message struct {
		Role              string              `json:"role"`
		Content           string              `json:"content"`
		ToolCalls         []ollamaRawToolCall `json:"tool_calls"`
		Reasoning         json.RawMessage     `json:"reasoning"`
		Thinking          json.RawMessage     `json:"thinking"`
		Thoughts          json.RawMessage     `json:"thoughts"`
		InternalThoughts  json.RawMessage     `json:"internal_thoughts"`
		InternalMonologue json.RawMessage     `json:"internal_monologue"`
	} `json:"message"`
	Reasoning         json.RawMessage `json:"reasoning"`
	Thinking          json.RawMessage `json:"thinking"`
	Thoughts          json.RawMessage `json:"thoughts"`
	InternalThoughts  json.RawMessage `json:"internal_thoughts"`
	InternalMonologue json.RawMessage `json:"internal_monologue"`
	Error             string          `json:"error"`
}

type ollamaOutgoingToolCall struct {
	ID       string                      `json:"id,omitempty"`
	Type     string                      `json:"type"`
	Function ollamaOutgoingToolSignature `json:"function"`
}

type ollamaOutgoingToolSignature struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (tc ollamaRawToolCall) toOpenAIToolCall() openAIToolCall {
	name := strings.TrimSpace(tc.Name)
	if tc.Function != nil {
		if fn := strings.TrimSpace(tc.Function.Name); fn != "" {
			name = fn
		}
	}
	if name == "" && tc.Function != nil {
		name = tc.Function.Name
	}

	args := tc.Arguments
	if tc.Function != nil && len(tc.Function.Arguments) > 0 {
		args = tc.Function.Arguments
	}

	argText := strings.TrimSpace(string(args))
	if argText == "" || argText == "null" {
		argText = "{}"
	}

	callType := strings.TrimSpace(tc.Type)
	if callType == "" {
		callType = "function"
	}

	return openAIToolCall{
		ID:   tc.ID,
		Type: callType,
		Function: openAIToolFunction{
			Name:      name,
			Arguments: argText,
		},
	}
}

func (p *ollamaProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	stream := make(chan StreamChunk)

	messages := buildOllamaMessages(req)
	_, definitions := buildOpenAITools(req.Tools)

	go func() {
		defer close(stream)

		thinkingSent := false
		for {
			result, err := p.streamOnce(ctx, req.Model, true, messages, stream, &thinkingSent, definitions)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				stream <- StreamChunk{Type: ChunkError, Err: err}
				return
			}

			messages = append(messages, result.assistantMessage)

			if len(result.toolCalls) == 0 {
				stream <- StreamChunk{Type: ChunkDone}
				return
			}

			if len(definitions) == 0 {
				stream <- StreamChunk{Type: ChunkError, Err: fmt.Errorf("ollama requested tool call but no tool definitions provided")}
				return
			}

			for _, call := range result.toolCalls {
				toolMessage, err := p.awaitToolResult(ctx, stream, definitions, call)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return
					}
					stream <- StreamChunk{Type: ChunkError, Err: err}
					return
				}
				messages = append(messages, toolMessage)
			}
		}
	}()

	return stream, nil
}

type ollamaPassResult struct {
	assistantMessage ollamaMessage
	toolCalls        []toolCallRequest
}

func (p *ollamaProvider) streamOnce(
	ctx context.Context,
	model string,
	streaming bool,
	messages []ollamaMessage,
	stream chan<- StreamChunk,
	thinkingSent *bool,
	definitions map[string]ToolDefinition,
) (*ollamaPassResult, error) {
	payload, err := buildOllamaPayload(model, messages, streaming)
	if err != nil {
		return nil, err
	}

	logger := LoggerFromContext(ctx)
	if logger != nil {
		logger.Debugf("Ollama LLM request: %s", string(payload))
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("ollama response %d: %s", resp.StatusCode, string(body))
	}
	defer resp.Body.Close()

	if !*thinkingSent {
		stream <- StreamChunk{Type: ChunkThinking}
		*thinkingSent = true
	}

	decoder := json.NewDecoder(resp.Body)
	var (
		builder   strings.Builder
		toolCalls []openAIToolCall
		assistant = ollamaMessage{Role: "assistant"}
	)

	for {
		var chunk ollamaStreamChunk
		if err := decoder.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, context.Canceled) {
				return nil, err
			}
			return nil, err
		}

		if chunk.Error != "" {
			stream <- StreamChunk{Type: ChunkError, Err: errors.New(chunk.Error)}
			continue
		}

		emitReasoningChunks(stream,
			chunk.Message.Reasoning,
			chunk.Message.Thinking,
			chunk.Message.Thoughts,
			chunk.Message.InternalThoughts,
			chunk.Message.InternalMonologue,
			chunk.Reasoning,
			chunk.Thinking,
			chunk.Thoughts,
			chunk.InternalThoughts,
			chunk.InternalMonologue,
		)

		if chunk.Message.Content != "" {
			stream <- StreamChunk{Type: ChunkToken, Content: chunk.Message.Content}
			builder.WriteString(chunk.Message.Content)
		}

		if len(chunk.Message.ToolCalls) > 0 {
			for _, raw := range chunk.Message.ToolCalls {
				call := raw.toOpenAIToolCall()
				toolCalls = append(toolCalls, call)
			}
		}

		if chunk.Done {
			break
		}
	}

	assistant.Content = builder.String()
	if len(toolCalls) == 0 {
		if manualCalls, cleaned := parseManualToolCall(assistant.Content); len(manualCalls) > 0 {
			toolCalls = manualCalls
			assistant.Content = strings.TrimSpace(cleaned)
		}
	}

	if len(toolCalls) > 0 {
		callContent := formatOllamaToolCallContent(toolCalls, definitions)
		if callContent != "" {
			content := strings.TrimSpace(assistant.Content)
			if content == "" {
				assistant.Content = callContent
			} else {
				assistant.Content = strings.TrimSpace(content + "\n\n" + callContent)
			}
		}
	}

	logResponse := func(toolCallCount int) {
		if logger == nil {
			return
		}
		if toolCallCount > 0 {
			logger.Debugf("LLM response (tool_calls): content=%q toolCalls=%d", assistant.Content, toolCallCount)
			return
		}
		logger.Debugf("LLM response: %s", assistant.Content)
	}
	if len(toolCalls) == 0 {
		logResponse(0)
		return &ollamaPassResult{assistantMessage: assistant}, nil
	}

	requests := make([]toolCallRequest, 0, len(toolCalls))
	for _, call := range toolCalls {
		requests = append(requests, toolCallRequest{Call: call})
	}
	logResponse(len(toolCalls))

	return &ollamaPassResult{
		assistantMessage: assistant,
		toolCalls:        requests,
	}, nil
}

func (p *ollamaProvider) awaitToolResult(
	ctx context.Context,
	stream chan<- StreamChunk,
	definitions map[string]ToolDefinition,
	call toolCallRequest,
) (ollamaMessage, error) {
	definition, ok := definitions[call.Call.Function.Name]
	if !ok {
		return ollamaMessage{}, fmt.Errorf("unknown tool requested: %s", call.Call.Function.Name)
	}

	args, err := call.arguments()
	if err != nil {
		return ollamaMessage{}, err
	}

	resultCh := make(chan ToolResult, 1)
	tc := &ToolCall{
		Server:      definition.Server,
		Method:      definition.Method,
		Description: definition.Description,
		Arguments:   args,
		Respond: func(ctx context.Context, result ToolResult) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case resultCh <- result:
				return nil
			}
		},
	}

	stream <- StreamChunk{Type: ChunkToolCall, ToolCall: tc}

	var result ToolResult
	select {
	case <-ctx.Done():
		return ollamaMessage{}, ctx.Err()
	case result = <-resultCh:
	}

	content := strings.TrimSpace(result.Content)
	if content == "" {
		content = "{}"
	}

	return ollamaMessage{
		Role:     toolRole(definition.Server),
		Content:  content,
		ToolName: call.Call.Function.Name,
	}, nil
}

func buildOllamaRequest(req ChatRequest) ([]byte, error) {
	messages := buildOllamaMessages(req)
	return buildOllamaPayload(req.Model, messages, req.Stream)
}

func buildOllamaMessages(req ChatRequest) []ollamaMessage {
	messages := make([]ollamaMessage, 0, len(req.Messages)+1)
	systemPrompt := enhanceSystemPromptWithToolSchema(req.SystemPrompt, req.Tools)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, ollamaMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}
	for _, msg := range req.Messages {
		messages = append(messages, ollamaMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return messages
}

func enhanceSystemPromptWithToolSchema(prompt string, defs []ToolDefinition) string {
	prompt = strings.TrimSpace(prompt)
	schema := buildToolSchemaPrompt(defs)
	if schema == "" {
		return prompt
	}
	if prompt == "" {
		return schema
	}
	return prompt + "\n\n" + schema
}

func buildToolSchemaPrompt(defs []ToolDefinition) string {
	if len(defs) == 0 {
		return ""
	}

	type toolEntry struct {
		name        string
		description string
		parameters  map[string]any
	}

	groups := make(map[string][]toolEntry)
	for _, def := range defs {
		server := strings.TrimSpace(def.Server)
		if server == "" {
			server = "default"
		}

		params := cloneAnyMap(def.Parameters)
		if params == nil {
			params = defaultToolSchema()
		}

		desc := strings.TrimSpace(def.Description)
		if desc == "" {
			desc = "No description provided."
		}

		groups[server] = append(groups[server], toolEntry{
			name:        def.Name,
			description: desc,
			parameters:  params,
		})
	}

	serverNames := make([]string, 0, len(groups))
	for server := range groups {
		serverNames = append(serverNames, server)
	}
	sort.Strings(serverNames)

	var builder strings.Builder
	builder.WriteString("FUNCTIONS:\n\n# Connected MCP Servers\n")

	for _, server := range serverNames {
		builder.WriteString("\n## MCP Server: ")
		builder.WriteString(server)
		builder.WriteString("\nThese are tool name, description and input schema.\n")

		tools := groups[server]
		sort.Slice(tools, func(i, j int) bool {
			return tools[i].name < tools[j].name
		})

		for _, tool := range tools {
			builder.WriteString("\n- name: **")
			builder.WriteString(tool.name)
			builder.WriteString("**\n")
			builder.WriteString("- description: ")
			builder.WriteString(tool.description)
			builder.WriteString("\n\n")
			builder.WriteString("    Input Schema:\n")

			schemaJSON, err := json.MarshalIndent(tool.parameters, "", "  ")
			if err != nil {
				builder.WriteString("    {}\n\n")
				continue
			}

			lines := strings.Split(string(schemaJSON), "\n")
			for _, line := range lines {
				builder.WriteString("    ")
				builder.WriteString(line)
				builder.WriteByte('\n')
			}
			builder.WriteByte('\n')
		}
	}

	builder.WriteString("\n\nFUNCTION_CALL:\n- Schema\n{\n\t\"server\": \"server name\",\n\t\"name\": \"function name\",\n\t\"arguments\": {\n\t  \"arg1 name\": \"argument1 value\",\n\t  \"arg2 name\": \"argument2 value\",\n\t}\n}\n- Example\n{\n\t\"server\": \"context7\",\n\t\"name\": \"resolve-library-id\",\n\t\"arguments\": {\n\t  \"libraryName\": \"java\"\n\t}\n}")

	return strings.TrimRight(builder.String(), "\n")
}

func buildOllamaPayload(model string, messages []ollamaMessage, stream bool) ([]byte, error) {
	payload := ollamaRequestPayload{
		Model:    model,
		Stream:   stream,
		Messages: messages,
		Options: map[string]any{
			"temperature": defaultTemperature,
		},
	}
	return json.Marshal(payload)
}

func parseManualToolCall(content string) ([]openAIToolCall, string) {
	cleaned := content
	var calls []openAIToolCall

	for {
		blocks := findJSONBlocks(cleaned)
		if len(blocks) == 0 {
			break
		}

		parsed := false
		for _, block := range blocks {
			call, ok := parseToolCallJSON(cleaned[block.start:block.end])
			if !ok {
				continue
			}
			calls = append(calls, call)
			cleaned = removeJSONBlock(cleaned, block.start, block.end)
			parsed = true
			break
		}

		if !parsed {
			break
		}
	}

	if len(calls) == 0 {
		return nil, content
	}
	return calls, cleaned
}

type jsonBlock struct {
	start int
	end   int
}

func findJSONBlocks(text string) []jsonBlock {
	var (
		blocks   []jsonBlock
		depth    int
		start    = -1
		inString bool
		escape   bool
	)

	for i := 0; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				blocks = append(blocks, jsonBlock{start: start, end: i + 1})
			}
		}
	}
	return blocks
}

func parseToolCallJSON(raw string) (openAIToolCall, bool) {
	var payload struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return openAIToolCall{}, false
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		return openAIToolCall{}, false
	}

	argText := strings.TrimSpace(string(payload.Arguments))
	if argText == "" || argText == "null" {
		argText = "{}"
	}

	return openAIToolCall{
		Type: "function",
		Function: openAIToolFunction{
			Name:      name,
			Arguments: argText,
		},
	}, true
}

func removeJSONBlock(content string, start, end int) string {
	left := strings.TrimRight(content[:start], " \t\r\n")
	left = trimTrailingFence(left)
	right := strings.TrimLeft(content[end:], " \t\r\n")
	right = trimLeadingFence(right)

	switch {
	case left == "":
		return strings.TrimSpace(right)
	case right == "":
		return strings.TrimSpace(left)
	default:
		return strings.TrimSpace(left + "\n\n" + right)
	}
}

func trimTrailingFence(s string) string {
	s = strings.TrimRight(s, " \t\r\n")
	if strings.HasSuffix(s, "```") {
		return strings.TrimRight(s[:len(s)-3], " \t\r\n")
	}
	return s
}

func trimLeadingFence(s string) string {
	s = strings.TrimLeft(s, " \t\r\n")
	if strings.HasPrefix(s, "```") {
		return strings.TrimLeft(s[3:], " \t\r\n")
	}
	return s
}

func formatOllamaToolCallContent(calls []openAIToolCall, definitions map[string]ToolDefinition) string {
	if len(calls) == 0 {
		return ""
	}

	payloads := make([]string, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		data := map[string]any{
			"server": "",
			"name":   name,
		}

		if def, ok := definitions[name]; ok {
			server := strings.TrimSpace(def.Server)
			if server != "" {
				data["server"] = server
			}
		}

		rawArgs := strings.TrimSpace(call.Function.Arguments)
		switch rawArgs {
		case "":
			data["arguments"] = map[string]any{}
		default:
			var parsed any
			if err := json.Unmarshal([]byte(rawArgs), &parsed); err != nil {
				data["arguments"] = rawArgs
			} else {
				data["arguments"] = parsed
			}
		}

		if call.ID != "" {
			data["id"] = call.ID
		}
		if call.Type != "" {
			data["type"] = call.Type
		}

		encoded, err := json.Marshal(data)
		if err != nil {
			continue
		}
		payloads = append(payloads, string(encoded))
	}

	return strings.Join(payloads, "\n")
}

func extractReasoningSegments(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil
	}

	var out []string
	collectReasoningStrings(&out, data)
	return out
}

func emitReasoningChunks(stream chan<- StreamChunk, raws ...json.RawMessage) {
	for _, raw := range raws {
		for _, text := range extractReasoningSegments(raw) {
			stream <- StreamChunk{Type: ChunkThinking, Content: text}
		}
	}
}

func collectReasoningStrings(out *[]string, value any) {
	switch v := value.(type) {
	case string:
		text := strings.TrimSpace(v)
		if text != "" {
			*out = append(*out, text)
		}
	case []any:
		for _, item := range v {
			collectReasoningStrings(out, item)
		}
	case map[string]any:
		for key, val := range v {
			switch key {
			case "type", "id", "role", "finish_reason", "index":
				continue
			}
			collectReasoningStrings(out, val)
		}
	}
}

// Timeout returns a copy of the factory with a custom timeout.
func (f *Factory) Timeout(d time.Duration) *Factory {
	client := &http.Client{
		Timeout: d,
	}
	return NewFactory(client)
}
