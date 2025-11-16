package tokenizer

import (
	"strings"
	"testing"

	"github.com/pkoukk/tiktoken-go"
)

func TestChunkerSplitsText(t *testing.T) {
	t.Parallel()

	chunker, err := NewChunker(32)
	if err != nil {
		t.Fatalf("NewChunker() error = %v", err)
	}

	text := strings.Repeat("chunk tokens precisely. ", 200)
	chunks, err := chunker.ChunkText(text)
	if err != nil {
		t.Fatalf("ChunkText() error = %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	var combined strings.Builder
	for _, part := range chunks {
		tokenCount := tokenLen(t, part)
		if tokenCount > chunker.Limit() {
			t.Fatalf("chunk exceeds limit: %d tokens", tokenCount)
		}
		combined.WriteString(part)
	}

	if combined.String() != text {
		t.Fatalf("combined text mismatch")
	}
}

func TestChunkerReturnsOriginalWhenBelowLimit(t *testing.T) {
	t.Parallel()

	chunker, err := NewChunker(4096)
	if err != nil {
		t.Fatalf("NewChunker() error = %v", err)
	}

	text := "short text"
	chunks, err := chunker.ChunkText(text)
	if err != nil {
		t.Fatalf("ChunkText() error = %v", err)
	}

	if len(chunks) != 1 || chunks[0] != text {
		t.Fatalf("expected unchanged output, got %#v", chunks)
	}
}

func tokenLen(t *testing.T, text string) int {
	t.Helper()
	encoding, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		t.Fatalf("failed to load tokenizer: %v", err)
	}
	return len(encoding.Encode(text, nil, nil))
}
