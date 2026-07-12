package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaptest"
)

func writeBody(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}
	return p
}

func TestDraftCmdFlagsAndAnnotations(t *testing.T) {
	cmd := newDraftCmd()
	if cmd.Annotations["stdout_format"] != "json" {
		t.Errorf("stdout_format: %q", cmd.Annotations["stdout_format"])
	}
	if cmd.Annotations["exit_codes"] != "0,2,3,4,5" {
		t.Errorf("exit_codes: %q", cmd.Annotations["exit_codes"])
	}
	if !strings.Contains(strings.ToLower(cmd.Short), "drafts") {
		t.Errorf("Short must mention Drafts: %q", cmd.Short)
	}
	if !strings.Contains(strings.ToLower(cmd.Short), "does not send") && !strings.Contains(strings.ToLower(cmd.Short), "not send") {
		t.Errorf("Short must make non-sending explicit: %q", cmd.Short)
	}
	for _, name := range []string{"to", "subject", "body-file", "dry-run", "drafts-mailbox", "in-reply-to", "source-mailbox"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag %q", name)
		}
	}
}

func TestDraftDryRun(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	body := writeBody(t, "hello world")
	cmd := newDraftCmd()
	cmd.SetArgs([]string{
		"--to", "user@example.com",
		"--subject", "Hi",
		"--body-file", body,
		"--from", "me@example.com",
		"--dry-run",
	})
	stdout, err := captureStdout(t, func() error { return cmd.Execute() })
	if err != nil {
		t.Fatalf("draft dry-run: %v (%s)", err, stdout)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if out["type"] != "draft_preview" {
		t.Errorf("wrong type: %v", out["type"])
	}
	if _, ok := out["bytes"].(float64); !ok {
		t.Errorf("missing bytes: %v", out)
	}
	raw, ok := out["rfc822"].(string)
	if !ok {
		t.Errorf("missing rfc822: %v", out)
	}
	if !strings.Contains(raw, "Subject: Hi") {
		t.Errorf("subject missing: %s", raw)
	}
	if !strings.Contains(raw, "hello world") {
		t.Errorf("body missing: %s", raw)
	}
}

func TestDraftValidationNoRecipient(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	body := writeBody(t, "hi")
	cmd := newDraftCmd()
	cmd.SetArgs([]string{"--subject", "Hi", "--body-file", body, "--dry-run"})
	_, err := captureStdout(t, func() error { return cmd.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind: %v", cerr.From(err).Kind)
	}
}

func TestDraftValidationNoBody(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	cmd := newDraftCmd()
	cmd.SetArgs([]string{"--to", "u@x.com", "--dry-run"})
	_, err := captureStdout(t, func() error { return cmd.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestDraftBadBodyFile(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	cmd := newDraftCmd()
	cmd.SetArgs([]string{"--to", "u@x.com", "--body-file", "/no/such/file.txt", "--dry-run"})
	_, err := captureStdout(t, func() error { return cmd.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind: %v", cerr.From(err).Kind)
	}
}

func TestDraftRejectsNonUTF8Body(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	p := filepath.Join(t.TempDir(), "body.bin")
	if err := os.WriteFile(p, []byte{0xff, 0xfe, 0x00}, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := newDraftCmd()
	cmd.SetArgs([]string{"--to", "u@x.com", "--body-file", p, "--dry-run"})
	_, err := captureStdout(t, func() error { return cmd.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestDraftAppendToDrafts(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("hi", "a@x.com")},
	}), imaptest.WithMailbox("Drafts", nil))
	applyTestConfig(t, srv)
	body := writeBody(t, "draft body")
	cmd := newDraftCmd()
	cmd.SetArgs([]string{
		"--to", "user@example.com",
		"--subject", "Hello",
		"--body-file", body,
		"--from", "me@example.com",
	})
	stdout, err := captureStdout(t, func() error { return cmd.Execute() })
	if err != nil {
		t.Fatalf("draft: %v (%s)", err, stdout)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parse: %v\n%s", err, stdout)
	}
	if out["drafted"] != true {
		t.Errorf("drafted flag missing: %v", out)
	}
	if out["mailbox"] != "Drafts" {
		t.Errorf("mailbox: %v", out["mailbox"])
	}
	msgID, ok := out["message_id"].(string)
	if !ok || msgID == "" {
		t.Errorf("message_id missing: %v", out)
	}

	// Verify the message was actually APPENDed to Drafts.
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("dial verify: %v", err)
	}
	defer imapclient.CloseAndLogout(c)
	status, err := c.Select("Drafts", true)
	if err != nil {
		t.Fatalf("select Drafts: %v", err)
	}
	if status.Messages < 1 {
		t.Errorf("Drafts empty: %d", status.Messages)
	}
}

func TestDraftInReplyTo(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("Conv", []imaptest.Message{
		{RFC822: testMsg("Original", "a@x.com")},
	}), imaptest.WithMailbox("Drafts", nil))
	applyTestConfig(t, srv)
	body := writeBody(t, "my reply")
	cmd := newDraftCmd()
	cmd.SetArgs([]string{
		"--to", "a@x.com",
		"--body-file", body,
		"--from", "me@x.com",
		"--in-reply-to", "1",
		"--source-mailbox", "Conv",
		"--dry-run",
	})
	stdout, err := captureStdout(t, func() error { return cmd.Execute() })
	if err != nil {
		t.Fatalf("reply: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, "draft_preview") {
		t.Errorf("expected preview: %s", stdout)
	}
	// Expect Subject prefixed with Re: and In-Reply-To header set.
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, _ := out["rfc822"].(string)
	if !strings.Contains(raw, "Subject: Re: Original") {
		t.Errorf("subject missing Re prefix: %s", raw)
	}
	if !strings.Contains(raw, "In-Reply-To:") {
		t.Errorf("In-Reply-To missing: %s", raw)
	}
}

func TestDraftInReplyToInvalidUID(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", nil))
	applyTestConfig(t, srv)
	body := writeBody(t, "x")
	cmd := newDraftCmd()
	cmd.SetArgs([]string{
		"--to", "a@x.com",
		"--body-file", body,
		"--from", "me@x.com",
		"--in-reply-to", "not-a-number",
		"--dry-run",
	})
	_, err := captureStdout(t, func() error { return cmd.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind: %v", cerr.From(err).Kind)
	}
}

func TestHasRePrefix(t *testing.T) {
	cases := map[string]bool{
		"Re: hi": true,
		"re: hi": true,
		"RE: x":  true,
		"":       false,
		"Fwd: x": false,
		"hello":  false,
	}
	for in, want := range cases {
		if got := hasRePrefix(in); got != want {
			t.Errorf("hasRePrefix(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFlattenAddrs(t *testing.T) {
	got := flattenAddrs([]string{"a@x.com, b@x.com", " c@x.com "})
	if len(got) != 3 {
		t.Fatalf("got: %v", got)
	}
	if got[0] != "a@x.com" || got[1] != "b@x.com" || got[2] != "c@x.com" {
		t.Errorf("got: %v", got)
	}
}

func TestExtractMessageID(t *testing.T) {
	raw := []byte("From: a\r\nMessage-ID: <abc@x>\r\nSubject: hi\r\n\r\nbody")
	if got := extractMessageID(raw); got != "<abc@x>" {
		t.Errorf("got %q", got)
	}
	if got := extractMessageID([]byte("no headers")); got != "" {
		t.Errorf("no-id: got %q", got)
	}
}

func TestUTF8Valid(t *testing.T) {
	if !utf8Valid([]byte("hello")) {
		t.Error("ascii")
	}
	if !utf8Valid([]byte("héllo")) {
		t.Error("latin1-utf8")
	}
	if utf8Valid([]byte{0xff, 0xfe}) {
		t.Error("bad bytes accepted")
	}
	if !utf8Valid([]byte("π日本語")) {
		t.Error("multibyte rejected")
	}
	if utf8Valid([]byte{0xC0}) { // truncated
		t.Error("truncated accepted")
	}
}

func TestReadBodyFileStdin(t *testing.T) {
	// Swap stdin.
	prev := os.Stdin
	defer func() { os.Stdin = prev }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	_, _ = w.Write([]byte("from stdin"))
	_ = w.Close()
	os.Stdin = r
	got, err := readBodyFile("-")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "from stdin" {
		t.Errorf("got %q", got)
	}
}

func TestReadBodyFileEmpty(t *testing.T) {
	got, err := readBodyFile("")
	if err != nil || got != "" {
		t.Errorf("empty: got %q / %v", got, err)
	}
}
