package imapwrite

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"

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
	if err := MarkRead(c, "INBOX", []uint32{1}, true); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if err := MarkRead(c, "INBOX", []uint32{1}, false); err != nil {
		t.Fatalf("MarkRead unread: %v", err)
	}
	if err := MarkRead(c, "INBOX", nil, true); err != nil {
		t.Errorf("nil uids should be a no-op, got %v", err)
	}
	// A UID that doesn't exist must not be reported as marked.
	if err := MarkRead(c, "INBOX", []uint32{1, 2}, true); err == nil {
		t.Error("MarkRead with a nonexistent UID should error, not claim success")
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
	if err := SetFlag(c, "INBOX", []uint32{1}, imap.FlaggedFlag, true); err != nil {
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

// silentWriteMailbox acknowledges COPY/STORE/EXPUNGE without applying them
// once armed — the failure mode observed with Proton Bridge on large MOVE
// batches, where the server answers OK but leaves messages in place.
type silentWriteMailbox struct {
	backend.Mailbox
	armed *atomic.Bool
}

func (m *silentWriteMailbox) CopyMessages(uid bool, seq *imap.SeqSet, dest string) error {
	if m.armed.Load() {
		return nil
	}
	return m.Mailbox.CopyMessages(uid, seq, dest)
}

func (m *silentWriteMailbox) UpdateMessagesFlags(uid bool, seq *imap.SeqSet, op imap.FlagsOp, flags []string) error {
	if m.armed.Load() {
		return nil
	}
	return m.Mailbox.UpdateMessagesFlags(uid, seq, op, flags)
}

func (m *silentWriteMailbox) Expunge() error {
	if m.armed.Load() {
		return nil
	}
	return m.Mailbox.Expunge()
}

func TestMoveVerifiedDetectsSilentlyIgnoredWrites(t *testing.T) {
	var lie atomic.Bool
	srv := imaptest.Start(t,
		imaptest.WithMailbox("INBOX", []imaptest.Message{
			{RFC822: seedMsg("A")},
			{RFC822: seedMsg("B")},
		}),
		imaptest.WithMailbox("Archive", nil),
		imaptest.WithMailboxWrapper(func(m backend.Mailbox) backend.Mailbox {
			return &silentWriteMailbox{Mailbox: m, armed: &lie}
		}),
	)
	lie.Store(true)
	c := dial(t, srv)
	res, err := MoveVerified(c, "INBOX", "Archive", []uint32{1, 2})
	if err != nil {
		t.Fatalf("MoveVerified: %v", err)
	}
	if len(res.Moved) != 0 {
		t.Errorf("server never applied the writes, yet %v reported moved", res.Moved)
	}
	if len(res.Failed) != 2 {
		t.Errorf("failed=%v, want both UIDs", res.Failed)
	}
	if err := Move(c, "INBOX", "Archive", []uint32{1, 2}); err == nil {
		t.Error("Move must not report success when the server ignores writes")
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

func TestSetFlagVerifiedDetectsSilentlyIgnoredStore(t *testing.T) {
	var lie atomic.Bool
	srv := imaptest.Start(t,
		imaptest.WithMailbox("INBOX", []imaptest.Message{
			{RFC822: seedMsg("A")},
			{RFC822: seedMsg("B")},
		}),
		imaptest.WithMailboxWrapper(func(m backend.Mailbox) backend.Mailbox {
			return &silentWriteMailbox{Mailbox: m, armed: &lie}
		}),
	)
	lie.Store(true)
	c := dial(t, srv)
	res, err := SetFlagVerified(c, "INBOX", []uint32{1, 2}, imap.FlaggedFlag, true)
	if err != nil {
		t.Fatalf("SetFlagVerified: %v", err)
	}
	if len(res.Updated) != 0 {
		t.Errorf("server never applied the STORE, yet %v reported updated", res.Updated)
	}
	if len(res.Failed) != 2 {
		t.Errorf("failed=%v, want both UIDs", res.Failed)
	}
}

func TestSetFlagVerifiedReportsMissingUIDs(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: seedMsg("A")},
	}))
	c := dial(t, srv)
	res, err := SetFlagVerified(c, "INBOX", []uint32{1, 999}, imap.FlaggedFlag, true)
	if err != nil {
		t.Fatalf("SetFlagVerified: %v", err)
	}
	if len(res.Updated) != 1 || res.Updated[0] != 1 {
		t.Errorf("updated=%v, want [1]", res.Updated)
	}
	if len(res.Failed) != 1 || res.Failed[0] != 999 {
		t.Errorf("failed=%v, want [999] — a nonexistent UID must not be reported as flagged", res.Failed)
	}
}

func TestSetFlagVerifiedRemoveFlag(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: seedMsg("A"), Flags: []string{imap.SeenFlag}},
		{RFC822: seedMsg("B"), Flags: []string{imap.SeenFlag}},
	}))
	c := dial(t, srv)
	res, err := SetFlagVerified(c, "INBOX", []uint32{1, 2}, imap.SeenFlag, false)
	if err != nil {
		t.Fatalf("SetFlagVerified remove: %v", err)
	}
	if len(res.Updated) != 2 || len(res.Failed) != 0 {
		t.Errorf("updated=%v failed=%v, want both updated", res.Updated, res.Failed)
	}
}
