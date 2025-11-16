package app

import (
	"fmt"

	"github.com/gamzabox/humble-ai-cli/internal/llm"
	"github.com/gamzabox/humble-ai-cli/internal/tokenizer"
)

const contextChunkTokenLimit = tokenizer.DefaultChunkSize

// contextChunker splits oversized context messages while preserving roles.
type contextChunker struct {
	chunker *tokenizer.Chunker
}

func newContextChunker(limit int) (*contextChunker, error) {
	chunker, err := tokenizer.NewChunker(limit)
	if err != nil {
		return nil, fmt.Errorf("initialize tokenizer: %w", err)
	}
	return &contextChunker{chunker: chunker}, nil
}

func (c *contextChunker) Chunk(messages []llm.Message) ([]llm.Message, error) {
	if c == nil || c.chunker == nil {
		out := make([]llm.Message, len(messages))
		copy(out, messages)
		return out, nil
	}

	result := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		parts, err := c.chunker.ChunkText(msg.Content)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			result = append(result, msg)
			continue
		}
		for _, part := range parts {
			split := msg
			split.Content = part
			result = append(result, split)
		}
	}

	return result, nil
}
