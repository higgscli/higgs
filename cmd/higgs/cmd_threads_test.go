package main

import (
	"strings"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imaptest"
	"github.com/higgscli/higgs/internal/imapthread"
)

func mkStubThread(rootUID uint32, rootMsgID string) *imapthread.Thread {
	return &imapthread.Thread{
		Root: &imapthread.Node{UID: rootUID, MessageID: rootMsgID},
	}
}

func threadTestMsg(subject, from, msgID, inReplyTo string) []byte {
	hdr := "From: " + from + "\r\n" +
		"To: u@x.com\r\n" +
		"Subject: " + subject + "\r\n" +
		"Date: Wed, 01 Jan 2026 00:00:00 +0000\r\n" +
		"Message-ID: " + msgID + "\r\n"
	if inReplyTo != "" {
		hdr += "In-Reply-To: " + inReplyTo + "\r\n" +
			"References: " + inReplyTo + "\r\n"
	}
	hdr += "\r\nbody\r\n"
	return []byte(hdr)
}

func TestCmdThreadsHappy(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: threadTestMsg("Hello", "a@x.com", "<t1@x>", "")},
		{RFC822: threadTestMsg("Re: Hello", "b@x.com", "<t1r@x>", "<t1@x>")},
		{RFC822: threadTestMsg("Other", "c@x.com", "<t2@x>", "")},
	}))
	applyTestConfig(t, srv)
	root := rootWithCmd(newThreadsCmd())
	root.SetArgs([]string{"threads", "INBOX"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("threads: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"thread"`) {
		t.Errorf("missing thread row: %s", stdout)
	}
	if !strings.Contains(stdout, `"type":"summary"`) {
		t.Errorf("missing summary: %s", stdout)
	}
	// One thread should mention both Hello and Other roots.
	if !strings.Contains(stdout, `"subject":"Hello"`) {
		t.Errorf("missing Hello subject: %s", stdout)
	}
	if !strings.Contains(stdout, `"subject":"Other"`) {
		t.Errorf("missing Other subject: %s", stdout)
	}
	// The Hello thread should have count=2.
	if !strings.Contains(stdout, `"count":2`) {
		t.Errorf("expected a thread with count=2: %s", stdout)
	}
}

func TestCmdThreadsBadSince(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	err := cmdThreads("INBOX", &threadsFlags{since: "nope"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("expected validation, got %v", cerr.From(err).Kind)
	}
}

func TestCmdThreadsNegativeLimit(t *testing.T) {
	err := cmdThreads("INBOX", &threadsFlags{limit: -1})
	if err == nil {
		t.Fatal("expected validation")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("got %v, want validation", cerr.From(err).Kind)
	}
}

func TestSimpleID(t *testing.T) {
	// Direct unit coverage; simpleID is the fallback path when Root.MessageID
	// is empty.
	got := simpleID(3, mkStubThread(42, ""))
	if got != "thread-3-uid-42" {
		t.Errorf("unexpected id: %q", got)
	}
}

func TestThreadIDUsesMessageIDWhenPresent(t *testing.T) {
	got := threadID(0, mkStubThread(1, "<abc@x>"))
	if got != "<abc@x>" {
		t.Errorf("expected MessageID id, got %q", got)
	}
}

func TestExtractReferencesValue(t *testing.T) {
	in := "References: <a@x> <b@x>\r\n <c@x>\r\n\r\n"
	got := extractReferencesValue(in)
	if !strings.Contains(got, "<a@x>") || !strings.Contains(got, "<c@x>") {
		t.Errorf("missing refs: %q", got)
	}
	if got2 := extractReferencesValue(""); got2 != "" {
		t.Errorf("empty should produce empty: %q", got2)
	}
	if got3 := extractReferencesValue("X-Other: foo\r\n"); got3 != "" {
		t.Errorf("non-references header should produce empty: %q", got3)
	}
}
