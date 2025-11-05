package llm

import (
	"context"
)

// Message represents a single conversation message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Logger allows providers to emit debug logs without depending on a concrete implementation.
type Logger interface {
	Debugf(format string, args ...any)
}

type contextKeyLogger struct{}

// WithLogger attaches a debug logger to the context so providers can emit request/response logs.
func WithLogger(ctx context.Context, logger Logger) context.Context {
	if logger == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKeyLogger{}, logger)
}

// LoggerFromContext retrieves a logger that was previously attached via WithLogger.
func LoggerFromContext(ctx context.Context) Logger {
	if v, ok := ctx.Value(contextKeyLogger{}).(Logger); ok {
		return v
	}
	return nil
}

// ChatRequest describes a LLM chat completion request.
type ChatRequest struct {
	Model        string           `json:"model"`
	Messages     []Message        `json:"messages"`
	SystemPrompt string           `json:"systemPrompt,omitempty"`
	Stream       bool             `json:"stream"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
}

// ChunkType is the type of a streaming response chunk.
type ChunkType int

const (
	// ChunkThinking indicates the provider is still preparing a response.
	ChunkThinking ChunkType = iota + 1
	// ChunkToken carries a new token.
	ChunkToken
	// ChunkToolCall indicates the LLM is requesting a MCP tool invocation.
	ChunkToolCall
	// ChunkDone signals the response has finished streaming.
	ChunkDone
	// ChunkError signals an error mid stream.
	ChunkError
)

// ToolCallResponder handles sending a tool result back to the LLM provider.
type ToolCallResponder func(context.Context, ToolResult) error

// ToolCall encapsulates a MCP invocation request from the LLM.
type ToolCall struct {
	Server      string
	Method      string
	Description string
	Arguments   map[string]any
	Respond     ToolCallResponder
}

// ToolResult captures the outcome of a tool call that should be sent back to the LLM.
type ToolResult struct {
	Content string
	IsError bool
}

// ToolDefinition describes a single callable tool provided to the LLM.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Server      string         `json:"server"`
	Method      string         `json:"method"`
	Parameters  map[string]any `json:"parameters"`
}

// StreamChunk represents a single streamed chunk.
type StreamChunk struct {
	Type     ChunkType
	Content  string
	Err      error
	ToolCall *ToolCall
}

// ChatProvider defines streaming chat interactions.
type ChatProvider interface {
	Stream(context.Context, ChatRequest) (<-chan StreamChunk, error)
}
