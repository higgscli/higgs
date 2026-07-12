package llm

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/llmclient"
)

//go:embed schemas/*.json
var Presets embed.FS

const extractSystemPrompt = `You extract structured data from a single email.
Output JSON matching the provided schema exactly. Use empty strings or empty
arrays for fields that are not present in the email. Never invent values.`

// Preset loads a named JSON schema from the embedded schemas directory. Valid
// names: "invoice", "shipping", "meeting". Returns a decoded map suitable for
// passing to Extract.
func Preset(name string) (map[string]any, error) {
	path := "schemas/" + name + ".json"
	b, err := Presets.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("preset %q: %w", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("preset %q decode: %w", name, err)
	}
	return out, nil
}

// Extract runs the model against a single message with a caller-supplied JSON
// schema. The decoded result is returned as a generic map.
func Extract(ctx context.Context, c llmclient.Client, model string, m Message, schema map[string]any) (map[string]any, error) {
	userPrompt := buildUserPrompt(m, defaultMaxInputBytes, "")
	messages := []llmclient.ChatMessage{
		{Role: "system", Content: extractSystemPrompt},
		{Role: "user", Content: userPrompt},
	}
	var out map[string]any
	req := llmclient.ChatRequest{Model: model, Messages: messages, Schema: schema}
	if err := llmclient.ChatJSON(ctx, c, req, &out); err != nil {
		return nil, cerr.Classify(err, "extract")
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
