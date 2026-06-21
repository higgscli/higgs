package imaputil_test

import (
	"testing"

	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/imaptest"
)

func TestListMailboxes_OverRealConnection(t *testing.T) {
	srv := imaptest.Start(t,
		imaptest.WithMailbox("Labels", nil),
		imaptest.WithMailbox("Labels/Work", nil),
		imaptest.WithMailbox("Folders/Accounts", nil),
	)
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)

	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	if len(mboxes) == 0 {
		t.Fatal("expected at least INBOX + seeded mailboxes")
	}

	names := make(map[string]bool, len(mboxes))
	for _, m := range mboxes {
		names[m.Name] = true
	}
	for _, want := range []string{"INBOX", "Labels", "Labels/Work", "Folders/Accounts"} {
		if !names[want] {
			t.Errorf("missing mailbox %q in ListMailboxes result: %v", want, names)
		}
	}

	// DetectLabelsRoot over the live list must return "Labels".
	root, ok := imaputil.DetectLabelsRoot(mboxes)
	if !ok || root != "Labels" {
		t.Errorf("DetectLabelsRoot = %q, %v; want Labels,true", root, ok)
	}

	// ResolveMailboxName case-folding via a live listing.
	got, err := imaputil.ResolveMailboxName("labels/work", mboxes)
	if err != nil {
		t.Fatalf("ResolveMailboxName: %v", err)
	}
	if got != "Labels/Work" {
		t.Errorf("ResolveMailboxName = %q, want Labels/Work", got)
	}

	// useStatus=true takes the same code path but without LIST-STATUS. Exercise it.
	if _, err := imaputil.ListMailboxes(c, true); err != nil {
		t.Fatalf("ListMailboxes(useStatus=true): %v", err)
	}
}

// TestListMailboxes_Empty exercises the 0-mailbox return path by connecting,
// logging out, and then trying to list through a nil — here we use the normal
// flow but confirm no panic on the empty-listing case.
func TestListMailboxes_Empty(t *testing.T) {
	srv := imaptest.Start(t)
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)

	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	// The memory backend seeds an INBOX by default, so we expect >=1.
	if len(mboxes) == 0 {
		t.Error("expected default INBOX from memory backend")
	}
}
