package imapwrite

import (
	"fmt"
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

func TestMoveVerifiedHappy(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: seedMsg("A")},
		{RFC822: seedMsg("B")},
	}))
	c := dial(t, srv)
	if err := c.Create("Archive"); err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := MoveVerified(c, "INBOX", "Archive", []uint32{1, 2})
	if err != nil {
		t.Fatalf("MoveVerified: %v", err)
	}
	if len(res.Moved) != 2 || len(res.Failed) != 0 {
		t.Fatalf("got moved=%v failed=%v, want 2 moved 0 failed", res.Moved, res.Failed)
	}
	remaining, err := presentUIDs(c, []uint32{1, 2})
	if err != nil {
		t.Fatalf("presentUIDs: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("messages still present in INBOX after verified move: %v", remaining)
	}
}

func TestMoveVerifiedChunks(t *testing.T) {
	n := MoveChunkSize + 10
	msgs := make([]imaptest.Message, n)
	for i := range msgs {
		msgs[i] = imaptest.Message{RFC822: seedMsg(fmt.Sprintf("m%d", i))}
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", msgs))
	c := dial(t, srv)
	if err := c.Create("Archive"); err != nil {
		t.Fatalf("create: %v", err)
	}
	uids := make([]uint32, n)
	for i := range uids {
		uids[i] = uint32(i + 1)
	}
	res, err := MoveVerified(c, "INBOX", "Archive", uids)
	if err != nil {
		t.Fatalf("MoveVerified: %v", err)
	}
	if len(res.Moved) != n || len(res.Failed) != 0 {
		t.Fatalf("got %d moved %d failed, want %d moved 0 failed", len(res.Moved), len(res.Failed), n)
	}
	remaining, err := presentUIDs(c, uids)
	if err != nil {
		t.Fatalf("presentUIDs: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("%d messages still present in INBOX after verified move", len(remaining))
	}
}

func TestMoveVerifiedReportsFailures(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: seedMsg("A")},
		{RFC822: seedMsg("B")},
	}))
	c := dial(t, srv)
	// Destination does not exist: every attempt is rejected, so verification
	// must report the messages as still present rather than moved.
	res, err := MoveVerified(c, "INBOX", "NoSuchMailbox", []uint32{1, 2})
	if err != nil {
		t.Fatalf("MoveVerified should recover from per-chunk errors, got %v", err)
	}
	if len(res.Moved) != 0 || len(res.Failed) != 2 {
		t.Fatalf("got moved=%v failed=%v, want 0 moved 2 failed", res.Moved, res.Failed)
	}
	if err := Move(c, "INBOX", "NoSuchMailbox", []uint32{1, 2}); err == nil {
		t.Error("Move should report messages still present in source")
	}
}

func TestSubtractUIDs(t *testing.T) {
	got := subtractUIDs([]uint32{1, 2, 3, 4}, []uint32{2, 4})
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("subtractUIDs = %v, want [1 3]", got)
	}
	all := []uint32{5, 6}
	if got := subtractUIDs(all, nil); len(got) != 2 {
		t.Errorf("subtract nil = %v, want unchanged", got)
	}
}

func TestStoreOpString(t *testing.T) {
	if storeOpString(true) != "+" || storeOpString(false) != "-" {
		t.Errorf("unexpected op strings")
	}
}
