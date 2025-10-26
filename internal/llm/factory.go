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
	"strings"
	"time"

	"gamzabox.com/humble-ai-cli/internal/config"
)

// HTTPClient abstracts http.Client for testability.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Factory wires config models to providers.
type Factory struct {
	client HTTPClient
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
		Model:    model,
		Stream:   true,
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		return nil, err
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
			if choice.Delta.Content != "" {
				stream <- StreamChunk{Type: ChunkToken, Content: choice.Delta.Content}
				builder.WriteString(choice.Delta.Content)
			}

			if len(choice.Delta.ToolCalls) > 0 {
				accumulator.add(choice.Delta.ToolCalls)
			}

			if choice.FinishReason == "stop" {
				assistantCall.Content = builder.String()
				return &openAIPassResult{assistantMessage: assistantCall}, nil
			}
			if choice.FinishReason == "tool_calls" {
				assistantCall.Content = builder.String()
				assistantCall.ToolCalls = accumulator.complete()
				return &openAIPassResult{
					assistantMessage: assistantCall,
					toolCalls:        accumulator.requests(),
				}, nil
			}
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}

	assistantCall.Content = builder.String()
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
		Role:       "tool",
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
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string                `json:"content"`
			ToolCalls []openAIToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
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
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaRequestPayload struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Messages []ollamaMessage `json:"messages"`
}

type ollamaStreamChunk struct {
	Done    bool `json:"done"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Error string `json:"error"`
}

func (p *ollamaProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	payload, err := buildOllamaRequest(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("ollama response %d: %s", resp.StatusCode, string(body))
	}

	stream := make(chan StreamChunk)

	go func() {
		defer resp.Body.Close()
		defer close(stream)

		stream <- StreamChunk{Type: ChunkThinking}

		decoder := json.NewDecoder(resp.Body)
		for {
			var chunk ollamaStreamChunk
			if err := decoder.Decode(&chunk); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					stream <- StreamChunk{Type: ChunkDone}
					return
				}
				stream <- StreamChunk{Type: ChunkError, Err: err}
				return
			}

			if chunk.Error != "" {
				stream <- StreamChunk{Type: ChunkError, Err: errors.New(chunk.Error)}
				continue
			}

			if chunk.Message.Content != "" {
				stream <- StreamChunk{Type: ChunkToken, Content: chunk.Message.Content}
			}

			if chunk.Done {
				stream <- StreamChunk{Type: ChunkDone}
				return
			}
		}
	}()

	return stream, nil
}

func buildOllamaRequest(req ChatRequest) ([]byte, error) {
	messages := make([]ollamaMessage, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, ollamaMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}
	for _, msg := range req.Messages {
		messages = append(messages, ollamaMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	payload := ollamaRequestPayload{
		Model:    req.Model,
		Stream:   true,
		Messages: messages,
	}
	return json.Marshal(payload)
}

// Timeout returns a copy of the factory with a custom timeout.
func (f *Factory) Timeout(d time.Duration) *Factory {
	client := &http.Client{
		Timeout: d,
	}
	return NewFactory(client)
}
