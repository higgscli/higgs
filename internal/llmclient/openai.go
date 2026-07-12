package llmclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// openaiClient speaks the OpenAI Chat Completions API
// (POST {base}/v1/chat/completions), as implemented by llama.cpp llama-server
// and other self-hosted OpenAI-compatible servers.
type openaiClient struct {
	baseURL string
	apiKey  string
	model   string
	httpc   *http.Client
	// backoff between retry attempts; shortened in tests.
	backoff time.Duration
}

func newOpenAIClient(baseURL, apiKey, model string) *openaiClient {
	return &openaiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		httpc:   &http.Client{Timeout: 120 * time.Second},
		backoff: 250 * time.Millisecond,
	}
}

// Defaults for max_tokens. Generous on purpose: reasoning models spend tokens
// on chain-of-thought before emitting content, and a tight cap yields an
// empty message.content.
const (
	defaultMaxTokens         = 4096
	defaultMaxTokensThinking = 16384
	maxAttempts              = 3
)

type openaiChatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *openaiClient) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		if req.Thinking {
			maxTokens = defaultMaxTokensThinking
		} else {
			maxTokens = defaultMaxTokens
		}
	}

	body := map[string]any{
		"model":       model,
		"messages":    req.Messages,
		"temperature": 0,
		"max_tokens":  maxTokens,
		"stream":      false,
	}
	if !req.Thinking {
		// Suppress chain-of-thought (Qwen3-style reasoning models) so
		// structured calls return content directly instead of burning the
		// token budget on reasoning_content.
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	}
	switch {
	case req.Schema != nil:
		body["response_format"] = map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"schema": req.Schema},
		}
	case req.ForceJSON:
		body["response_format"] = map[string]any{"type": "json_object"}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.doWithRetry(ctx, payload)
	if err != nil {
		return ChatResponse{}, err
	}

	if len(resp.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("openai backend returned no choices")
	}
	choice := resp.Choices[0]
	content := stripThink(choice.Message.Content)
	if strings.TrimSpace(content) == "" {
		// Reasoning models can leave content empty and put everything in
		// reasoning_content (e.g. when the token budget ran out mid-think).
		content = stripThink(choice.Message.ReasoningContent)
	}
	if strings.TrimSpace(content) == "" {
		return ChatResponse{}, fmt.Errorf("empty response content (finish_reason=%s); the model may need a larger max_tokens", choice.FinishReason)
	}
	return ChatResponse{Content: content}, nil
}

// doWithRetry POSTs payload, retrying transport errors and 5xx responses up
// to maxAttempts total attempts. llama-server closes idle sockets, so a
// connection reset on the first attempt is benign and worth one more try.
func (c *openaiClient) doWithRetry(ctx context.Context, payload []byte) (*openaiChatResponse, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * c.backoff):
			}
		}
		resp, retryable, err := c.doOnce(ctx, payload)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable || ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *openaiClient) doOnce(ctx context.Context, payload []byte) (resp *openaiChatResponse, retryable bool, err error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, false, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "higgs/1.0")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	httpResp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, true, fmt.Errorf("request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(httpResp.Body, 512))
		err := fmt.Errorf("openai backend returned status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(snippet)))
		return nil, httpResp.StatusCode >= 500, err
	}

	var out openaiChatResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&out); err != nil {
		return nil, false, fmt.Errorf("decode response: %w", err)
	}
	return &out, false, nil
}
