package tokenizer

import (
	"fmt"

	"github.com/pkoukk/tiktoken-go"
)

const DefaultChunkSize = 1500

// Chunker splits text into fixed-size BPE token segments.
type Chunker struct {
	encoding *tiktoken.Tiktoken
	limit    int
}

// NewChunker constructs a chunker for the provided token limit.
func NewChunker(limit int) (*Chunker, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("chunk size must be positive")
	}
	encoding, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		return nil, fmt.Errorf("load tokenizer: %w", err)
	}
	return &Chunker{
		encoding: encoding,
		limit:    limit,
	}, nil
}

// ChunkText splits the given string into <= limit token blocks.
func (c *Chunker) ChunkText(text string) ([]string, error) {
	if c == nil || c.encoding == nil || c.limit <= 0 {
		return []string{text}, nil
	}

	tokens := c.encoding.Encode(text, nil, nil)
	if len(tokens) == 0 || len(tokens) <= c.limit {
		return []string{text}, nil
	}

	chunks := make([]string, 0, len(tokens)/c.limit+1)
	for start := 0; start < len(tokens); start += c.limit {
		end := start + c.limit
		if end > len(tokens) {
			end = len(tokens)
		}
		chunkTokens := tokens[start:end]
		chunks = append(chunks, c.encoding.Decode(chunkTokens))
	}
	return chunks, nil
}

// Limit returns the chunk size limit in tokens.
func (c *Chunker) Limit() int {
	if c == nil {
		return 0
	}
	return c.limit
}
