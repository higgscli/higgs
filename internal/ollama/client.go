// Package ollama calls the Ollama REST API for chat with structured (JSON schema) output.
// Request/response shape aligns with the official client: https://github.com/ollama/ollama/blob/main/api/client.go
// and types: https://github.com/ollama/ollama/blob/main/api/types.go (ChatRequest, ChatResponse, Message).
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ChatRequest is the request body for POST /api/chat. Matches ollama/api types.ChatRequest:
// Model, Messages, Stream (pointer for omitempty), Format (json.RawMessage for schema or "json").
type ChatRequest struct {
	Model    string          `json:"model"`
	Messages []ChatMessage   `json:"messages"`
	Stream   *bool           `json:"stream,omitempty"`
	Format   json.RawMessage `json:"format,omitempty"`
}

// ChatMessage is a single message in a chat sequence. Matches ollama/api types.Message (Role, Content).
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse is the response from /api/chat when stream is false. Matches ollama/api types.ChatResponse:
// Message (with Role, Content), Done, DoneReason, etc.
type ChatResponse struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason,omitempty"`
}

// Chat sends a non-streaming chat request and returns the assistant message
// content. format is passed through as the "format" field (a JSON schema or
// the literal "json"); nil omits it for free-text output.
func Chat(ctx context.Context, baseURL, model string, messages []ChatMessage, format json.RawMessage) (string, error) {
	streamFalse := false
	reqBody := ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   &streamFalse,
		Format:   format,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	u.Path = "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "higgs/1.0")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	content := chatResp.Message.Content
	if content == "" {
		return "", fmt.Errorf("empty response content")
	}
	return content, nil
}

// ChatWithSchema sends a chat request with a JSON schema for structured output.
// schema is marshalled to JSON and sent as the "format" field (per Ollama API: format can be "json" or a JSON schema).
// Stream is set to false so the server returns a single JSON response; the official client uses streaming by default.
// The response message content is parsed into out (must be a pointer to a struct).
func ChatWithSchema(ctx context.Context, baseURL, model string, messages []ChatMessage, schema interface{}, out interface{}) error {
	formatBytes, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("marshal format schema: %w", err)
	}
	content, err := Chat(ctx, baseURL, model, messages, formatBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("parse JSON output: %w (raw: %q)", err, content)
	}
	return nil
}
