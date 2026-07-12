// Package llmclient abstracts the chat LLM backend behind a single interface.
// Two backends are provided: the Ollama native API (default) and any
// OpenAI-compatible Chat Completions server (e.g. a self-hosted llama.cpp
// llama-server). The backend is selected via PM_LLM_BACKEND.
package llmclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Backend identifies which chat API implementation to use.
type Backend string

const (
	BackendOllama Backend = "ollama"
	BackendOpenAI Backend = "openai"
)

// ChatMessage is one message in a chat sequence.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is a backend-agnostic chat call.
type ChatRequest struct {
	// Model overrides the client's configured default model when non-empty.
	Model string
	// Messages is the ordered chat transcript (system first by convention).
	Messages []ChatMessage
	// Schema, when non-nil, requests structured output conforming to this
	// JSON schema (Ollama "format", OpenAI response_format json_schema).
	Schema any
	// ForceJSON requests JSON output without a specific schema. Ignored when
	// Schema is set. (OpenAI response_format json_object, Ollama format "json".)
	ForceJSON bool
	// Thinking enables model reasoning for backends/models that support it.
	// When false, reasoning is explicitly suppressed on the OpenAI backend
	// (chat_template_kwargs enable_thinking:false) so structured calls stay
	// fast and deterministic. The Ollama backend ignores this field.
	Thinking bool
	// MaxTokens caps the completion length (OpenAI backend only). 0 uses a
	// generous default so reasoning models don't truncate mid-thought.
	MaxTokens int
}

// ChatResponse is the assistant's reply with any reasoning markup removed.
type ChatResponse struct {
	Content string
}

// Client is the single interface every model consumer goes through.
type Client interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// Config holds the settings for both backends; Backend picks the active one.
type Config struct {
	Backend       Backend
	OllamaBaseURL string
	OllamaModel   string
	OpenAIBaseURL string
	OpenAIAPIKey  string
	OpenAIModel   string
}

// LoadFromEnv reads backend selection and per-backend settings from the
// environment. PM_LLM_BACKEND defaults to "ollama"; the openai backend
// requires PM_OPENAI_BASE_URL and PM_OPENAI_MODEL.
func LoadFromEnv() (Config, error) {
	backend := Backend(strings.ToLower(envDefault("PM_LLM_BACKEND", string(BackendOllama))))
	cfg := Config{
		Backend:       backend,
		OllamaBaseURL: envDefault("PM_OLLAMA_BASE_URL", "http://localhost:11434"),
		OllamaModel:   envDefault("PM_OLLAMA_MODEL", "gemma4"),
		OpenAIBaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("PM_OPENAI_BASE_URL")), "/"),
		OpenAIAPIKey:  strings.TrimSpace(os.Getenv("PM_OPENAI_API_KEY")),
		OpenAIModel:   strings.TrimSpace(os.Getenv("PM_OPENAI_MODEL")),
	}
	switch backend {
	case BackendOllama:
	case BackendOpenAI:
		if cfg.OpenAIBaseURL == "" {
			return Config{}, fmt.Errorf("PM_LLM_BACKEND=openai requires PM_OPENAI_BASE_URL (e.g. http://localhost:8080)")
		}
		if cfg.OpenAIModel == "" {
			return Config{}, fmt.Errorf("PM_LLM_BACKEND=openai requires PM_OPENAI_MODEL (the model name/alias the server expects)")
		}
	default:
		return Config{}, fmt.Errorf("PM_LLM_BACKEND must be %q or %q (got %q)", BackendOllama, BackendOpenAI, backend)
	}
	return cfg, nil
}

// New builds the Client selected by cfg.Backend.
func New(cfg Config) (Client, error) {
	switch cfg.Backend {
	case BackendOllama:
		return &ollamaClient{baseURL: cfg.OllamaBaseURL, model: cfg.OllamaModel}, nil
	case BackendOpenAI:
		if cfg.OpenAIBaseURL == "" || cfg.OpenAIModel == "" {
			return nil, fmt.Errorf("openai backend requires a base URL and model")
		}
		return newOpenAIClient(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey, cfg.OpenAIModel), nil
	default:
		return nil, fmt.Errorf("unknown LLM backend %q", cfg.Backend)
	}
}

// ChatJSON runs a chat call and decodes the JSON reply into out. When no
// schema is supplied it still forces JSON-mode output so free-form prose can't
// leak into structured pipelines.
func ChatJSON(ctx context.Context, c Client, req ChatRequest, out any) error {
	if req.Schema == nil {
		req.ForceJSON = true
	}
	resp, err := c.Chat(ctx, req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(resp.Content), out); err != nil {
		return fmt.Errorf("parse JSON output: %w (raw: %q)", err, clip(resp.Content, 512))
	}
	return nil
}

// stripThink removes a leading <think>...</think> block that reasoning models
// emit ahead of the real answer.
func stripThink(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "<think>") {
		return s
	}
	end := strings.Index(t, "</think>")
	if end < 0 {
		// Unterminated think block: the whole reply is reasoning.
		return ""
	}
	return strings.TrimSpace(t[end+len("</think>"):])
}

func envDefault(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
