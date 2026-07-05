package imapfetch

import (
	"errors"
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

// partialFetchMailbox forwards only the first FETCH result then fails, once
// armed — a connection drop or server error mid-FETCH. The client still
// receives the partial results before the tagged error arrives.
type partialFetchMailbox struct {
	backend.Mailbox
	armed *atomic.Bool
}

func (m *partialFetchMailbox) ListMessages(uid bool, seq *imap.SeqSet, items []imap.FetchItem, ch chan<- *imap.Message) error {
	if !m.armed.Load() {
		return m.Mailbox.ListMessages(uid, seq, items, ch)
	}
	defer close(ch)
	inner := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() { done <- m.Mailbox.ListMessages(uid, seq, items, inner) }()
	forwarded := 0
	for msg := range inner {
		if forwarded == 0 {
			ch <- msg
			forwarded++
		}
	}
	if err := <-done; err != nil {
		return err
	}
	return errors.New("simulated connection drop mid-FETCH")
}

func dialSelected(t *testing.T, srv *imaptest.Server, mailbox string) *imapclient.Client {
	t.Helper()
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { imapclient.CloseAndLogout(c) })
	if _, err := SelectMailbox(c, mailbox); err != nil {
		t.Fatalf("select: %v", err)
	}
	return c
}

func TestFetchRFC822PropagatesMidFetchError(t *testing.T) {
	var fail atomic.Bool
	srv := imaptest.Start(t,
		imaptest.WithMailbox("INBOX", []imaptest.Message{
			{RFC822: seedMsg("A")},
			{RFC822: seedMsg("B")},
			{RFC822: seedMsg("C")},
		}),
		imaptest.WithMailboxWrapper(func(m backend.Mailbox) backend.Mailbox {
			return &partialFetchMailbox{Mailbox: m, armed: &fail}
		}),
	)
	c := dialSelected(t, srv, "INBOX")
	fail.Store(true)

	msgs, err := FetchRFC822(c, []uint32{1, 2, 3})
	if err == nil {
		t.Fatalf("FETCH failed mid-stream yet FetchRFC822 returned nil error with %d of 3 messages — callers will treat a truncated mailbox read as complete", len(msgs))
	}
}

func TestMissingUIDs(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: seedMsg("A")},
		{RFC822: seedMsg("B")},
	}))
	c := dialSelected(t, srv, "INBOX")

	msgs, err := FetchRFC822(c, []uint32{1, 2, 999})
	if err != nil {
		t.Fatalf("FetchRFC822: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	missing := MissingUIDs([]uint32{1, 2, 999}, msgs)
	if len(missing) != 1 || missing[0] != 999 {
		t.Errorf("MissingUIDs = %v, want [999]", missing)
	}
	if got := MissingUIDs([]uint32{1, 2}, msgs); len(got) != 0 {
		t.Errorf("MissingUIDs with full fetch = %v, want empty", got)
	}
}
