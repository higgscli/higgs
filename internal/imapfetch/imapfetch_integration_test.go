package imapfetch_test

import (
	"testing"
	"time"

	"github.com/akeemjenkins/protoncli/internal/imapclient"
	"github.com/akeemjenkins/protoncli/internal/imapfetch"
	"github.com/akeemjenkins/protoncli/internal/imaptest"
)

func rfc822(subject, from string) []byte {
	return []byte(
		"From: " + from + "\r\n" +
			"To: user@example.com\r\n" +
			"Subject: " + subject + "\r\n" +
			"Date: Mon, 01 Jan 2024 00:00:00 +0000\r\n" +
			"Message-ID: <" + subject + "@test>\r\n" +
			"Content-Type: text/plain\r\n" +
			"\r\n" +
			"body for " + subject + "\r\n")
}

func TestSelectMailbox_And_FetchAllUIDs(t *testing.T) {
	seeded := []imaptest.Message{
		{RFC822: rfc822("one", "a@x.com"), Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		{RFC822: rfc822("two", "b@x.com"), Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
		{RFC822: rfc822("three", "c@x.com"), Date: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)},
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", seeded))

	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)

	snap, err := imapfetch.SelectMailbox(c, "INBOX")
	if err != nil {
		t.Fatalf("SelectMailbox: %v", err)
	}
	if snap.Name != "INBOX" {
		t.Errorf("snap.Name = %q, want INBOX", snap.Name)
	}
	if snap.UIDValidity == 0 {
		t.Error("UIDValidity should be non-zero for a live mailbox")
	}

	// No time filter.
	uids, err := imapfetch.SearchUIDs(c, time.Time{}, false)
	if err != nil {
		t.Fatalf("SearchUIDs: %v", err)
	}
	// imaptest purges the memory backend's default INBOX message before
	// seeding, so only our 3 seeded messages remain (UIDs 1..3).
	if len(uids) != 3 {
		t.Fatalf("SearchUIDs = %v (len %d); want 3", uids, len(uids))
	}

	msgs, err := imapfetch.FetchRFC822(c, uids)
	if err != nil {
		t.Fatalf("FetchRFC822: %v", err)
	}
	if len(msgs) != len(uids) {
		t.Errorf("FetchRFC822 returned %d messages for %d uids", len(msgs), len(uids))
	}
	for _, m := range msgs {
		if len(m.RFC822) == 0 {
			t.Errorf("uid=%d: empty RFC822 body", m.UID)
		}
		if m.Envelope == nil {
			t.Errorf("uid=%d: nil envelope", m.UID)
		}
	}
}

func TestSearchUIDs_SinceFilter_AndUnseen(t *testing.T) {
	// Seed three messages with different dates so the SentSince filter has
	// something to drop. Flags include \\Seen on message 2 so unseenOnly
	// takes a different path.
	old := []imaptest.Message{
		{RFC822: rfc822("old", "a@x.com"), Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	recent := []imaptest.Message{
		{RFC822: rfc822("recent1", "b@x.com"), Date: time.Now(), Flags: []string{"\\Seen"}},
		{RFC822: rfc822("recent2", "c@x.com"), Date: time.Now()},
	}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("INBOX", old),
		imaptest.WithMailbox("INBOX", recent),
	)

	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)

	if _, err := imapfetch.SelectMailbox(c, "INBOX"); err != nil {
		t.Fatalf("SelectMailbox: %v", err)
	}

	// Since filter.
	if _, err := imapfetch.SearchUIDs(c, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), false); err != nil {
		t.Fatalf("SearchUIDs(since): %v", err)
	}
	// unseenOnly.
	if _, err := imapfetch.SearchUIDs(c, time.Time{}, true); err != nil {
		t.Fatalf("SearchUIDs(unseenOnly): %v", err)
	}
}

func TestFetchRFC822_Empty(t *testing.T) {
	srv := imaptest.Start(t)
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)

	msgs, err := imapfetch.FetchRFC822(c, nil)
	if err != nil {
		t.Fatalf("FetchRFC822(nil): %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 msgs, got %d", len(msgs))
	}
}

func TestSelectMailbox_BadName(t *testing.T) {
	srv := imaptest.Start(t)
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)

	if _, err := imapfetch.SelectMailbox(c, "NoSuchMailbox"); err == nil {
		t.Error("expected SelectMailbox to fail for missing mailbox")
	}
}
