package imapapply

import (
	"testing"

	"github.com/higgscli/higgs/internal/imaputil"
)

func TestBuildMailboxSet_Nil(t *testing.T) {
	got := BuildMailboxSet(nil)
	if got == nil {
		t.Fatal("BuildMailboxSet(nil) should return a non-nil empty map")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestBuildMailboxSet_Empty(t *testing.T) {
	got := BuildMailboxSet([]imaputil.MailboxInfo{})
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestBuildMailboxSet_Unique(t *testing.T) {
	in := []imaputil.MailboxInfo{
		{Name: "INBOX"},
		{Name: "Sent"},
		{Name: "Drafts"},
	}
	got := BuildMailboxSet(in)
	if len(got) != 3 {
		t.Errorf("len=%d, want 3", len(got))
	}
	for _, n := range []string{"INBOX", "Sent", "Drafts"} {
		if !got[n] {
			t.Errorf("missing %q", n)
		}
	}
}

func TestBuildMailboxSet_Duplicates(t *testing.T) {
	in := []imaputil.MailboxInfo{
		{Name: "INBOX"},
		{Name: "INBOX"},
		{Name: "Sent"},
	}
	got := BuildMailboxSet(in)
	if len(got) != 2 {
		t.Errorf("dup handling: len=%d, want 2", len(got))
	}
}

func TestBuildMailboxSet_CaseSensitive(t *testing.T) {
	in := []imaputil.MailboxInfo{
		{Name: "Inbox"},
		{Name: "INBOX"},
	}
	got := BuildMailboxSet(in)
	// IMAP names are case-sensitive; both should be present.
	if len(got) != 2 {
		t.Errorf("expected case-sensitive distinct entries, got %v", got)
	}
}

func TestLabelsRootConstant(t *testing.T) {
	if LabelsRoot != "Labels" {
		t.Errorf("LabelsRoot=%q, want %q", LabelsRoot, "Labels")
	}
}
