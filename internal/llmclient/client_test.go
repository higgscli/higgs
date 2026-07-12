package llmclient

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PM_LLM_BACKEND",
		"PM_OLLAMA_BASE_URL", "PM_OLLAMA_MODEL",
		"PM_OPENAI_BASE_URL", "PM_OPENAI_API_KEY", "PM_OPENAI_MODEL",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadFromEnv_Defaults(t *testing.T) {
	clearLLMEnv(t)
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.Backend != BackendOllama {
		t.Errorf("backend=%q want ollama", cfg.Backend)
	}
	if cfg.OllamaBaseURL != "http://localhost:11434" {
		t.Errorf("ollama base=%q", cfg.OllamaBaseURL)
	}
	if cfg.OllamaModel != "gemma4" {
		t.Errorf("ollama model=%q", cfg.OllamaModel)
	}
}

func TestLoadFromEnv_OpenAI(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("PM_LLM_BACKEND", "openai")
	t.Setenv("PM_OPENAI_BASE_URL", "http://10.1.1.8:8080/")
	t.Setenv("PM_OPENAI_MODEL", "qwen3.6-35b-a3b")
	t.Setenv("PM_OPENAI_API_KEY", "k")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.Backend != BackendOpenAI {
		t.Errorf("backend=%q", cfg.Backend)
	}
	if cfg.OpenAIBaseURL != "http://10.1.1.8:8080" {
		t.Errorf("trailing slash not trimmed: %q", cfg.OpenAIBaseURL)
	}
	if cfg.OpenAIModel != "qwen3.6-35b-a3b" || cfg.OpenAIAPIKey != "k" {
		t.Errorf("openai cfg=%+v", cfg)
	}
}

func TestLoadFromEnv_OpenAIRequiresBaseURLAndModel(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("PM_LLM_BACKEND", "openai")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error when PM_OPENAI_BASE_URL missing")
	}
	t.Setenv("PM_OPENAI_BASE_URL", "http://x:8080")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error when PM_OPENAI_MODEL missing")
	}
	t.Setenv("PM_OPENAI_MODEL", "m")
	if _, err := LoadFromEnv(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadFromEnv_InvalidBackend(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("PM_LLM_BACKEND", "watson")
	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	if !strings.Contains(err.Error(), "PM_LLM_BACKEND") {
		t.Errorf("error should name the env var: %v", err)
	}
}

func TestLoadFromEnv_BackendCaseInsensitive(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("PM_LLM_BACKEND", "Ollama")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.Backend != BackendOllama {
		t.Errorf("backend=%q", cfg.Backend)
	}
}

func TestNew_SelectsBackend(t *testing.T) {
	ollamaC, err := New(Config{Backend: BackendOllama, OllamaBaseURL: "http://x", OllamaModel: "m"})
	if err != nil || ollamaC == nil {
		t.Fatalf("New ollama: %v", err)
	}
	openaiC, err := New(Config{Backend: BackendOpenAI, OpenAIBaseURL: "http://x", OpenAIModel: "m"})
	if err != nil || openaiC == nil {
		t.Fatalf("New openai: %v", err)
	}
	if _, err := New(Config{Backend: "nope"}); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

// staticClient returns a canned response for ChatJSON tests.
type staticClient struct {
	content string
	lastReq ChatRequest
}

func (s *staticClient) Chat(_ context.Context, req ChatRequest) (ChatResponse, error) {
	s.lastReq = req
	return ChatResponse{Content: s.content}, nil
}

func TestChatJSON_Decodes(t *testing.T) {
	c := &staticClient{content: `{"answer":"42"}`}
	var out struct {
		Answer string `json:"answer"`
	}
	req := ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "q"}},
		Schema:   map[string]any{"type": "object"},
	}
	if err := ChatJSON(context.Background(), c, req, &out); err != nil {
		t.Fatalf("ChatJSON: %v", err)
	}
	if out.Answer != "42" {
		t.Errorf("answer=%q", out.Answer)
	}
}

func TestChatJSON_ForcesJSONWithoutSchema(t *testing.T) {
	c := &staticClient{content: `{}`}
	var out map[string]any
	req := ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "q"}}}
	if err := ChatJSON(context.Background(), c, req, &out); err != nil {
		t.Fatalf("ChatJSON: %v", err)
	}
	if !c.lastReq.ForceJSON {
		t.Error("ChatJSON should set ForceJSON when no schema supplied")
	}
}

func TestChatJSON_BadJSON(t *testing.T) {
	c := &staticClient{content: "not json"}
	var out map[string]any
	err := ChatJSON(context.Background(), c, ChatRequest{Schema: json.RawMessage(`{}`)}, &out)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse JSON output") {
		t.Errorf("error=%v", err)
	}
}
