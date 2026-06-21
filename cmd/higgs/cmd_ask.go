package main

import (
	"context"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/agent"
	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/termio"
)

// askRunFn is the agent entry point, overridable by tests to avoid
// requiring a live LLM in unit tests.
var askRunFn = agent.Run

func newAskCmd() *cobra.Command {
	var (
		maxSteps    int
		userContext string
		trace       bool
		model       string
		baseURL     string
	)
	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Agentic Q&A over your mailbox using read-only tools",
		Long: `ask plans a minimal sequence of read-only higgs tool calls (search,
fetch-and-parse, scan-folders, summarize, digest, thread(s), attachments,
state), executes them, and synthesizes a grounded answer with citations.

Without --trace the command prints a single JSON object ({answer,
citations, steps_taken}).

With --trace the command switches to NDJSON: one {"type":"step",...}
event per invoked tool followed by a final {"type":"answer", ...}
object.`,
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,3,4,6,9",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdAsk(args[0], askOptions{
				maxSteps:    maxSteps,
				userContext: userContext,
				trace:       trace,
				model:       model,
				baseURL:     baseURL,
			})
		},
	}
	cmd.Flags().IntVar(&maxSteps, "max-steps", 5, "Maximum tool invocations the agent may perform")
	cmd.Flags().StringVar(&userContext, "user-context", "", "Optional extra context appended to the planner prompt")
	cmd.Flags().BoolVar(&trace, "trace", false, "Emit NDJSON per-step events followed by a final answer event")
	cmd.Flags().StringVar(&model, "model", "", "Override Ollama model (defaults to PM_OLLAMA_MODEL or gemma4)")
	cmd.Flags().StringVar(&baseURL, "ollama-base-url", "", "Override Ollama base URL (defaults to PM_OLLAMA_BASE_URL or http://localhost:11434)")
	return cmd
}

type askOptions struct {
	maxSteps    int
	userContext string
	trace       bool
	model       string
	baseURL     string
}

func cmdAsk(question string, opts askOptions) error {
	binPath, err := os.Executable()
	if err != nil {
		return cerr.Internal(err, "resolve binary path")
	}
	model := opts.model
	if model == "" {
		model = askEnvDefault("PM_OLLAMA_MODEL", "gemma4")
	}
	baseURL := opts.baseURL
	if baseURL == "" {
		baseURL = askEnvDefault("PM_OLLAMA_BASE_URL", "http://localhost:11434")
	}
	w := termio.Default()
	runOpts := agent.Options{
		BinPath:     binPath,
		BaseURL:     baseURL,
		Model:       model,
		MaxSteps:    opts.maxSteps,
		UserContext: opts.userContext,
		Trace:       opts.trace,
	}
	ans, err := askRunFn(context.Background(), question, runOpts, w.Out)
	if err != nil {
		return cerr.Internal(err, "agent run")
	}
	if opts.trace {
		// When tracing, the per-step events have already been streamed
		// to stdout by agent.Run; emit the final answer envelope as a
		// matching NDJSON line.
		return w.PrintNDJSON(map[string]any{
			"type":        "answer",
			"answer":      ans.Answer,
			"citations":   ans.Citations,
			"steps_taken": ans.StepsTaken,
		})
	}
	return w.PrintJSON(ans)
}

func askEnvDefault(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}
