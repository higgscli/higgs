package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akeemjenkins/protoncli/internal/cerr"
	"github.com/akeemjenkins/protoncli/internal/imaptest"
)

// fakeOllamaJSON spins up an httptest server that returns a canned JSON
// envelope (with the given inner content) for /api/chat.
func fakeOllamaJSON(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": content},
			"done":    true,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSummarizeCmdFlags(t *testing.T) {
	cmd := newSummarizeCmd()
	if cmd.Annotations["stdout_format"] != "ndjson" {
		t.Errorf("stdout_format = %q", cmd.Annotations["stdout_format"])
	}
	for _, name := range []string{"uid", "thread", "limit", "user-context", "model", "max-bullets"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag %q", name)
		}
	}
}

func TestSummarizeValidation_NoTarget(t *testing.T) {
	root := newSummarizeCmd()
	root.SetArgs([]string{"INBOX"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v", cerr.From(err).Kind)
	}
}

func TestSummarizeValidation_BothTargets(t *testing.T) {
	root := newSummarizeCmd()
	root.SetArgs([]string{"INBOX", "--uid", "1", "--thread", "2"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v", cerr.From(err).Kind)
	}
}

func TestSummarizeHappy_UIDs(t *testing.T) {
	// imaptest purges the memory backend's default INBOX message before
	// seeding, so the single seeded message is UID 1.
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("Hello", "alice@x.com")},
	}))
	applyTestConfig(t, srv)

	ollama := fakeOllamaJSON(t, `{"tldr":"Hi","bullets":["a"],"is_action_required":false,"due_date":""}`)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "m")

	root := newSummarizeCmd()
	root.SetArgs([]string{"INBOX", "--uid", "1"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("summarize: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"summary_item"`) {
		t.Errorf("missing item: %s", stdout)
	}
	if !strings.Contains(stdout, `"type":"summary"`) {
		t.Errorf("missing summary terminator: %s", stdout)
	}
}

func TestSummarizeError_OllamaFailure(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("Hello", "alice@x.com")},
	}))
	applyTestConfig(t, srv)
	// Server returns 500 on /api/chat.
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "m")

	root := newSummarizeCmd()
	root.SetArgs([]string{"INBOX", "--uid", "1"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("expected per-item failure, not top-level error: %v", err)
	}
	if !strings.Contains(stdout, `"failed":1`) {
		t.Errorf("expected failed=1 in summary: %s", stdout)
	}
}

func TestSummarizeThreadHappy(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("First", "alice@x.com")},
	}))
	applyTestConfig(t, srv)
	ollama := fakeOllamaJSON(t, `{"tldr":"Hi","bullets":["a"],"is_action_required":false}`)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "m")

	root := newSummarizeCmd()
	root.SetArgs([]string{"INBOX", "--thread", "1"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("thread: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"thread":true`) {
		t.Errorf("missing thread marker: %s", stdout)
	}
}

func TestSummarizeValidation_BadUIDList(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", nil))
	applyTestConfig(t, srv)
	root := newSummarizeCmd()
	root.SetArgs([]string{"INBOX", "--uid", "abc"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v", cerr.From(err).Kind)
	}
}

func TestSplitRefs(t *testing.T) {
	got := splitRefs(" <a@x>  <b@x> \t<c@x>")
	if len(got) != 3 {
		t.Errorf("got %v", got)
	}
	if len(splitRefs("")) != 0 {
		t.Error("empty should be empty")
	}
}
