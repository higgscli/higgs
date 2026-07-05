package imapsearch

import (
	"sync/atomic"
	"testing"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"

	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaptest"
)

// flakySearchMailbox answers the first SEARCH (once armed) with a truncated
// result, then answers honestly — the instability observed against Proton
// Bridge's virtual All Mail mailbox, where identical back-to-back queries
// returned 122 then 28 matches.
type flakySearchMailbox struct {
	backend.Mailbox
	armed *atomic.Bool
	calls *atomic.Int32
}

func (m *flakySearchMailbox) SearchMessages(uid bool, crit *imap.SearchCriteria) ([]uint32, error) {
	res, err := m.Mailbox.SearchMessages(uid, crit)
	if err != nil {
		return nil, err
	}
	if m.armed.Load() && m.calls.Add(1) == 1 && len(res) > 1 {
		return res[:1], nil
	}
	return res, nil
}

func TestSearchUIDsRetriesUntilStable(t *testing.T) {
	var armed atomic.Bool
	var calls atomic.Int32
	srv := imaptest.Start(t,
		imaptest.WithMailbox("INBOX", []imaptest.Message{
			{RFC822: []byte("From: a@x.com\r\nTo: b@x.com\r\nSubject: one\r\n\r\nbody\r\n")},
			{RFC822: []byte("From: a@x.com\r\nTo: b@x.com\r\nSubject: two\r\n\r\nbody\r\n")},
			{RFC822: []byte("From: a@x.com\r\nTo: b@x.com\r\nSubject: three\r\n\r\nbody\r\n")},
		}),
		imaptest.WithMailboxWrapper(func(m backend.Mailbox) backend.Mailbox {
			return &flakySearchMailbox{Mailbox: m, armed: &armed, calls: &calls}
		}),
	)
	armed.Store(true)
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { imapclient.CloseAndLogout(c) })
	if _, err := c.Select("INBOX", true); err != nil {
		t.Fatalf("select: %v", err)
	}

	uids, err := SearchUIDs(c, Criteria{})
	if err != nil {
		t.Fatalf("SearchUIDs: %v", err)
	}
	if len(uids) != 3 {
		t.Errorf("got %d UIDs %v, want the settled result of 3 — the flaky first answer must not be trusted", len(uids), uids)
	}
	if calls.Load() < 3 {
		t.Errorf("expected at least 3 search runs (flaky, then two agreeing), got %d", calls.Load())
	}
}

func TestEqualUIDs(t *testing.T) {
	cases := []struct {
		a, b []uint32
		want bool
	}{
		{nil, nil, true},
		{[]uint32{1, 2}, []uint32{1, 2}, true},
		{[]uint32{1, 2}, []uint32{1, 3}, false},
		{[]uint32{1}, []uint32{1, 2}, false},
		{nil, []uint32{1}, false},
	}
	for _, c := range cases {
		if got := equalUIDs(c.a, c.b); got != c.want {
			t.Errorf("equalUIDs(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
