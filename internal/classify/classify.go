// Package classify runs messages through the configured local LLM backend and
// maps the model output onto the canonical higgs label taxonomy.
package classify

import (
	"context"
	"fmt"

	"github.com/higgscli/higgs/internal/email"
	"github.com/higgscli/higgs/internal/labels"
	"github.com/higgscli/higgs/internal/llmclient"
	"github.com/higgscli/higgs/internal/termio"
)

// Result is the structured classification result from Ollama.
type Result struct {
	SuggestedLabels []string `json:"suggested_labels"`
	Confidence      float64  `json:"confidence"`
	Rationale       string   `json:"rationale"`
	IsMailingList   bool     `json:"is_mailing_list"`
}

// AllowedLabels returns the canonical set of labels the AI must pick from.
// Sourced from the labels package so the taxonomy stays in one place.
func AllowedLabels() []string {
	return labels.Default.Canonical()
}

// ClassificationSchema is the JSON schema for Ollama structured output.
// Enum is sourced from the labels taxonomy at build time.
var ClassificationSchema = buildClassificationSchema()

func buildClassificationSchema() map[string]interface{} {
	canonical := labels.Default.Canonical()
	// Copy so callers can't mutate our internal slice.
	enum := make([]string, len(canonical))
	copy(enum, canonical)
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"suggested_labels": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "string",
					"enum": enum,
				},
				"description": "Labels from the allowed canonical taxonomy",
			},
			"confidence": map[string]interface{}{
				"type":        "number",
				"description": "Confidence 0.0 to 1.0",
			},
			"rationale": map[string]interface{}{
				"type":        "string",
				"description": "Brief reason for the labels",
			},
			"is_mailing_list": map[string]interface{}{
				"type":        "boolean",
				"description": "True if the email is a newsletter, mailing list, or marketing (e.g. contains unsubscribe)",
			},
		},
		"required": []string{"suggested_labels", "confidence", "rationale", "is_mailing_list"},
	}
}

const systemPrompt = `Return ONLY this JSON shape, nothing else:
{"suggested_labels":["<Label>"],"confidence":<0.0-1.0>,"rationale":"<one sentence>","is_mailing_list":<true|false>}

Rules:
1. The KEY name is "suggested_labels". Do NOT rename it to "labels".
2. <Label> is one or two of: Orders, Finance, Newsletters, Promotions, Jobs, Social, Services, Health, Travel, Security, Signups.
3. is_mailing_list = true if the email contains "unsubscribe", "manage preferences", or mailing-list language.
4. Output a single JSON object. No Markdown. No code fences. No prose.`

// Classify runs the configured LLM backend with the email snippet and returns
// the structured result.
func Classify(ctx context.Context, c llmclient.Client, model string, msg *email.Message) (*Result, error) {
	userContent := fmt.Sprintf("Classify this email.\n\nFrom: %s\nSubject: %s\n\nBody snippet:\n%s",
		msg.From, msg.Subject, msg.BodySnippet)

	messages := []llmclient.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}

	var result Result
	req := llmclient.ChatRequest{Model: model, Messages: messages, Schema: ClassificationSchema}
	if err := llmclient.ChatJSON(ctx, c, req, &result); err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}

	// Normalize labels to canonical set
	result.SuggestedLabels = normalizeLabels(result.SuggestedLabels)

	// If is_mailing_list and no Newsletters/Promotions label, add Newsletters
	if result.IsMailingList {
		hasMailingLabel := false
		for _, l := range result.SuggestedLabels {
			if l == "Newsletters" || l == "Promotions" {
				hasMailingLabel = true
				break
			}
		}
		if !hasMailingLabel {
			result.SuggestedLabels = append(result.SuggestedLabels, "Newsletters")
		}
	}

	termio.Info("classified uid=%d subject=%q labels=%v is_mailing_list=%v",
		msg.UID, truncate(msg.Subject, 40), result.SuggestedLabels, result.IsMailingList)
	return &result, nil
}

// normalizeLabels maps AI-generated labels to the canonical label set via the
// labels taxonomy (which knows aliases, "Labels/" prefixes, whitespace, etc.).
func normalizeLabels(in []string) []string {
	return labels.Default.Normalize(in)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
