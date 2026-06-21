package imapwrite

import (
	"testing"

	"github.com/emersion/go-imap"

	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaptest"
)

func seedMsg(subject string) []byte {
	return []byte("From: a@x.com\r\nTo: b@x.com\r\nSubject: " + subject +
		"\r\nDate: Wed, 01 Jan 2026 00:00:00 +0000\r\nMessage-ID: <" + subject + "@t>\r\n\r\nbody\r\n")
}

func dial(t *testing.T, srv *imaptest.Server) *imapclient.Client {
	t.Helper()
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { imapclient.CloseAndLogout(c) })
	return c
}

func TestMarkReadAndSeen(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: seedMsg("one")},
	}))
	c := dial(t, srv)
	if _, err := c.Select("INBOX", false); err != nil {
		t.Fatalf("select: %v", err)
	}
	if err := MarkRead(c, "INBOX", []uint32{1, 2}, true); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if err := MarkRead(c, "INBOX", []uint32{1, 2}, false); err != nil {
		t.Fatalf("MarkRead unread: %v", err)
	}
	if err := MarkRead(c, "INBOX", nil, true); err != nil {
		t.Errorf("nil uids should be a no-op, got %v", err)
	}
}

func TestSetFlagValidation(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{{RFC822: seedMsg("x")}}))
	c := dial(t, srv)
	if err := SetFlag(c, "INBOX", []uint32{1}, "   ", true); err == nil {
		t.Error("expected error on empty flag")
	}
}

func TestSetFlagCustomKeyword(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{{RFC822: seedMsg("x")}}))
	c := dial(t, srv)
	if err := SetFlag(c, "INBOX", []uint32{1, 2}, imap.FlaggedFlag, true); err != nil {
		t.Fatalf("set flagged: %v", err)
	}
}

func TestCopyMoveArchiveTrash(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: seedMsg("A")},
		{RFC822: seedMsg("B")},
	}))
	c := dial(t, srv)
	if err := c.Create("Archive"); err != nil {
		t.Fatalf("create Archive: %v", err)
	}
	if err := c.Create("Trash"); err != nil {
		t.Fatalf("create Trash: %v", err)
	}
	if err := c.Create("Custom"); err != nil {
		t.Fatalf("create Custom: %v", err)
	}

	if err := Copy(c, "INBOX", "Custom", []uint32{1}); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := Copy(c, "INBOX", "Custom", nil); err != nil {
		t.Errorf("copy nil should no-op, got %v", err)
	}

	if err := Move(c, "INBOX", "Custom", []uint32{1, 2}); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := Move(c, "INBOX", "Custom", nil); err != nil {
		t.Errorf("move nil should no-op, got %v", err)
	}
}

func TestArchiveDefault(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{{RFC822: seedMsg("x")}}))
	c := dial(t, srv)
	if err := c.Create("Archive"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := Archive(c, "INBOX", []uint32{1, 2}, ""); err != nil {
		t.Fatalf("archive: %v", err)
	}
}

func TestTrashDefault(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{{RFC822: seedMsg("x")}}))
	c := dial(t, srv)
	if err := c.Create("Trash"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := Trash(c, "INBOX", []uint32{1, 2}, ""); err != nil {
		t.Fatalf("trash: %v", err)
	}
}

func TestStoreOpString(t *testing.T) {
	if storeOpString(true) != "+" || storeOpString(false) != "-" {
		t.Errorf("unexpected op strings")
	}
}
