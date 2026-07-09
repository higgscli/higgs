package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imaptest"
)

// `higgs extract ... --apply-as-label <Label> [--when <field>=<value>]`
// turns extraction results directly into a persistent, searchable label
// (a Labels/<Label> mailbox, as elsewhere in higgs) instead of forcing the
// caller to bookkeep JSON output and re-drive `apply-labels` themselves:
//
//   - --apply-as-label alone labels every successfully extracted message
//   - with --when field=value, only messages whose extracted data has that
//     field stringifying to that value (booleans: "true"/"false") are labeled
//   - extraction rows gain "label_applied": true|false, and the summary
//     gains a "labels_applied" count
//   - --when without --apply-as-label, or a --when without "=", is a
//     validation error

func startExtractServer(t *testing.T) *imaptest.Server {
	t.Helper()
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("please-reply", "human@x.com")},
	}))
	applyTestConfig(t, srv)
	return srv
}

func writeNeedsReplySchema(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "needs_reply.json")
	schema := `{"type":"object","properties":{"needs_reply":{"type":"boolean"}},"required":["needs_reply"]}`
	if err := os.WriteFile(path, []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// labelMessageCount returns how many messages Labels/<label> holds, or -1 if
// the mailbox does not exist.
func labelMessageCount(t *testing.T, srv *imaptest.Server, label string) int {
	t.Helper()
	c, err := mustDial(srv)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Logout()
	status, err := c.Select("Labels/"+label, true)
	if err != nil {
		return -1
	}
	return int(status.Messages)
}

func runExtract(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	root.SetArgs(append([]string{"extract"}, args...))
	return captureStdout(t, func() error { return root.Execute() })
}

func TestExtractApplyAsLabel_WhenTrue(t *testing.T) {
	srv := startExtractServer(t)
	ollama := fakeOllamaJSON(t, `{"needs_reply":true}`)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "test-model")

	stdout, err := runExtract(t, "INBOX", "--uid", "1", "--schema", writeNeedsReplySchema(t),
		"--apply-as-label", "Needs-Reply", "--when", "needs_reply=true")
	if err != nil {
		t.Fatalf("extract: %v (%s)", err, stdout)
	}
	rows := ndjsonRows(t, stdout)
	ext := rowsOfType(rows, "extraction")
	if len(ext) != 1 {
		t.Fatalf("got %d extraction rows: %s", len(ext), stdout)
	}
	if ext[0]["label_applied"] != true {
		t.Errorf("label_applied = %v, want true", ext[0]["label_applied"])
	}
	if sum := summaryRow(t, rows); sum["labels_applied"].(float64) != 1 {
		t.Errorf("summary labels_applied = %v, want 1", sum["labels_applied"])
	}
	if n := labelMessageCount(t, srv, "Needs-Reply"); n != 1 {
		t.Errorf("Labels/Needs-Reply message count = %d, want 1", n)
	}
}

func TestExtractApplyAsLabel_WhenFalse(t *testing.T) {
	srv := startExtractServer(t)
	ollama := fakeOllamaJSON(t, `{"needs_reply":false}`)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "test-model")

	stdout, err := runExtract(t, "INBOX", "--uid", "1", "--schema", writeNeedsReplySchema(t),
		"--apply-as-label", "Needs-Reply", "--when", "needs_reply=true")
	if err != nil {
		t.Fatalf("extract: %v (%s)", err, stdout)
	}
	rows := ndjsonRows(t, stdout)
	ext := rowsOfType(rows, "extraction")
	if len(ext) != 1 || ext[0]["label_applied"] != false {
		t.Errorf("extraction rows: %v", ext)
	}
	if sum := summaryRow(t, rows); sum["labels_applied"].(float64) != 0 {
		t.Errorf("summary labels_applied = %v, want 0", sum["labels_applied"])
	}
	// The label mailbox must not even be created when nothing matched.
	if n := labelMessageCount(t, srv, "Needs-Reply"); n != -1 {
		t.Errorf("Labels/Needs-Reply exists with %d messages, want absent", n)
	}
}

func TestExtractApplyAsLabel_NoWhenLabelsAllSuccessful(t *testing.T) {
	srv := startExtractServer(t)
	ollama := fakeOllamaJSON(t, `{"needs_reply":false}`)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "test-model")

	stdout, err := runExtract(t, "INBOX", "--uid", "1", "--schema", writeNeedsReplySchema(t),
		"--apply-as-label", "Extracted")
	if err != nil {
		t.Fatalf("extract: %v (%s)", err, stdout)
	}
	if n := labelMessageCount(t, srv, "Extracted"); n != 1 {
		t.Errorf("Labels/Extracted message count = %d, want 1", n)
	}
}

func TestExtractApplyAsLabel_StringMatch(t *testing.T) {
	srv := startExtractServer(t)
	ollama := fakeOllamaJSON(t, `{"category":"invoice"}`)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "test-model")

	schema := filepath.Join(t.TempDir(), "cat.json")
	if err := os.WriteFile(schema, []byte(`{"type":"object","properties":{"category":{"type":"string"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, err := runExtract(t, "INBOX", "--uid", "1", "--schema", schema,
		"--apply-as-label", "Invoices", "--when", "category=invoice")
	if err != nil {
		t.Fatalf("extract: %v (%s)", err, stdout)
	}
	if n := labelMessageCount(t, srv, "Invoices"); n != 1 {
		t.Errorf("Labels/Invoices message count = %d, want 1", n)
	}
}

func TestExtractApplyAsLabel_UIDsFromStdin(t *testing.T) {
	srv := startExtractServer(t)
	ollama := fakeOllamaJSON(t, `{"needs_reply":true}`)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "test-model")

	var stdout string
	var err error
	withStdin(t, `{"type":"match","uid":1}`+"\n", func() {
		stdout, err = runExtract(t, "INBOX", "--uid", "-", "--schema", writeNeedsReplySchema(t),
			"--apply-as-label", "Needs-Reply", "--when", "needs_reply=true")
	})
	if err != nil {
		t.Fatalf("extract --uid -: %v (%s)", err, stdout)
	}
	if n := labelMessageCount(t, srv, "Needs-Reply"); n != 1 {
		t.Errorf("Labels/Needs-Reply message count = %d, want 1", n)
	}
}

func TestExtractApplyAsLabel_Validation(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	schema := writeNeedsReplySchema(t)

	// --when without --apply-as-label.
	_, err := runExtract(t, "INBOX", "--uid", "1", "--schema", schema, "--when", "needs_reply=true")
	if err == nil {
		t.Fatal("expected validation error for --when without --apply-as-label")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v, want validation", cerr.From(err).Kind)
	}

	// Malformed --when (no '=').
	_, err = runExtract(t, "INBOX", "--uid", "1", "--schema", schema,
		"--apply-as-label", "X", "--when", "needs_reply")
	if err == nil {
		t.Fatal("expected validation error for malformed --when")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v, want validation", cerr.From(err).Kind)
	}
}
