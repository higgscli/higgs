package imapclient

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/higgscli/higgs/internal/imaptest"
)

func TestWatch_ValidationErrors(t *testing.T) {
	_, _, err := Watch(context.Background(), nil, "INBOX", 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{{RFC822: idleTestMsg("S0", "a@x.com", 1)}}))
	c, err := Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer CloseAndLogout(c)
	if _, _, err := Watch(context.Background(), c, "", 10*time.Millisecond); err == nil {
		t.Fatal("expected error for empty mailbox")
	}
}

func TestWatch_DetectsNewMessage(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: idleTestMsg("S0", "a@x.com", 1)},
	}))
	cfg := imaptest.Config(srv)

	watcher, err := Dial(cfg)
	if err != nil {
		t.Fatalf("dial watcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	events, errs, err := Watch(ctx, watcher, "INBOX", 30*time.Millisecond)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	// Append a new message via a second client.
	appender, err := Dial(cfg)
	if err != nil {
		t.Fatalf("dial appender: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := appender.Append("INBOX", nil, time.Now(), bytes.NewReader(idleTestMsg("S1", "b@x.com", 2))); err != nil {
		t.Fatalf("append: %v", err)
	}
	CloseAndLogout(appender)

	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("events channel closed before event arrived")
		}
		if ev.Kind != EventNew {
			t.Errorf("expected kind=new, got %q", ev.Kind)
		}
		if ev.Mailbox != "INBOX" {
			t.Errorf("expected mailbox=INBOX, got %q", ev.Mailbox)
		}
		if ev.UID == 0 {
			t.Errorf("expected non-zero UID")
		}
		if ev.At.IsZero() {
			t.Errorf("expected non-zero At")
		}
	case e := <-errs:
		t.Fatalf("watch errored: %v", e)
	case <-ctx.Done():
		t.Fatal("timed out waiting for new event")
	}

	cancel()
	if !drainEvents(events, errs, 2*time.Second) {
		t.Error("channels did not close after context cancel")
	}
	CloseAndLogout(watcher)
}

func TestWatch_DetectsExpunge(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: idleTestMsg("A", "a@x.com", 1)},
		{RFC822: idleTestMsg("B", "b@x.com", 2)},
	}))
	cfg := imaptest.Config(srv)
	watcher, err := Dial(cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	events, errs, err := Watch(ctx, watcher, "INBOX", 30*time.Millisecond)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	mutator, err := Dial(cfg)
	if err != nil {
		t.Fatalf("dial mutator: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := mutator.Select("INBOX", false); err != nil {
		t.Fatalf("select: %v", err)
	}
	// Snapshot the mailbox's actual UIDs and expunge the first one.
	uids, err := mutator.UidSearch(&imap.SearchCriteria{})
	if err != nil || len(uids) == 0 {
		t.Fatalf("uidSearch: %v (uids=%v)", err, uids)
	}
	if err := expungeUID(mutator, uids[0]); err != nil {
		t.Fatalf("expunge: %v", err)
	}
	CloseAndLogout(mutator)

	seenExpunge := false
	deadline := time.After(2 * time.Second)
LOOP:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break LOOP
			}
			if ev.Kind == EventExpunge {
				seenExpunge = true
				break LOOP
			}
		case err := <-errs:
			if err != nil {
				t.Fatalf("watch errored: %v", err)
			}
		case <-deadline:
			break LOOP
		}
	}
	// Cancel Watch and wait for its goroutine to exit before closing the client,
	// otherwise CloseAndLogout races with the in-flight UidSearch.
	cancel()
	drainEvents(events, errs, 2*time.Second)
	CloseAndLogout(watcher)
	if !seenExpunge {
		t.Error("expected to see an expunge event")
	}
}

func TestWatch_IntervalDefaultAndClean(t *testing.T) {
	// Verify interval<=0 default path and clean exit on ctx cancel.
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", nil))
	c, err := Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	events, errs, err := Watch(ctx, c, "INBOX", 0)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	cancel()
	if !drainEvents(events, errs, 2*time.Second) {
		t.Error("expected channels to close after cancel")
	}
	CloseAndLogout(c)
}

// --- helpers -----------------------------------------------------------------

func drainEvents(events <-chan Event, errs <-chan error, d time.Duration) bool {
	deadline := time.After(d)
	eventsClosed, errsClosed := false, false
	for !eventsClosed || !errsClosed {
		select {
		case _, ok := <-events:
			if !ok {
				eventsClosed = true
				events = nil
			}
		case _, ok := <-errs:
			if !ok {
				errsClosed = true
				errs = nil
			}
		case <-deadline:
			return false
		}
	}
	return true
}

func idleTestMsg(subject, from string, day int) []byte {
	dd := byte('0' + day%10)
	first := byte('0' + day/10)
	return []byte("From: " + from + "\r\n" +
		"To: u@x.com\r\n" +
		"Subject: " + subject + "\r\n" +
		"Date: Wed, " + string(first) + string(dd) + " Jan 2026 00:00:00 +0000\r\n" +
		"Message-ID: <" + subject + "@idle>\r\n" +
		"\r\nhi\r\n")
}

func expungeUID(c *client.Client, uid uint32) error {
	seqSet := &imap.SeqSet{}
	seqSet.AddNum(uid)
	if err := c.UidStore(seqSet, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.DeletedFlag}, nil); err != nil {
		return err
	}
	return c.Expunge(nil)
}
