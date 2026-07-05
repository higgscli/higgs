package main

import (
	"strings"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imaptest"
)

func TestCmdMarkReadHappy(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("X", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"mark-read", "INBOX", "--uid", "1"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("mark-read: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"marked"`) || !strings.Contains(stdout, `"type":"summary"`) {
		t.Errorf("output missing expected rows: %s", stdout)
	}
}

func TestCmdMarkReadDryRun(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("X", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"mark-read", "INBOX", "--uid", "1", "--dry-run", "--unread"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(stdout, `"type":"pending"`) || !strings.Contains(stdout, `"state":"unread"`) {
		t.Errorf("dry-run output: %s", stdout)
	}
}

func TestCmdMarkReadTargetValidation(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	// Neither --uid nor --all-matching → validation exit 3 early (but dial happens first
	// because target resolution runs after dial). Instead force an explicit-vs-all conflict.
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"mark-read", "INBOX", "--uid", "1", "--all-matching"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("got kind %v, want validation", cerr.From(err).Kind)
	}
}

func TestCmdMarkReadNoTarget(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"mark-read", "INBOX"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error for missing target")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("got kind %v", cerr.From(err).Kind)
	}
}

func TestCmdFlagHappy(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"flag", "INBOX", "--uid", "1", "--add", "\\Flagged"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("flag: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"flagged"`) {
		t.Errorf("missing flagged row: %s", stdout)
	}
}

func TestCmdFlagValidation(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	root := newRootCmd()
	root.SetArgs([]string{"flag", "INBOX", "--uid", "1"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error: no --add or --remove")
	}

	root = newRootCmd()
	root.SetArgs([]string{"flag", "INBOX", "--uid", "1", "--add", "A", "--remove", "B"})
	_, err = captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error: both --add and --remove")
	}
}

func TestCmdMoveDryRun(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"move", "INBOX", "Archive", "--uid", "1", "--dry-run"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(stdout, `"type":"pending"`) || !strings.Contains(stdout, `"dst":"Archive"`) {
		t.Errorf("unexpected output: %s", stdout)
	}
}

func TestCmdArchiveDryRun(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"archive", "INBOX", "--uid", "1", "--dry-run"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(stdout, `"dst":"Archive"`) {
		t.Errorf("default target should be Archive: %s", stdout)
	}
}

func TestCmdArchiveHappy(t *testing.T) {
	srv := imaptest.Start(t,
		imaptest.WithMailbox("INBOX", []imaptest.Message{{RFC822: testMsg("x", "a@x.com")}}),
		imaptest.WithMailbox("Archive", nil),
	)
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"archive", "INBOX", "--uid", "1"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("archive: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"archived"`) || !strings.Contains(stdout, `"archived":1`) {
		t.Errorf("missing archived rows: %s", stdout)
	}
	if strings.Contains(stdout, `"type":"error"`) {
		t.Errorf("unexpected error rows: %s", stdout)
	}
}

func TestCmdArchivePartialFailureIsReported(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	// Nonexistent target: the message stays in INBOX, which must surface as
	// per-UID error rows and a non-nil (non-zero exit) command error — never
	// as "archived".
	root.SetArgs([]string{"archive", "INBOX", "--uid", "1", "--target", "NoSuchMailbox"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatalf("expected error for unmoved messages, output: %s", stdout)
	}
	if cerr.From(err).Kind != cerr.KindIMAP {
		t.Errorf("kind=%v want imap", cerr.From(err).Kind)
	}
	if !strings.Contains(stdout, `"type":"error"`) || !strings.Contains(stdout, `"failed":1`) {
		t.Errorf("missing error/failed rows: %s", stdout)
	}
	if strings.Contains(stdout, `"type":"archived"`) {
		t.Errorf("must not claim archived for unmoved UIDs: %s", stdout)
	}
}

func TestCmdTrashDryRun(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"trash", "INBOX", "--uid", "1", "--dry-run"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(stdout, `"dst":"Trash"`) {
		t.Errorf("default target should be Trash: %s", stdout)
	}
}

func TestCmdMoveAllMatching(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("keepme", "keep@x.com")},
		{RFC822: testMsg("archive-me", "auto@x.com")},
	}))
	applyTestConfig(t, srv)
	// Pre-create target mailbox for MOVE-fallback path.
	root := newRootCmd()
	root.SetArgs([]string{"move", "INBOX", "Archive", "--all-matching", "--from", "auto@x.com", "--dry-run"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("move: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"dst":"Archive"`) {
		t.Errorf("unexpected: %s", stdout)
	}
}
