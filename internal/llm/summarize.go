package llm

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/ollama"
)

// Summary is the structured output of the summarize verb.
type Summary struct {
	TLDR             string   `json:"tldr"`
	Bullets          []string `json:"bullets"`
	IsActionRequired bool     `json:"is_action_required"`
	DueDate          string   `json:"due_date,omitempty"`
}

// SummarizeOpts are per-call options for Summarize / SummarizeThread.
type SummarizeOpts struct {
	// MaxBulletCount caps the number of bullets the model is asked to
	// produce. 0 defaults to 5.
	MaxBulletCount int
	// UserContext is appended to the user prompt verbatim.
	UserContext string
	// MaxInput caps the per-message body bytes sent to the model.
	// 0 defaults to 8000.
	MaxInput int
}

const defaultMaxInputBytes = 8000
const defaultMaxBullets = 5

const summarizeSystemPrompt = `You are an email assistant that produces compact, accurate summaries.

Produce JSON matching this shape:
{
  "tldr": short one-sentence summary of the message,
  "bullets": 1-5 short bullets with the key points,
  "is_action_required": true if the sender expects a reply, click, or task,
  "due_date": RFC3339 date if a deadline is mentioned, otherwise empty string
}

Rules:
- Never hallucinate facts not present in the email.
- Bullets must be full sentences, concise (under 140 chars each).
- If no deadline is present, set due_date to "".`

const summarizeThreadSystemPrompt = `You summarize an ordered email thread (oldest first, separated by ---).
Produce JSON matching this shape:
{
  "tldr": one-sentence summary of the whole thread capturing the final state,
  "bullets": 1-5 short bullets with the key points across messages (note continuity),
  "is_action_required": true if the most recent message expects a reply or action,
  "due_date": RFC3339 deadline if mentioned, otherwise empty string
}

Rules:
- Reason across all messages; the most recent message drives is_action_required.
- Use full sentences in bullets, under 140 chars each.
- Never invent facts not present in the thread.`

// summarySchema is the JSON schema used for Ollama structured output.
var summarySchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"tldr": map[string]interface{}{"type": "string"},
		"bullets": map[string]interface{}{
			"type":  "array",
			"items": map[string]interface{}{"type": "string"},
		},
		"is_action_required": map[string]interface{}{"type": "boolean"},
		"due_date":           map[string]interface{}{"type": "string"},
	},
	"required": []string{"tldr", "bullets", "is_action_required"},
}

// Summarize produces a structured summary for a single message.
func Summarize(ctx context.Context, baseURL, model string, m Message, opts SummarizeOpts) (Summary, error) {
	maxIn := opts.MaxInput
	if maxIn <= 0 {
		maxIn = defaultMaxInputBytes
	}
	bullets := opts.MaxBulletCount
	if bullets <= 0 {
		bullets = defaultMaxBullets
	}
	userPrompt := buildUserPrompt(m, maxIn, opts.UserContext) +
		"\n\nProduce at most " + itoa(bullets) + " bullets."
	messages := []ollama.ChatMessage{
		{Role: "system", Content: summarizeSystemPrompt},
		{Role: "user", Content: userPrompt},
	}
	var out Summary
	if err := ollama.ChatWithSchema(ctx, baseURL, model, messages, summarySchema, &out); err != nil {
		return Summary{}, cerr.Classify(err, "summarize")
	}
	out.DueDate = strings.TrimSpace(out.DueDate)
	if out.Bullets == nil {
		out.Bullets = []string{}
	}
	return out, nil
}

// SummarizeThread produces a single Summary over a chronologically ordered
// list of messages. Messages are sorted ascending by parsed Date (ties resolved
// by input order) and concatenated with --- separators.
func SummarizeThread(ctx context.Context, baseURL, model string, msgs []Message, opts SummarizeOpts) (Summary, error) {
	if len(msgs) == 0 {
		return Summary{Bullets: []string{}}, nil
	}
	sorted := make([]Message, len(msgs))
	copy(sorted, msgs)
	sort.SliceStable(sorted, func(i, j int) bool {
		ti := parseMaybeRFC3339(sorted[i].Date)
		tj := parseMaybeRFC3339(sorted[j].Date)
		return ti.Before(tj)
	})
	maxIn := opts.MaxInput
	if maxIn <= 0 {
		maxIn = defaultMaxInputBytes
	}
	bullets := opts.MaxBulletCount
	if bullets <= 0 {
		bullets = defaultMaxBullets
	}
	var b strings.Builder
	for i, m := range sorted {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString(buildUserPrompt(m, maxIn, ""))
	}
	userPrompt := b.String()
	if strings.TrimSpace(opts.UserContext) != "" {
		userPrompt += "\n\nAdditional context from the user:\n" + opts.UserContext
	}
	userPrompt += "\n\nProduce at most " + itoa(bullets) + " bullets."

	messages := []ollama.ChatMessage{
		{Role: "system", Content: summarizeThreadSystemPrompt},
		{Role: "user", Content: userPrompt},
	}
	var out Summary
	if err := ollama.ChatWithSchema(ctx, baseURL, model, messages, summarySchema, &out); err != nil {
		return Summary{}, cerr.Classify(err, "summarize thread")
	}
	out.DueDate = strings.TrimSpace(out.DueDate)
	if out.Bullets == nil {
		out.Bullets = []string{}
	}
	return out, nil
}

func parseMaybeRFC3339(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC1123Z, s); err == nil {
		return t
	}
	return time.Time{}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
