package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/akeemjenkins/protoncli/internal/ollama"
)

// scriptedLLM is a fake ChatFunc that returns canned responses in order.
// Each entry is either a raw JSON payload (string) or an error.
type scriptedLLM struct {
	responses []any
	calls     int32
}

func (s *scriptedLLM) fn(ctx context.Context, baseURL, model string, messages []ollama.ChatMessage, schema interface{}, out interface{}) error {
	idx := int(atomic.AddInt32(&s.calls, 1)) - 1
	if idx >= len(s.responses) {
		return errors.New("scriptedLLM: out of responses")
	}
	resp := s.responses[idx]
	if err, ok := resp.(error); ok {
		return err
	}
	body, _ := json.Marshal(resp)
	return json.Unmarshal(body, out)
}

// writeAgentFakeBin reuses the writeFakeBin helper but returns a script
// that dispatches based on the first argv. The script emits NDJSON when
// invoked with `schema` and a match row when invoked with any allow-list
// tool.
func writeAgentFakeBin(t *testing.T, dir string) string {
	t.Helper()
	// The fake binary is a POSIX shell script (#!/bin/sh) which Windows cannot
	// exec directly; these agent integration tests are skipped there.
	if runtime.GOOS == "windows" {
		t.Skip("fake binary is a POSIX shell script; not executable on Windows")
	}
	path := filepath.Join(dir, "protoncli")
	script := `#!/bin/sh
case "$1" in
  schema)
    cat <<'__EOF__'
{
  "commands": [
    {"name":"search","short":"search","flags":[{"name":"from","type":"string","default":"","description":"from"}],"args":[{"name":"mailbox","required":false,"default":""}],"stdout":"ndjson","exit_codes":[0,5]},
    {"name":"summarize","short":"summarize","flags":[],"args":[],"stdout":"ndjson","exit_codes":[0]}
  ],
  "exit_code_docs": []
}
__EOF__
    ;;
  search)
    echo '{"type":"match","uid":42,"subject":"Hello","mailbox":"INBOX"}'
    echo '{"type":"summary","count":1}'
    ;;
  summarize)
    echo '{"summary":"ok"}'
    ;;
  fail)
    echo 'oops' >&2
    exit 5
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestRun_HappyPath(t *testing.T) {
	dir := t.TempDir()
	bin := writeAgentFakeBin(t, dir)
	llm := &scriptedLLM{
		responses: []any{
			map[string]any{"steps": []map[string]any{
				{"tool": "search", "args": []string{"INBOX", "--from", "alice"}, "why": "find alice"},
			}},
			map[string]any{
				"answer": "alice sent one email",
				"citations": []map[string]any{
					{"uid": 42, "subject": "Hello", "mailbox": "INBOX"},
				},
			},
		},
	}
	var buf bytes.Buffer
	ans, err := Run(context.Background(), "did alice email me?", Options{
		BinPath:      bin,
		BaseURL:      "http://unused",
		Model:        "m",
		MaxSteps:     3,
		AllowedTools: []string{"search", "summarize"},
		ChatFn:       llm.fn,
	}, &buf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans.Answer == "" {
		t.Error("empty answer")
	}
	if ans.StepsTaken != 1 {
		t.Errorf("steps=%d want 1", ans.StepsTaken)
	}
	if len(ans.Citations) != 1 || ans.Citations[0].UID != 42 {
		t.Errorf("citations=%v", ans.Citations)
	}
	// No trace events should be emitted when Trace=false.
	if buf.Len() != 0 {
		t.Errorf("unexpected trace output: %q", buf.String())
	}
}

func TestRun_TraceEvents(t *testing.T) {
	dir := t.TempDir()
	bin := writeAgentFakeBin(t, dir)
	llm := &scriptedLLM{
		responses: []any{
			map[string]any{"steps": []map[string]any{
				{"tool": "search", "args": []string{"INBOX"}, "why": "x"},
				{"tool": "summarize", "args": []string{}, "why": "y"},
			}},
			map[string]any{"answer": "done"},
		},
	}
	var buf bytes.Buffer
	_, err := Run(context.Background(), "q", Options{
		BinPath:      bin,
		BaseURL:      "http://unused",
		Model:        "m",
		AllowedTools: []string{"search", "summarize"},
		Trace:        true,
		ChatFn:       llm.fn,
	}, &buf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 trace lines, got %d (%q)", len(lines), buf.String())
	}
	for i, line := range lines {
		var ev TraceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i, err)
		}
		if ev.Type != "step" {
			t.Errorf("line %d type=%q", i, ev.Type)
		}
		if ev.Status != "ok" {
			t.Errorf("line %d status=%q", i, ev.Status)
		}
		if ev.ExitCode != 0 {
			t.Errorf("line %d exit=%d", i, ev.ExitCode)
		}
	}
}

func TestRun_MaxStepsRespected(t *testing.T) {
	dir := t.TempDir()
	bin := writeAgentFakeBin(t, dir)
	llm := &scriptedLLM{
		responses: []any{
			map[string]any{"steps": []map[string]any{
				{"tool": "search", "args": []string{"a"}},
				{"tool": "search", "args": []string{"b"}},
				{"tool": "search", "args": []string{"c"}},
				{"tool": "search", "args": []string{"d"}},
				{"tool": "search", "args": []string{"e"}},
			}},
			map[string]any{"answer": "ok"},
		},
	}
	ans, err := Run(context.Background(), "q", Options{
		BinPath:      bin,
		BaseURL:      "http://x",
		Model:        "m",
		MaxSteps:     2,
		AllowedTools: []string{"search"},
		ChatFn:       llm.fn,
	}, io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans.StepsTaken != 2 {
		t.Errorf("steps=%d want 2", ans.StepsTaken)
	}
}

func TestRun_ToolNotInAllowList(t *testing.T) {
	dir := t.TempDir()
	bin := writeAgentFakeBin(t, dir)
	llm := &scriptedLLM{
		responses: []any{
			map[string]any{"steps": []map[string]any{
				{"tool": "move", "args": []string{"INBOX", "Trash"}},
			}},
			map[string]any{"answer": "rejected"},
		},
	}
	var buf bytes.Buffer
	ans, err := Run(context.Background(), "q", Options{
		BinPath:      bin,
		BaseURL:      "http://x",
		Model:        "m",
		AllowedTools: []string{"search"},
		Trace:        true,
		ChatFn:       llm.fn,
	}, &buf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans.StepsTaken != 1 {
		t.Errorf("steps=%d want 1", ans.StepsTaken)
	}
	if !strings.Contains(buf.String(), `"status":"rejected"`) {
		t.Errorf("trace missing rejected status: %q", buf.String())
	}
}

func TestRun_PlanRetriesThenFails(t *testing.T) {
	dir := t.TempDir()
	bin := writeAgentFakeBin(t, dir)
	llm := &scriptedLLM{
		responses: []any{
			errors.New("bad json from model"),
			errors.New("still bad json"),
		},
	}
	_, err := Run(context.Background(), "q", Options{
		BinPath:      bin,
		BaseURL:      "http://x",
		Model:        "m",
		AllowedTools: []string{"search"},
		ChatFn:       llm.fn,
	}, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "plan failed") {
		t.Errorf("want plan failed, got %v", err)
	}
	if got := atomic.LoadInt32(&llm.calls); got != 2 {
		t.Errorf("llm calls=%d want 2 (retry once)", got)
	}
}

func TestRun_PlanRetrySucceeds(t *testing.T) {
	dir := t.TempDir()
	bin := writeAgentFakeBin(t, dir)
	llm := &scriptedLLM{
		responses: []any{
			errors.New("bad json from model"),
			map[string]any{"steps": []map[string]any{
				{"tool": "search", "args": []string{"INBOX"}},
			}},
			map[string]any{"answer": "ok"},
		},
	}
	ans, err := Run(context.Background(), "q", Options{
		BinPath:      bin,
		BaseURL:      "http://x",
		Model:        "m",
		AllowedTools: []string{"search"},
		ChatFn:       llm.fn,
	}, io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans.StepsTaken != 1 {
		t.Errorf("steps=%d", ans.StepsTaken)
	}
}

func TestRun_StepNonZeroExitContinues(t *testing.T) {
	dir := t.TempDir()
	bin := writeAgentFakeBin(t, dir)
	llm := &scriptedLLM{
		responses: []any{
			map[string]any{"steps": []map[string]any{
				{"tool": "fail", "args": []string{}},
				{"tool": "search", "args": []string{"INBOX"}},
			}},
			map[string]any{"answer": "synthesized despite failure"},
		},
	}
	var buf bytes.Buffer
	ans, err := Run(context.Background(), "q", Options{
		BinPath:      bin,
		BaseURL:      "http://x",
		Model:        "m",
		AllowedTools: []string{"fail", "search"},
		Trace:        true,
		ChatFn:       llm.fn,
	}, &buf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans.StepsTaken != 2 {
		t.Errorf("steps=%d want 2", ans.StepsTaken)
	}
	if !strings.Contains(buf.String(), `"status":"nonzero_exit"`) {
		t.Errorf("expected nonzero_exit trace: %q", buf.String())
	}
}

func TestRun_AnswerSynthesisError(t *testing.T) {
	dir := t.TempDir()
	bin := writeAgentFakeBin(t, dir)
	llm := &scriptedLLM{
		responses: []any{
			map[string]any{"steps": []map[string]any{
				{"tool": "search", "args": []string{"INBOX"}},
			}},
			errors.New("synth failed"),
		},
	}
	_, err := Run(context.Background(), "q", Options{
		BinPath:      bin,
		BaseURL:      "http://x",
		Model:        "m",
		AllowedTools: []string{"search"},
		ChatFn:       llm.fn,
	}, io.Discard)
	if err == nil {
		t.Fatal("expected synth error")
	}
	if !strings.Contains(err.Error(), "synthesize") {
		t.Errorf("want synthesize, got %v", err)
	}
}

func TestRun_Validation(t *testing.T) {
	_, err := Run(context.Background(), "", Options{BinPath: "x", BaseURL: "u", Model: "m"}, io.Discard)
	if err == nil {
		t.Error("empty question should error")
	}
	_, err = Run(context.Background(), "q", Options{BaseURL: "u", Model: "m"}, io.Discard)
	if err == nil {
		t.Error("empty BinPath should error")
	}
	_, err = Run(context.Background(), "q", Options{BinPath: "x", Model: "m"}, io.Discard)
	if err == nil {
		t.Error("empty BaseURL should error")
	}
	_, err = Run(context.Background(), "q", Options{BinPath: "x", BaseURL: "u"}, io.Discard)
	if err == nil {
		t.Error("empty Model should error")
	}
}

func TestRun_DiscoverFails(t *testing.T) {
	llm := &scriptedLLM{}
	_, err := Run(context.Background(), "q", Options{
		BinPath: "/definitely/not/a/bin",
		BaseURL: "http://x",
		Model:   "m",
		ChatFn:  llm.fn,
	}, io.Discard)
	if err == nil {
		t.Fatal("expected discover error")
	}
	if !strings.Contains(err.Error(), "discover tools") {
		t.Errorf("want discover wrap: %v", err)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abc", 10); got != "abc" {
		t.Errorf("truncate short: %q", got)
	}
	got := truncate("0123456789", 5)
	if !strings.HasPrefix(got, "01234") {
		t.Errorf("truncate prefix: %q", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("truncate marker: %q", got)
	}
}

func TestChatFnDefault_UsedWhenNil(t *testing.T) {
	// When ChatFn is nil, ollama.ChatWithSchema is used. We wire a real
	// http server so the fallback path is exercised.
	dir := t.TempDir()
	bin := writeAgentFakeBin(t, dir)
	respNum := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&respNum, 1)
		var content string
		if n == 1 {
			content = `{"steps":[{"tool":"search","args":["INBOX"]}]}`
		} else {
			content = `{"answer":"from fallback","citations":[]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": content},
			"done":    true,
		})
	}))
	defer srv.Close()
	ans, err := Run(context.Background(), "q", Options{
		BinPath:      bin,
		BaseURL:      srv.URL,
		Model:        "m",
		AllowedTools: []string{"search"},
	}, io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans.Answer == "" {
		t.Error("empty answer from fallback path")
	}
}
