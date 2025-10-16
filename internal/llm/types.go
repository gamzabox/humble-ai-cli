package llm

import "context"

// Message represents a single conversation message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest describes a chat completion request.
type ChatRequest struct {
	Model        string    `json:"model"`
	Messages     []Message `json:"messages"`
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Stream       bool      `json:"stream"`
}

// ChunkType is the type of a streaming response chunk.
type ChunkType int

const (
	// ChunkThinking indicates the provider is still preparing a response.
	ChunkThinking ChunkType = iota + 1
	// ChunkToken carries a new token.
	ChunkToken
	// ChunkDone signals the response has finished streaming.
	ChunkDone
	// ChunkError signals an error mid stream.
	ChunkError
)

// StreamChunk represents a single streamed chunk.
type StreamChunk struct {
	Type    ChunkType
	Content string
	Err     error
}

// ChatProvider defines streaming chat interactions.
type ChatProvider interface {
	Stream(context.Context, ChatRequest) (<-chan StreamChunk, error)
}
