// Package imapfetch fetches and decodes messages from IMAP mailboxes.
package imapfetch

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// FetchedMessage holds a single message fetched via IMAP (go-imap v1, same as Proton Bridge).
type FetchedMessage struct {
	UID      uint32
	Envelope *imap.Envelope
	RFC822   []byte
}

// MailboxSnapshot holds mailbox state after SELECT (read-only).
type MailboxSnapshot struct {
	Name        string
	UIDValidity uint32
}

// SelectMailbox selects the mailbox read-only. Same pattern as proton-bridge/tests/imap_test.go clientFetch.
func SelectMailbox(c *client.Client, mailbox string) (MailboxSnapshot, error) {
	status, err := c.Select(mailbox, true)
	if err != nil {
		return MailboxSnapshot{}, err
	}
	return MailboxSnapshot{Name: mailbox, UIDValidity: status.UidValidity}, nil
}

// SearchUIDs returns UIDs matching criteria. Same SearchCriteria as go-imap v1; Bridge uses UidSearch.
func SearchUIDs(c *client.Client, since time.Time, unseenOnly bool) ([]uint32, error) {
	crit := &imap.SearchCriteria{}
	if !since.IsZero() {
		crit.SentSince = since
	}
	if unseenOnly {
		crit.WithoutFlags = []string{imap.SeenFlag}
	}
	uids, err := c.UidSearch(crit)
	if err != nil {
		return nil, err
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	return uids, nil
}

// FetchRFC822 fetches full RFC822 (BODY.PEEK[]) for the given UIDs. Same items as Bridge clientFetch.
func FetchRFC822(c *client.Client, uids []uint32) ([]FetchedMessage, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	seqSet := &imap.SeqSet{}
	seqSet.AddNum(uids...)
	resCh := make(chan *imap.Message, len(uids))
	go func() {
		if err := c.UidFetch(seqSet, []imap.FetchItem{imap.FetchFlags, imap.FetchEnvelope, imap.FetchUid, "BODY.PEEK[]"}, resCh); err != nil {
			// Channel is closed by client after send; if err is set, we still get partial results
			_ = err
		}
	}()

	var msgs []*imap.Message
	for m := range resCh {
		msgs = append(msgs, m)
	}

	out := make([]FetchedMessage, 0, len(msgs))
	section, err := imap.ParseBodySectionName("BODY[]")
	if err != nil {
		return nil, fmt.Errorf("BODY[] section: %w", err)
	}
	for _, m := range msgs {
		lit := m.GetBody(section)
		if lit == nil {
			return nil, fmt.Errorf("FETCH did not include BODY[] for uid=%d", m.Uid)
		}
		raw, err := io.ReadAll(lit)
		if err != nil {
			return nil, fmt.Errorf("read body uid=%d: %w", m.Uid, err)
		}
		out = append(out, FetchedMessage{
			UID:      m.Uid,
			Envelope: m.Envelope,
			RFC822:   raw,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}
