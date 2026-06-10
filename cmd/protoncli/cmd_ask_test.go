package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/akeemjenkins/protoncli/internal/agent"
	"github.com/akeemjenkins/protoncli/internal/cerr"
)

func TestCmdAskUsageAndFlags(t *testing.T) {
	cmd := newAskCmd()
	if cmd.Use != "ask <question>" {
		t.Errorf("use=%q", cmd.Use)
	}
	if cmd.Annotations["stdout_format"] != "json" {
		t.Errorf("annotation stdout_format=%q", cmd.Annotations["stdout_format"])
	}
	if cmd.Annotations["exit_codes"] != "0,3,4,6,9" {
		t.Errorf("annotation exit_codes=%q", cmd.Annotations["exit_codes"])
	}
	want := []string{"max-steps", "user-context", "trace", "model", "ollama-base-url"}
	for _, name := range want {
		if cmd.Flag(name) == nil {
			t.Errorf("missing flag %q", name)
		}
	}
}

func TestCmdAskArgsRequired(t *testing.T) {
	// Use the ask command directly; args validation should reject zero.
	cmd := newAskCmd()
	cmd.SetArgs([]string{})
	cmd.SetOut(discardWriter{})
	cmd.SetErr(discardWriter{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected arg error")
	}
}

func TestCmdAskEnvDefault(t *testing.T) {
	t.Setenv("PM_OLLAMA_MODEL", "")
	if got := askEnvDefault("PM_OLLAMA_MODEL", "gemma4"); got != "gemma4" {
		t.Errorf("expected default gemma4, got %q", got)
	}
	t.Setenv("PM_OLLAMA_MODEL", "llama3")
	if got := askEnvDefault("PM_OLLAMA_MODEL", "gemma4"); got != "llama3" {
		t.Errorf("expected llama3, got %q", got)
	}
	// Whitespace-only env value falls back to default.
	t.Setenv("PM_OLLAMA_MODEL", "   ")
	if got := askEnvDefault("PM_OLLAMA_MODEL", "gemma4"); got != "gemma4" {
		t.Errorf("whitespace should use default, got %q", got)
	}
}

func TestCmdAskRegisteredSchemaShape(t *testing.T) {
	// The agent owner is forbidden from touching main.go so the command
	// isn't expected to be registered on the root yet. Verify the
	// command's own schema description independently.
	cmd := newAskCmd()
	sc := describeCommand(cmd)
	if sc.Name != "ask" {
		t.Errorf("name=%q", sc.Name)
	}
	if sc.Stdout != "json" {
		t.Errorf("stdout=%q", sc.Stdout)
	}
	want := []int{0, 3, 4, 6, 9}
	if len(sc.ExitCodes) != len(want) {
		t.Fatalf("exit codes=%v", sc.ExitCodes)
	}
	for i := range want {
		if sc.ExitCodes[i] != want[i] {
			t.Errorf("exit[%d]=%d want %d", i, sc.ExitCodes[i], want[i])
		}
	}
	if len(sc.Args) != 1 || sc.Args[0].Name != "question" || !sc.Args[0].Required {
		t.Errorf("args=%+v", sc.Args)
	}
}

func TestCmdAskLongDescribesTrace(t *testing.T) {
	cmd := newAskCmd()
	if !strings.Contains(cmd.Long, "NDJSON") {
		t.Error("Long should document --trace NDJSON switch")
	}
	if !strings.Contains(cmd.Long, "read-only") {
		t.Error("Long should mention read-only")
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestCmdAskInvocationInvalidBinary(t *testing.T) {
	// Verify cmdAsk wraps an agent failure as a KindInternal error. We stub
	// askRunFn (the documented test seam) rather than letting agent.Run exec
	// os.Executable() — which, under `go test`, is the test binary itself and
	// would recursively re-run the suite (and leak a handle that breaks
	// cleanup on Windows). The real agent.Run path is covered in internal/agent.
	mockAgentRun(t, agent.Answer{}, errors.New("dial tcp 127.0.0.1:1: connection refused"))
	err := cmdAsk("q", askOptions{maxSteps: 1, baseURL: "http://127.0.0.1:1", model: "m"})
	if err == nil {
		t.Fatal("expected error")
	}
	if cerr.From(err).Kind != cerr.KindInternal {
		t.Errorf("kind=%v want internal", cerr.From(err).Kind)
	}
}

func TestCmdAskInvocationTrace(t *testing.T) {
	// With trace=true the error must still surface before any events. Stub the
	// agent runner (see TestCmdAskInvocationInvalidBinary) to avoid exec-ing
	// the test binary.
	mockAgentRun(t, agent.Answer{}, errors.New("connection refused"))
	err := cmdAsk("q", askOptions{trace: true, baseURL: "http://127.0.0.1:1", model: "m", maxSteps: 1})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCmdAskSchemaJSONRendering(t *testing.T) {
	cmd := newAskCmd()
	// Exit code annotation parses as expected.
	codes := parseExitCodes(cmd.Annotations["exit_codes"])
	b, _ := json.Marshal(codes)
	if !strings.Contains(string(b), "3") || !strings.Contains(string(b), "9") {
		t.Errorf("codes json=%s", b)
	}
}

// mockAgentRun lets tests swap out the real agent entry point.
func mockAgentRun(t *testing.T, ans agent.Answer, runErr error) {
	t.Helper()
	prev := askRunFn
	askRunFn = func(ctx context.Context, question string, opts agent.Options, w io.Writer) (agent.Answer, error) {
		return ans, runErr
	}
	t.Cleanup(func() { askRunFn = prev })
}

func TestCmdAsk_HappyJSONOutput(t *testing.T) {
	mockAgentRun(t, agent.Answer{
		Answer:     "hello",
		Citations:  []agent.Citation{{UID: 1, Subject: "s", Mailbox: "INBOX"}},
		StepsTaken: 1,
	}, nil)
	stdout, err := captureStdout(t, func() error {
		return cmdAsk("q", askOptions{maxSteps: 1, model: "m", baseURL: "http://unused"})
	})
	if err != nil {
		t.Fatalf("cmdAsk: %v", err)
	}
	// Non-trace output is a single pretty JSON object.
	var out agent.Answer
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("unmarshal: %v (stdout=%q)", err, stdout)
	}
	if out.Answer != "hello" || len(out.Citations) != 1 || out.StepsTaken != 1 {
		t.Errorf("out=%+v", out)
	}
}

func TestCmdAsk_TraceNDJSONOutput(t *testing.T) {
	mockAgentRun(t, agent.Answer{
		Answer:     "ok",
		Citations:  []agent.Citation{{UID: 7}},
		StepsTaken: 2,
	}, nil)
	stdout, err := captureStdout(t, func() error {
		return cmdAsk("q", askOptions{trace: true, maxSteps: 1, model: "m", baseURL: "http://unused"})
	})
	if err != nil {
		t.Fatalf("cmdAsk: %v", err)
	}
	// Trace output is NDJSON; the last line is the {"type":"answer",...}
	// envelope.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("unmarshal last: %v", err)
	}
	if last["type"] != "answer" {
		t.Errorf("last type=%v", last["type"])
	}
	if last["answer"] != "ok" {
		t.Errorf("answer=%v", last["answer"])
	}
}

func TestCmdAsk_RunError(t *testing.T) {
	mockAgentRun(t, agent.Answer{}, io.EOF)
	err := cmdAsk("q", askOptions{maxSteps: 1, model: "m", baseURL: "http://x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if cerr.From(err).Kind != cerr.KindInternal {
		t.Errorf("kind=%v", cerr.From(err).Kind)
	}
}
