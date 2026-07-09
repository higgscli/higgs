package imapwrite

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"

	"github.com/higgscli/higgs/internal/imaptest"
)

// gluonEmptySearchMailbox reproduces a Proton Bridge (gluon) quirk: a SEARCH
// whose criteria reference UIDs answers "NO no such message" when the
// selected mailbox is empty, instead of returning no matches — gluon resolves
// UID search keys against the mailbox snapshot, and resolution errors when
// the snapshot holds no messages (snapMsgList.resolveUID). Criteria without a
// UID set (e.g. ALL) still succeed on an empty mailbox.
type gluonEmptySearchMailbox struct {
	backend.Mailbox
}

func (m *gluonEmptySearchMailbox) SearchMessages(uid bool, criteria *imap.SearchCriteria) ([]uint32, error) {
	if criteria != nil && criteria.Uid != nil {
		status, err := m.Status([]imap.StatusItem{imap.StatusMessages})
		if err != nil {
			return nil, err
		}
		if status.Messages == 0 {
			return nil, errors.New("no such message")
		}
	}
	return m.Mailbox.SearchMessages(uid, criteria)
}

func startGluonQuirkServer(t *testing.T, src string, msgs []imaptest.Message) *imaptest.Server {
	t.Helper()
	return imaptest.Start(t,
		imaptest.WithMailbox(src, msgs),
		imaptest.WithMailbox("Trash", nil),
		imaptest.WithMailboxWrapper(func(m backend.Mailbox) backend.Mailbox {
			return &gluonEmptySearchMailbox{Mailbox: m}
		}),
	)
}

// Moving the last message out of a mailbox leaves it empty; the verification
// UID SEARCH then hits the quirk. That is the expected post-move state, so
// MoveVerified must report the move as confirmed, not as a failure.
func TestMoveVerifiedLastMessageEmptiesSource(t *testing.T) {
	srv := startGluonQuirkServer(t, "Labels/Needs-Reply-Test", []imaptest.Message{
		{RFC822: seedMsg("only")},
	})
	c := dial(t, srv)
	res, err := MoveVerified(c, "Labels/Needs-Reply-Test", "Trash", []uint32{1})
	if err != nil {
		t.Fatalf("MoveVerified must tolerate the empty-mailbox SEARCH error: %v", err)
	}
	if len(res.Moved) != 1 || res.Moved[0] != 1 {
		t.Errorf("moved=%v, want [1]", res.Moved)
	}
	if len(res.Failed) != 0 {
		t.Errorf("failed=%v, want none", res.Failed)
	}
	// The message must actually be in Trash.
	status, err := c.Select("Trash", true)
	if err != nil {
		t.Fatalf("select Trash: %v", err)
	}
	if status.Messages != 1 {
		t.Errorf("Trash has %d messages, want 1", status.Messages)
	}
}

// Draining a multi-message mailbox in one call ends with the same empty
// source; every UID must still be confirmed moved.
func TestMoveVerifiedDrainsMailbox(t *testing.T) {
	srv := startGluonQuirkServer(t, "INBOX", []imaptest.Message{
		{RFC822: seedMsg("a")},
		{RFC822: seedMsg("b")},
		{RFC822: seedMsg("c")},
	})
	c := dial(t, srv)
	res, err := MoveVerified(c, "INBOX", "Trash", []uint32{1, 2, 3})
	if err != nil {
		t.Fatalf("MoveVerified: %v", err)
	}
	if len(res.Moved) != 3 {
		t.Errorf("moved=%v, want all three", res.Moved)
	}
	if len(res.Failed) != 0 {
		t.Errorf("failed=%v, want none", res.Failed)
	}
}

// A flag change targeting UIDs in an empty mailbox hits the same quirk during
// verification. The UIDs are simply not present: they must land in Failed
// per-UID rather than aborting the whole operation with a hard error.
func TestSetFlagVerifiedEmptyMailbox(t *testing.T) {
	srv := startGluonQuirkServer(t, "INBOX", nil)
	c := dial(t, srv)
	res, err := SetFlagVerified(c, "INBOX", []uint32{1, 2}, imap.FlaggedFlag, true)
	if err != nil {
		t.Fatalf("SetFlagVerified must tolerate the empty-mailbox SEARCH error: %v", err)
	}
	if len(res.Updated) != 0 {
		t.Errorf("updated=%v, want none in an empty mailbox", res.Updated)
	}
	if len(res.Failed) != 2 {
		t.Errorf("failed=%v, want both UIDs", res.Failed)
	}
}

// A genuine SEARCH failure on a non-empty mailbox must still surface as an
// error: the tolerance is scoped to the provably-empty case only.
type brokenSearchMailbox struct {
	backend.Mailbox
	armed *atomic.Bool
}

func (m *brokenSearchMailbox) SearchMessages(uid bool, criteria *imap.SearchCriteria) ([]uint32, error) {
	if m.armed.Load() {
		return nil, errors.New("search exploded")
	}
	return m.Mailbox.SearchMessages(uid, criteria)
}

func TestMoveVerifiedStillSurfacesRealSearchErrors(t *testing.T) {
	var explode atomic.Bool
	srv := imaptest.Start(t,
		imaptest.WithMailbox("INBOX", []imaptest.Message{
			{RFC822: seedMsg("a")},
			{RFC822: seedMsg("b")},
		}),
		imaptest.WithMailbox("Trash", nil),
		imaptest.WithMailboxWrapper(func(m backend.Mailbox) backend.Mailbox {
			return &brokenSearchMailbox{Mailbox: m, armed: &explode}
		}),
	)
	explode.Store(true)
	c := dial(t, srv)
	// Move only one of two messages: the source stays non-empty, so the
	// failing verification SEARCH cannot be explained away and must error.
	_, err := MoveVerified(c, "INBOX", "Trash", []uint32{1})
	if err == nil {
		t.Fatal("expected verification error when SEARCH fails on a non-empty mailbox")
	}
}
