package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/ollama"
)

// Highlight is one notable message surfaced by the digest.
type Highlight struct {
	UID          uint32 `json:"uid"`
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	IsActionable bool   `json:"is_actionable"`
}

// Digest is the structured output of the digest verb. It summarizes a batch of
// messages grouped by category plus a small set of actionable highlights.
type Digest struct {
	Window     string              `json:"window"`
	Highlights []Highlight         `json:"highlights"`
	ByCategory map[string][]string `json:"by_category"`
	Counts     map[string]int      `json:"counts"`
}

// DigestOpts parameterizes Digest.
type DigestOpts struct {
	UserContext     string
	CanonicalLabels []string
	MaxInput        int
	Window          string
}

const digestSystemPromptBase = `You produce a compact digest of a batch of emails.

Output JSON with this shape:
{
  "window": string describing the time window (copy from the user's prompt),
  "highlights": array of up to 10 notable items, each {"uid": int, "title": string, "summary": string, "is_actionable": bool},
  "by_category": object mapping category name -> array of short item descriptions (may be empty),
  "counts": object mapping category name -> integer count
}

Rules:
- Never invent UIDs: each highlight's uid MUST appear in the input.
- "is_actionable" is true if the message expects a user reply/click/task.
- Keep titles under 80 chars, summaries under 200 chars.`

var digestSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"window": map[string]interface{}{"type": "string"},
		"highlights": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"uid":           map[string]interface{}{"type": "integer"},
					"title":         map[string]interface{}{"type": "string"},
					"summary":       map[string]interface{}{"type": "string"},
					"is_actionable": map[string]interface{}{"type": "boolean"},
				},
				"required": []string{"uid", "title", "summary", "is_actionable"},
			},
		},
		"by_category": map[string]interface{}{
			"type": "object",
			"additionalProperties": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
		},
		"counts": map[string]interface{}{
			"type": "object",
			"additionalProperties": map[string]interface{}{
				"type": "integer",
			},
		},
	},
	"required": []string{"window", "highlights", "by_category", "counts"},
}

// BuildDigest asks the model to synthesize a digest over all provided
// messages in a single call. Callers should cap the input count before
// invoking.
func BuildDigest(ctx context.Context, baseURL, model string, msgs []Message, opts DigestOpts) (Digest, error) {
	system := digestSystemPromptBase
	if len(opts.CanonicalLabels) > 0 {
		system += "\n\nBucket messages using ONLY these canonical labels: " +
			strings.Join(opts.CanonicalLabels, ", ") + "."
	}
	maxIn := opts.MaxInput
	if maxIn <= 0 {
		maxIn = 1500
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Window: %s\nMessage count: %d\n\n", opts.Window, len(msgs))
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		fmt.Fprintf(&b, "uid=%d\n", m.UID)
		b.WriteString(buildUserPrompt(m, maxIn, ""))
	}
	if strings.TrimSpace(opts.UserContext) != "" {
		b.WriteString("\n\nAdditional context from the user:\n")
		b.WriteString(opts.UserContext)
	}

	messages := []ollama.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: b.String()},
	}
	var out Digest
	if err := ollama.ChatWithSchema(ctx, baseURL, model, messages, digestSchema, &out); err != nil {
		return Digest{}, cerr.Classify(err, "digest")
	}
	if out.Highlights == nil {
		out.Highlights = []Highlight{}
	}
	if out.ByCategory == nil {
		out.ByCategory = map[string][]string{}
	}
	if out.Counts == nil {
		out.Counts = map[string]int{}
	}
	if out.Window == "" {
		out.Window = opts.Window
	}
	return out, nil
}
