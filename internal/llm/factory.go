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
	payload, err := buildOpenAIRequest(req)
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

	stream := make(chan StreamChunk)
	go func() {
		defer resp.Body.Close()
		defer close(stream)

		stream <- StreamChunk{Type: ChunkThinking}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				stream <- StreamChunk{Type: ChunkDone}
				return
			}

			var chunk openAIStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				stream <- StreamChunk{Type: ChunkError, Err: err}
				continue
			}
			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					stream <- StreamChunk{Type: ChunkToken, Content: choice.Delta.Content}
				}
				if choice.FinishReason == "stop" {
					stream <- StreamChunk{Type: ChunkDone}
					return
				}
			}
		}

		if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
			stream <- StreamChunk{Type: ChunkError, Err: err}
		} else {
			stream <- StreamChunk{Type: ChunkDone}
		}
	}()

	return stream, nil
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequestPayload struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Messages []openAIMessage `json:"messages"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func buildOpenAIRequest(req ChatRequest) ([]byte, error) {
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
	payload := openAIRequestPayload{
		Model:    req.Model,
		Stream:   true,
		Messages: messages,
	}
	return json.Marshal(payload)
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
