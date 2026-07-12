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

// openaiFixture spins up a fake OpenAI-compatible server that records the
// last request body and headers and replies with the given handler.
type openaiFixture struct {
	srv      *httptest.Server
	lastBody atomic.Value // string
	lastAuth atomic.Value // string
	lastPath atomic.Value // string
}

func newOpenAIFixture(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *openaiFixture {
	t.Helper()
	f := &openaiFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.lastBody.Store(string(body))
		f.lastAuth.Store(r.Header.Get("Authorization"))
		f.lastPath.Store(r.URL.Path)
		handler(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func openaiOK(content string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": content},
			}},
		})
	}
}

func (f *openaiFixture) body() string {
	s, _ := f.lastBody.Load().(string)
	return s
}

func newTestOpenAI(baseURL string) Client {
	c, err := New(Config{
		Backend:       BackendOpenAI,
		OpenAIBaseURL: baseURL,
		OpenAIModel:   "qwen-test",
	})
	if err != nil {
		panic(err)
	}
	return c
}

func TestOpenAI_RequestShape(t *testing.T) {
	f := newOpenAIFixture(t, openaiOK(`{"ok":true}`))
	c := newTestOpenAI(f.srv.URL)

	schema := map[string]any{"type": "object"}
	resp, err := c.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
		},
		Schema: schema,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != `{"ok":true}` {
		t.Errorf("content=%q", resp.Content)
	}
	if p, _ := f.lastPath.Load().(string); p != "/v1/chat/completions" {
		t.Errorf("path=%q", p)
	}

	var req map[string]any
	if err := json.Unmarshal([]byte(f.body()), &req); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if req["model"] != "qwen-test" {
		t.Errorf("model=%v want default qwen-test", req["model"])
	}
	if req["stream"] != false {
		t.Errorf("stream=%v want false", req["stream"])
	}
	if req["temperature"] != float64(0) {
		t.Errorf("temperature=%v want 0", req["temperature"])
	}
	if mt, ok := req["max_tokens"].(float64); !ok || mt <= 0 {
		t.Errorf("max_tokens=%v want positive default", req["max_tokens"])
	}
	// Thinking not requested → suppressed via chat_template_kwargs.
	kw, ok := req["chat_template_kwargs"].(map[string]any)
	if !ok || kw["enable_thinking"] != false {
		t.Errorf("chat_template_kwargs=%v want enable_thinking:false", req["chat_template_kwargs"])
	}
	rf, ok := req["response_format"].(map[string]any)
	if !ok || rf["type"] != "json_schema" {
		t.Fatalf("response_format=%v want json_schema", req["response_format"])
	}
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("json_schema wrapper missing: %v", rf)
	}
	if _, ok := js["schema"].(map[string]any); !ok {
		t.Errorf("json_schema.schema missing: %v", js)
	}
	if a, _ := f.lastAuth.Load().(string); a != "" {
		t.Errorf("Authorization sent without API key: %q", a)
	}
}

func TestOpenAI_ThinkingOnOmitsSuppression(t *testing.T) {
	f := newOpenAIFixture(t, openaiOK("free text answer"))
	c := newTestOpenAI(f.srv.URL)

	_, err := c.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Thinking: true,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal([]byte(f.body()), &req)
	if _, present := req["chat_template_kwargs"]; present {
		t.Errorf("chat_template_kwargs should be absent when thinking enabled: %s", f.body())
	}
	if _, present := req["response_format"]; present {
		t.Errorf("response_format should be absent without schema: %s", f.body())
	}
}

func TestOpenAI_ForceJSONWithoutSchema(t *testing.T) {
	f := newOpenAIFixture(t, openaiOK(`{}`))
	c := newTestOpenAI(f.srv.URL)

	_, err := c.Chat(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		ForceJSON: true,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal([]byte(f.body()), &req)
	rf, ok := req["response_format"].(map[string]any)
	if !ok || rf["type"] != "json_object" {
		t.Errorf("response_format=%v want json_object", req["response_format"])
	}
}

func TestOpenAI_BearerSentWhenKeySet(t *testing.T) {
	f := newOpenAIFixture(t, openaiOK("x"))
	c, err := New(Config{
		Backend:       BackendOpenAI,
		OpenAIBaseURL: f.srv.URL,
		OpenAIModel:   "m",
		OpenAIAPIKey:  "sekrit",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Chat(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if a, _ := f.lastAuth.Load().(string); a != "Bearer sekrit" {
		t.Errorf("Authorization=%q", a)
	}
}

func TestOpenAI_PerRequestModelOverride(t *testing.T) {
	f := newOpenAIFixture(t, openaiOK("x"))
	c := newTestOpenAI(f.srv.URL)
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "override-model",
		Messages: []ChatMessage{{Role: "user", Content: "q"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(f.body(), `"model":"override-model"`) {
		t.Errorf("model override missing: %s", f.body())
	}
}

func TestOpenAI_MaxTokensOverride(t *testing.T) {
	f := newOpenAIFixture(t, openaiOK("x"))
	c := newTestOpenAI(f.srv.URL)
	_, err := c.Chat(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "q"}},
		MaxTokens: 77,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(f.body(), `"max_tokens":77`) {
		t.Errorf("max_tokens override missing: %s", f.body())
	}
}

func TestOpenAI_ReasoningContentFallback(t *testing.T) {
	f := newOpenAIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message": map[string]any{
					"role":              "assistant",
					"content":           "",
					"reasoning_content": `{"from":"reasoning"}`,
				},
			}},
		})
	})
	c := newTestOpenAI(f.srv.URL)
	resp, err := c.Chat(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != `{"from":"reasoning"}` {
		t.Errorf("content=%q want reasoning_content fallback", resp.Content)
	}
}

func TestOpenAI_StripThinkBlock(t *testing.T) {
	f := newOpenAIFixture(t, openaiOK("<think>pondering...\nmore</think>\n{\"answer\":42}"))
	c := newTestOpenAI(f.srv.URL)
	resp, err := c.Chat(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != `{"answer":42}` {
		t.Errorf("content=%q want think block stripped", resp.Content)
	}
}

func TestOpenAI_EmptyContentError(t *testing.T) {
	f := newOpenAIFixture(t, openaiOK("<think>only thoughts, never an answer"))
	c := newTestOpenAI(f.srv.URL)
	_, err := c.Chat(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}})
	if err == nil {
		t.Fatal("expected empty-content error")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("want empty response error, got: %v", err)
	}
}

func TestOpenAI_RetryOn5xx(t *testing.T) {
	var calls int32
	f := newOpenAIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		openaiOK("recovered")(w, r)
	})
	c := newTestOpenAI(f.srv.URL)
	resp, err := c.Chat(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatalf("Chat after retry: %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("content=%q", resp.Content)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("calls=%d want 2", calls)
	}
}

func TestOpenAI_RetryOnConnectionClose(t *testing.T) {
	var calls int32
	f := newOpenAIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				panic("no hijack support")
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		openaiOK("recovered")(w, r)
	})
	c := newTestOpenAI(f.srv.URL)
	resp, err := c.Chat(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatalf("Chat after connection reset: %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("content=%q", resp.Content)
	}
}

func TestOpenAI_NoRetryOn4xx(t *testing.T) {
	var calls int32
	f := newOpenAIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad model"}}`)
	})
	c := newTestOpenAI(f.srv.URL)
	_, err := c.Chat(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("want status in error, got: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("calls=%d want 1 (no retry on 4xx)", calls)
	}
}

func TestOpenAI_ExhaustedRetriesSurfacesError(t *testing.T) {
	var calls int32
	f := newOpenAIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := newTestOpenAI(f.srv.URL)
	_, err := c.Chat(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}})
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("want status in error, got: %v", err)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("calls=%d want 3 (initial + 2 retries)", calls)
	}
}

func TestOpenAI_ContextCancelStopsRetry(t *testing.T) {
	f := newOpenAIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := newTestOpenAI(f.srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Chat(ctx, ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}})
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestOpenAI_NoChoicesError(t *testing.T) {
	f := newOpenAIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[]}`)
	})
	c := newTestOpenAI(f.srv.URL)
	_, err := c.Chat(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}})
	if err == nil {
		t.Fatal("expected error on empty choices")
	}
}
