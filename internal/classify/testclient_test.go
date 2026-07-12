package classify

import (
	"testing"

	"github.com/higgscli/higgs/internal/llmclient"
)

// tc builds an Ollama-backend llmclient pointed at a test server.
func tc(t *testing.T, baseURL string) llmclient.Client {
	t.Helper()
	c, err := llmclient.New(llmclient.Config{
		Backend:       llmclient.BackendOllama,
		OllamaBaseURL: baseURL,
		OllamaModel:   "m",
	})
	if err != nil {
		t.Fatalf("llmclient.New: %v", err)
	}
	return c
}
