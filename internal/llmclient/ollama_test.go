package llmclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newOllamaFixture(t *testing.T, content string) (*httptest.Server, *atomic.Value) {
	t.Helper()
	captured := &atomic.Value{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.Store(string(body) + "\npath=" + r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": content},
			"done":    true,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func newTestOllama(t *testing.T, baseURL string) Client {
	t.Helper()
	c, err := New(Config{Backend: BackendOllama, OllamaBaseURL: baseURL, OllamaModel: "default-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestOllamaBackend_ChatWithSchema(t *testing.T) {
	srv, cap := newOllamaFixture(t, `{"x":1}`)
	c := newTestOllama(t, srv.URL)
	resp, err := c.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "q"}},
		Schema:   map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != `{"x":1}` {
		t.Errorf("content=%q", resp.Content)
	}
	raw, _ := cap.Load().(string)
	if !strings.Contains(raw, "path=/api/chat") {
		t.Errorf("wrong path: %s", raw)
	}
	if !strings.Contains(raw, `"format":{"type":"object"}`) {
		t.Errorf("schema format missing: %s", raw)
	}
	if !strings.Contains(raw, `"model":"default-model"`) {
		t.Errorf("default model missing: %s", raw)
	}
}

func TestOllamaBackend_ModelOverride(t *testing.T) {
	srv, cap := newOllamaFixture(t, "hi")
	c := newTestOllama(t, srv.URL)
	if _, err := c.Chat(context.Background(), ChatRequest{
		Model:    "other",
		Messages: []ChatMessage{{Role: "user", Content: "q"}},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	raw, _ := cap.Load().(string)
	if !strings.Contains(raw, `"model":"other"`) {
		t.Errorf("model override missing: %s", raw)
	}
}

func TestOllamaBackend_NoSchemaOmitsFormat(t *testing.T) {
	srv, cap := newOllamaFixture(t, "plain text")
	c := newTestOllama(t, srv.URL)
	resp, err := c.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "q"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "plain text" {
		t.Errorf("content=%q", resp.Content)
	}
	raw, _ := cap.Load().(string)
	if strings.Contains(raw, `"format"`) {
		t.Errorf("format should be omitted without schema: %s", raw)
	}
}

func TestOllamaBackend_ForceJSONSendsJSONFormat(t *testing.T) {
	srv, cap := newOllamaFixture(t, `{}`)
	c := newTestOllama(t, srv.URL)
	if _, err := c.Chat(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "q"}},
		ForceJSON: true,
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	raw, _ := cap.Load().(string)
	if !strings.Contains(raw, `"format":"json"`) {
		t.Errorf("format json missing: %s", raw)
	}
}

func TestOllamaBackend_StripThinkBlock(t *testing.T) {
	srv, _ := newOllamaFixture(t, "<think>hmm</think>{\"x\":1}")
	c := newTestOllama(t, srv.URL)
	resp, err := c.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "q"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != `{"x":1}` {
		t.Errorf("content=%q want think block stripped", resp.Content)
	}
}
