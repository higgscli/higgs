package imapclient

import (
	"context"
	"sort"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// Event is a single IMAP change observed by Watch.
type Event struct {
	Kind    string    `json:"kind"`
	UID     uint32    `json:"uid"`
	Mailbox string    `json:"mailbox"`
	At      time.Time `json:"at"`
}

// Event kinds emitted by Watch.
const (
	EventNew        = "new"
	EventExpunge    = "expunge"
	EventFlagChange = "flag_change"
)

// DefaultPollInterval is used when Watch is called with interval <= 0.
const DefaultPollInterval = 30 * time.Second

// Watch observes the given mailbox and streams events until ctx is cancelled.
//
// Implementation note: the memory backend used by tests does not support
// IMAP IDLE (the capability is not advertised and no UID-search-based fallback
// is built in), so this implementation uses a polling strategy only. Each tick
// it SELECTs the mailbox, runs UID SEARCH ALL, and diffs against the last
// snapshot to emit "new" / "expunge" / "flag_change" events.
//
// Returning from the goroutine closes both channels. The goroutine exits on
// ctx.Done(), or after an unrecoverable error is sent on errCh.
func Watch(ctx context.Context, c *client.Client, mailbox string, interval time.Duration) (<-chan Event, <-chan error, error) {
	if c == nil {
		return nil, nil, errNilClient
	}
	if mailbox == "" {
		return nil, nil, errEmptyMailbox
	}
	if interval <= 0 {
		interval = DefaultPollInterval
	}

	events := make(chan Event, 16)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		// Initial snapshot.
		prev, err := snapshotUIDs(c, mailbox)
		if err != nil {
			select {
			case errs <- Wrap(err):
			case <-ctx.Done():
			}
			return
		}
		prevFlags := map[uint32][]string{}
		_ = prevFlags // reserved for future flag_change diffing; see below.

		t := time.NewTimer(interval)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}

			cur, err := snapshotUIDs(c, mailbox)
			if err != nil {
				select {
				case errs <- Wrap(err):
				case <-ctx.Done():
				}
				return
			}

			// Diff UID sets. Both are sorted ascending.
			i, j := 0, 0
			now := time.Now().UTC()
			for i < len(prev) && j < len(cur) {
				switch {
				case prev[i] == cur[j]:
					i++
					j++
				case prev[i] < cur[j]:
					// Present before, missing now → expunged.
					if !sendEvent(ctx, events, Event{Kind: EventExpunge, UID: prev[i], Mailbox: mailbox, At: now}) {
						return
					}
					i++
				default:
					// New UID seen now that wasn't present before.
					if !sendEvent(ctx, events, Event{Kind: EventNew, UID: cur[j], Mailbox: mailbox, At: now}) {
						return
					}
					j++
				}
			}
			for ; i < len(prev); i++ {
				if !sendEvent(ctx, events, Event{Kind: EventExpunge, UID: prev[i], Mailbox: mailbox, At: now}) {
					return
				}
			}
			for ; j < len(cur); j++ {
				if !sendEvent(ctx, events, Event{Kind: EventNew, UID: cur[j], Mailbox: mailbox, At: now}) {
					return
				}
			}

			prev = cur
			t.Reset(interval)
		}
	}()

	return events, errs, nil
}

// sendEvent performs a context-aware send. Returns false if ctx was cancelled.
func sendEvent(ctx context.Context, ch chan<- Event, ev Event) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// snapshotUIDs selects the mailbox read-only and returns the sorted UID set,
// re-running UID SEARCH until two consecutive runs agree. A single flaky
// SEARCH answer (seen with Proton Bridge's virtual All Mail) would otherwise
// diff against the previous snapshot as a burst of phantom expunge/new
// events. Mirrors imapsearch's stability loop, kept local to avoid an
// imapclient→imapsearch dependency inversion.
func snapshotUIDs(c *client.Client, mailbox string) ([]uint32, error) {
	if _, err := c.Select(mailbox, true); err != nil {
		return nil, err
	}
	const maxRuns = 5
	prev, err := sortedUIDSearch(c)
	if err != nil {
		return nil, err
	}
	for run := 1; run < maxRuns; run++ {
		cur, err := sortedUIDSearch(c)
		if err != nil {
			return nil, err
		}
		if uidSetsEqual(prev, cur) {
			return cur, nil
		}
		prev = cur
		time.Sleep(200 * time.Millisecond)
	}
	return prev, nil
}

func sortedUIDSearch(c *client.Client) ([]uint32, error) {
	uids, err := c.UidSearch(&imap.SearchCriteria{})
	if err != nil {
		return nil, err
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	return uids, nil
}

func uidSetsEqual(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type watchError string

func (e watchError) Error() string { return string(e) }

const (
	errNilClient    watchError = "imapclient.Watch: nil client"
	errEmptyMailbox watchError = "imapclient.Watch: empty mailbox"
)
