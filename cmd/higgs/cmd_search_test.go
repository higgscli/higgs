package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imaptest"
)

func applyTestConfig(t *testing.T, srv *imaptest.Server) {
	t.Helper()
	cfg := imaptest.Config(srv)
	t.Setenv("PM_IMAP_HOST", cfg.Host)
	t.Setenv("PM_IMAP_PORT", fmt.Sprintf("%d", cfg.Port))
	t.Setenv("PM_IMAP_USERNAME", cfg.Username)
	t.Setenv("PM_IMAP_PASSWORD", cfg.Password)
	t.Setenv("PM_IMAP_SECURITY", string(cfg.Security))
	t.Setenv("PM_IMAP_TLS_SKIP_VERIFY", "true")
}

func testMsg(subject, from string) []byte {
	return []byte("From: " + from + "\r\nTo: u@x.com\r\nSubject: " + subject +
		"\r\nDate: Wed, 01 Jan 2026 00:00:00 +0000\r\nMessage-ID: <" + subject + "@t>\r\n\r\nhi\r\n")
}

func TestCmdSearchHappy(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("A", "alice@x.com")},
		{RFC822: testMsg("B", "bob@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"search", "INBOX", "--from", "alice@x.com"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("search: %v (stdout=%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"summary"`) {
		t.Errorf("missing summary: %s", stdout)
	}
	if !strings.Contains(stdout, `"type":"match"`) {
		t.Errorf("missing match row: %s", stdout)
	}
}

func TestCmdSearchValidation(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	root := newRootCmd()
	root.SetArgs([]string{"search", "--unseen", "--seen"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("expected validation kind, got %v", cerr.From(err).Kind)
	}
}

func TestCmdSearchBadDate(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	root := newRootCmd()
	root.SetArgs([]string{"search", "--since", "not-a-date"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("expected validation kind, got %v", cerr.From(err).Kind)
	}
}

func TestParseUIDList(t *testing.T) {
	uids, err := parseUIDList(" 1, 2 , 3,,4 ")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(uids) != 4 || uids[0] != 1 || uids[3] != 4 {
		t.Errorf("unexpected: %v", uids)
	}
	if _, err := parseUIDList("not-a-uid"); err == nil {
		t.Error("expected parse error")
	}
	if uids, err := parseUIDList(""); err != nil || len(uids) != 0 {
		t.Errorf("empty input should return empty slice: %v %v", uids, err)
	}
}

func TestTrimAll(t *testing.T) {
	got := trimAll([]string{" a ", "", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("unexpected: %v", got)
	}
}
