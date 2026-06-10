package main

import (
	"strings"
	"testing"

	"github.com/akeemjenkins/protoncli/internal/cerr"
	"github.com/akeemjenkins/protoncli/internal/imaptest"
	"github.com/akeemjenkins/protoncli/internal/imapthread"
)

func TestCmdThreadByUID(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: threadTestMsg("Hello", "a@x.com", "<t1@x>", "")},
		{RFC822: threadTestMsg("Re: Hello", "b@x.com", "<t1r@x>", "<t1@x>")},
		{RFC822: threadTestMsg("Other", "c@x.com", "<t2@x>", "")},
	}))
	applyTestConfig(t, srv)
	// Discover the UID of the first Hello message via an initial threads call.
	root := rootWithCmd(newThreadsCmd())
	root.SetArgs([]string{"threads", "INBOX"})
	if _, err := captureStdout(t, func() error { return root.Execute() }); err != nil {
		t.Fatalf("threads: %v", err)
	}

	root2 := rootWithCmd(newThreadCmd())
	// imaptest purges the memory backend's default INBOX message before
	// seeding, so the first seeded message ("Hello") is UID 1.
	root2.SetArgs([]string{"thread", "INBOX", "--uid", "1"})
	stdout, err := captureStdout(t, func() error { return root2.Execute() })
	if err != nil {
		t.Fatalf("thread: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"thread_message"`) {
		t.Errorf("missing thread_message row: %s", stdout)
	}
	if !strings.Contains(stdout, `"type":"summary"`) {
		t.Errorf("missing summary: %s", stdout)
	}
	// The Hello thread has 2 messages.
	if !strings.Contains(stdout, `"count":2`) {
		t.Errorf("expected count=2: %s", stdout)
	}
}

func TestCmdThreadByMessageID(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: threadTestMsg("Hello", "a@x.com", "<hi@x>", "")},
		{RFC822: threadTestMsg("Re: Hello", "b@x.com", "<hi-r@x>", "<hi@x>")},
	}))
	applyTestConfig(t, srv)
	root := rootWithCmd(newThreadCmd())
	root.SetArgs([]string{"thread", "INBOX", "--message-id", "<hi@x>"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("thread: %v (%s)", err, stdout)
	}
	// encoding/json escapes < and > to \u003c / \u003e by default.
	if !strings.Contains(stdout, `\u003chi@x\u003e`) {
		t.Errorf("missing anchor msgid: %s", stdout)
	}
	if !strings.Contains(stdout, `"count":2`) {
		t.Errorf("want count=2: %s", stdout)
	}
}

func TestCmdThreadValidation(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	// Neither flag provided.
	err := cmdThread("INBOX", &threadFlags{})
	if err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("expected validation err, got %v", err)
	}
	// Both provided.
	err = cmdThread("INBOX", &threadFlags{uid: 1, messageID: "<x@x>"})
	if err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("expected validation err, got %v", err)
	}
}

func TestCmdThreadAnchorNotFound(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: threadTestMsg("X", "a@x.com", "<x@x>", "")},
	}))
	applyTestConfig(t, srv)
	root := rootWithCmd(newThreadCmd())
	root.SetArgs([]string{"thread", "INBOX", "--uid", "99999"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("got kind %v, want validation", cerr.From(err).Kind)
	}
}

func TestFlattenThread(t *testing.T) {
	// Nil / empty-root safety.
	if got := flattenThread(nil); got != nil {
		t.Errorf("nil: got %v", got)
	}
	if got := flattenThread(&imapthread.Thread{}); got != nil {
		t.Errorf("empty root: got %v", got)
	}
	root := &imapthread.Node{UID: 1, MessageID: "<r@x>"}
	child := &imapthread.Node{UID: 2, MessageID: "<c@x>"}
	root.Children = []*imapthread.Node{child}
	th := &imapthread.Thread{Root: root}
	got := flattenThread(th)
	if len(got) != 2 {
		t.Errorf("want 2 nodes, got %d", len(got))
	}
}

func TestFindThread(t *testing.T) {
	root := &imapthread.Node{UID: 10, MessageID: "<r@x>"}
	th := &imapthread.Thread{Root: root, UIDs: []uint32{10}}
	threads := []*imapthread.Thread{th}
	if findThread(threads, &threadFlags{uid: 10}) == nil {
		t.Error("expected match by UID")
	}
	if findThread(threads, &threadFlags{messageID: "<r@x>"}) == nil {
		t.Error("expected match by MessageID")
	}
	if findThread(threads, &threadFlags{uid: 99}) != nil {
		t.Error("unexpected match for unknown UID")
	}
	if findThread(threads, &threadFlags{messageID: "<nope@x>"}) != nil {
		t.Error("unexpected match for unknown MessageID")
	}
}
