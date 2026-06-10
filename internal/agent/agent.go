package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/akeemjenkins/protoncli/internal/ollama"
)

// Options configures a Run invocation.
type Options struct {
	// BinPath is the path to the protoncli binary used for subprocess tool
	// calls. Tests may substitute a small shell script.
	BinPath string
	// BaseURL is the Ollama API endpoint.
	BaseURL string
	// Model is the Ollama model name.
	Model string
	// MaxSteps caps how many tool invocations the loop will execute. If
	// <= 0, defaults to 5.
	MaxSteps int
	// AllowedTools is the whitelist of subcommand names the agent may
	// invoke. Nil/empty means DefaultAllowedTools.
	AllowedTools []string
	// UserContext is optional free-form extra context appended to the
	// planner's system prompt (e.g. "timezone is America/Los_Angeles").
	UserContext string
	// Trace, if true, emits per-step NDJSON events via the writer passed
	// to Run before returning the final Answer.
	Trace bool
	// PlanBudget caps total wall time for planning + execution. If <= 0,
	// defaults to 2 minutes.
	PlanBudget time.Duration
	// ChatFn allows tests to substitute the LLM call. When nil, the
	// package default (ollama.ChatWithSchema) is used.
	ChatFn ChatFunc
}

// ChatFunc mirrors the ollama.ChatWithSchema signature for injection.
type ChatFunc func(ctx context.Context, baseURL, model string, messages []ollama.ChatMessage, schema interface{}, out interface{}) error

// Step is one planned tool invocation.
type Step struct {
	Tool string   `json:"tool"`
	Args []string `json:"args"`
	Why  string   `json:"why,omitempty"`
}

// Plan is what the planner LLM returns.
type Plan struct {
	Steps []Step `json:"steps"`
}

// Citation references a message the final answer relies on.
type Citation struct {
	UID     uint32 `json:"uid,omitempty"`
	Subject string `json:"subject,omitempty"`
	Mailbox string `json:"mailbox,omitempty"`
}

// Answer is the final synthesized answer.
type Answer struct {
	Answer     string     `json:"answer"`
	Citations  []Citation `json:"citations"`
	StepsTaken int        `json:"steps_taken"`
}

// TraceEvent is an NDJSON per-step event written when opts.Trace is true.
type TraceEvent struct {
	Type       string   `json:"type"`
	Tool       string   `json:"tool"`
	Args       []string `json:"args"`
	Status     string   `json:"status"`
	DurationMs int64    `json:"duration_ms"`
	ExitCode   int      `json:"exit_code"`
	Error      string   `json:"error,omitempty"`
}

// planSchema is the JSON Schema the planner LLM is asked to conform to.
var planSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"steps": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool": map[string]any{"type": "string"},
					"args": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"why": map[string]any{"type": "string"},
				},
				"required": []string{"tool", "args"},
			},
		},
	},
	"required": []string{"steps"},
}

// answerSchema is the JSON Schema the answer synthesizer LLM returns.
var answerSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"answer": map[string]any{"type": "string"},
		"citations": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"uid":     map[string]any{"type": "integer"},
					"subject": map[string]any{"type": "string"},
					"mailbox": map[string]any{"type": "string"},
				},
			},
		},
	},
	"required": []string{"answer"},
}

// Run executes the plan → invoke → observe → answer loop once and
// returns the synthesized Answer. Trace events (when opts.Trace) are
// written to w as NDJSON.
func Run(ctx context.Context, question string, opts Options, w io.Writer) (Answer, error) {
	if question == "" {
		return Answer{}, errors.New("question required")
	}
	if opts.BinPath == "" {
		return Answer{}, errors.New("BinPath required")
	}
	if opts.BaseURL == "" {
		return Answer{}, errors.New("BaseURL required")
	}
	if opts.Model == "" {
		return Answer{}, errors.New("model required")
	}
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 5
	}
	allow := opts.AllowedTools
	if len(allow) == 0 {
		allow = DefaultAllowedTools
	}
	budget := opts.PlanBudget
	if budget <= 0 {
		budget = 2 * time.Minute
	}
	chat := opts.ChatFn
	if chat == nil {
		chat = ollama.ChatWithSchema
	}

	budgetCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	tools, err := DiscoverTools(opts.BinPath, allow)
	if err != nil {
		return Answer{}, fmt.Errorf("discover tools: %w", err)
	}

	plan, err := planStep(budgetCtx, question, tools, opts, chat)
	if err != nil {
		return Answer{}, err
	}

	observations := make([]stepObservation, 0, maxSteps)
	for i, step := range plan.Steps {
		if i >= maxSteps {
			break
		}
		if !ToolAllowed(step.Tool, allow) {
			obs := stepObservation{
				Step:   step,
				Status: "rejected",
				Error:  fmt.Sprintf("tool %q not in allow list", step.Tool),
			}
			observations = append(observations, obs)
			emitTrace(w, opts.Trace, obs)
			continue
		}
		inv := Invocation{Tool: step.Tool, Args: step.Args}
		obsRaw, invErr := Invoke(budgetCtx, opts.BinPath, inv)
		obs := stepObservation{
			Step:       step,
			ExitCode:   obsRaw.ExitCode,
			Stdout:     obsRaw.Stdout,
			Stderr:     obsRaw.Stderr,
			DurationMs: obsRaw.Duration.Milliseconds(),
		}
		switch {
		case invErr != nil:
			obs.Status = "error"
			obs.Error = invErr.Error()
		case obsRaw.ExitCode == 0:
			obs.Status = "ok"
		default:
			obs.Status = "nonzero_exit"
		}
		observations = append(observations, obs)
		emitTrace(w, opts.Trace, obs)
	}

	return synthesize(budgetCtx, question, observations, opts, chat)
}

type stepObservation struct {
	Step       Step
	Status     string
	ExitCode   int
	Stdout     string
	Stderr     string
	DurationMs int64
	Error      string
}

func emitTrace(w io.Writer, trace bool, obs stepObservation) {
	if !trace || w == nil {
		return
	}
	ev := TraceEvent{
		Type:       "step",
		Tool:       obs.Step.Tool,
		Args:       obs.Step.Args,
		Status:     obs.Status,
		DurationMs: obs.DurationMs,
		ExitCode:   obs.ExitCode,
		Error:      obs.Error,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = w.Write(b)
	_, _ = w.Write([]byte{'\n'})
}

// planStep asks the planner LLM to produce a Plan. Invalid JSON triggers
// exactly one retry with a stricter reminder; after a second failure the
// error is returned.
func planStep(ctx context.Context, question string, tools []Tool, opts Options, chat ChatFunc) (Plan, error) {
	toolsJSON, _ := json.Marshal(tools)
	sys := "You are a read-only email inspection agent. Plan a minimal sequence of tool calls " +
		"to answer the user's question. Respond with a JSON object {\"steps\": [{\"tool\":..., \"args\":[...], \"why\":...}]}. " +
		"Use ONLY tools listed below. Prefer narrow searches. Tools:\n" + string(toolsJSON)
	if opts.UserContext != "" {
		sys += "\nUser context: " + opts.UserContext
	}
	msgs := []ollama.ChatMessage{
		{Role: "system", Content: sys},
		{Role: "user", Content: question},
	}

	var plan Plan
	err := chat(ctx, opts.BaseURL, opts.Model, msgs, planSchema, &plan)
	if err == nil {
		return plan, nil
	}
	// Retry once with a stricter reminder.
	msgs = append(msgs, ollama.ChatMessage{
		Role:    "system",
		Content: "Your previous response was not valid JSON per the schema. Return ONLY a JSON object matching {\"steps\":[{\"tool\":string,\"args\":[string],\"why\":string}]}.",
	})
	if err2 := chat(ctx, opts.BaseURL, opts.Model, msgs, planSchema, &plan); err2 != nil {
		return Plan{}, fmt.Errorf("plan failed after retry: %w", err2)
	}
	return plan, nil
}

// synthesize asks the LLM to produce a grounded final Answer given the
// recorded observations. The observations are passed truncated to bound
// prompt size.
func synthesize(ctx context.Context, question string, observations []stepObservation, opts Options, chat ChatFunc) (Answer, error) {
	type obsPayload struct {
		Tool     string `json:"tool"`
		Args     []string `json:"args"`
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr,omitempty"`
	}
	payload := make([]obsPayload, 0, len(observations))
	const maxBytes = 16 * 1024
	for _, o := range observations {
		p := obsPayload{
			Tool:     o.Step.Tool,
			Args:     o.Step.Args,
			Status:   o.Status,
			ExitCode: o.ExitCode,
			Stdout:   truncate(o.Stdout, maxBytes),
		}
		if o.Stderr != "" {
			p.Stderr = truncate(o.Stderr, 1024)
		}
		payload = append(payload, p)
	}
	obsJSON, _ := json.Marshal(payload)
	sys := "You are an assistant that grounds answers in tool observations. " +
		"Given the user's question and the NDJSON tool outputs below, produce a concise answer " +
		"and citations (uid, subject, mailbox) drawn only from the observations. If the data is insufficient, say so."
	msgs := []ollama.ChatMessage{
		{Role: "system", Content: sys},
		{Role: "user", Content: "Question: " + question + "\nObservations: " + string(obsJSON)},
	}
	var ans Answer
	if err := chat(ctx, opts.BaseURL, opts.Model, msgs, answerSchema, &ans); err != nil {
		return Answer{}, fmt.Errorf("synthesize answer: %w", err)
	}
	if ans.Citations == nil {
		ans.Citations = []Citation{}
	}
	ans.StepsTaken = len(observations)
	return ans, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]"
}
