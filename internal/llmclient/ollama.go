package llmclient

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/higgscli/higgs/internal/ollama"
)

// ollamaClient adapts the native Ollama API (internal/ollama) to Client.
// Thinking and MaxTokens are ignored: requests keep the exact shape higgs has
// always sent to Ollama.
type ollamaClient struct {
	baseURL string
	model   string
}

func (c *ollamaClient) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}
	var format json.RawMessage
	switch {
	case req.Schema != nil:
		b, err := json.Marshal(req.Schema)
		if err != nil {
			return ChatResponse{}, fmt.Errorf("marshal format schema: %w", err)
		}
		format = b
	case req.ForceJSON:
		format = json.RawMessage(`"json"`)
	}
	msgs := make([]ollama.ChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = ollama.ChatMessage{Role: m.Role, Content: m.Content}
	}
	content, err := ollama.Chat(ctx, c.baseURL, model, msgs, format)
	if err != nil {
		return ChatResponse{}, err
	}
	if stripped := stripThink(content); stripped != "" {
		content = stripped
	}
	return ChatResponse{Content: content}, nil
}
