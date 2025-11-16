package app

import (
	"strings"
	"testing"

	"github.com/pkoukk/tiktoken-go"

	"github.com/gamzabox/humble-ai-cli/internal/llm"
	"github.com/gamzabox/humble-ai-cli/internal/tokenizer"
)

func TestContextChunkerSplitsMessagesExceedingLimit(t *testing.T) {
	t.Parallel()

	chunker := newTestContextChunker(t, 32)
	msg := llm.Message{
		Role:    "user",
		Content: strings.Repeat("chunking requires accurate token measurements. ", 200),
	}

	encoding, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		t.Fatalf("failed to load tokenizer: %v", err)
	}
	tokens := encoding.Encode(msg.Content, nil, nil)
	if len(tokens) <= chunker.chunker.Limit() {
		t.Fatalf("test message did not exceed chunk limit: got %d tokens", len(tokens))
	}

	result, err := chunker.Chunk([]llm.Message{msg})
	if err != nil {
		t.Fatalf("Chunk() error = %v", err)
	}

	expectedChunks := (len(tokens) + chunker.chunker.Limit() - 1) / chunker.chunker.Limit()
	if len(result) != expectedChunks {
		t.Fatalf("expected %d chunks, got %d", expectedChunks, len(result))
	}

	var combined strings.Builder
	for i, part := range result {
		if part.Role != msg.Role {
			t.Fatalf("chunk %d role = %q, want %q", i, part.Role, msg.Role)
		}
		tokenCount := len(encoding.Encode(part.Content, nil, nil))
		if tokenCount > chunker.chunker.Limit() {
			t.Fatalf("chunk %d exceeds limit: %d tokens", i, tokenCount)
		}
		combined.WriteString(part.Content)
	}

	if combined.String() != msg.Content {
		t.Fatalf("reconstructed content mismatch")
	}
}

func TestContextChunkerLeavesShortMessagesUntouched(t *testing.T) {
	t.Parallel()

	chunker := newTestContextChunker(t, 4096)
	messages := []llm.Message{
		{Role: "system", Content: "Keep responses short."},
		{Role: "assistant", Content: "Sure, noted."},
		{Role: "user", Content: "Explain chunking."},
	}

	encoding, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		t.Fatalf("failed to load tokenizer: %v", err)
	}

	for i, msg := range messages {
		if tokenCount := len(encoding.Encode(msg.Content, nil, nil)); tokenCount >= chunker.chunker.Limit() {
			t.Fatalf("message %d unexpectedly exceeds chunk limit", i)
		}
	}

	chunked, err := chunker.Chunk(messages)
	if err != nil {
		t.Fatalf("Chunk() error = %v", err)
	}

	if len(chunked) != len(messages) {
		t.Fatalf("expected %d messages, got %d", len(messages), len(chunked))
	}

	for i := range messages {
		if chunked[i] != messages[i] {
			t.Fatalf("message %d mutated: %#v vs %#v", i, chunked[i], messages[i])
		}
	}
}

func TestContextChunkerPreservesLargeInput(t *testing.T) {
	t.Parallel()

	chunker := newTestContextChunker(t, tokenizer.DefaultChunkSize)
	content := strings.Repeat("Chunk context verification requires BPE based splitting. ", 2800)

	chunked, err := chunker.Chunk([]llm.Message{{Role: "user", Content: content}})
	if err != nil {
		t.Fatalf("Chunk() error = %v", err)
	}

	var combined strings.Builder
	for _, msg := range chunked {
		combined.WriteString(msg.Content)
	}

	if combined.String() != content {
		t.Fatalf("combined content mismatch (gotLen=%d wantLen=%d)", len(combined.String()), len(content))
	}
}

func newTestContextChunker(t *testing.T, limit int) *contextChunker {
	t.Helper()
	chunker, err := newContextChunker(limit)
	if err != nil {
		t.Fatalf("failed to create chunker: %v", err)
	}
	return chunker
}
