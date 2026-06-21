package imapsearch

import (
	"testing"
	"time"

	"github.com/emersion/go-imap"

	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaptest"
)

func ptrBool(b bool) *bool { return &b }

func TestBuild(t *testing.T) {
	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := Criteria{
		From: "alice@example.com", To: "bob@example.com", Cc: "carol@example.com",
		Subject: "hello", Body: "body", Text: "text",
		Since: since, Before: before, SentSince: since, SentBefore: before,
		LargerThan: 100, SmallerThan: 1000,
		Keywords: []string{"Important", ""}, Unkeywords: []string{"Junk"},
		Seen: ptrBool(true), Flagged: ptrBool(true),
		Answered: ptrBool(false), Deleted: ptrBool(false),
		Draft: ptrBool(false), Recent: ptrBool(false),
	}
	got := Build(c)
	if got.Header["From"][0] != "alice@example.com" {
		t.Errorf("From header not set: %v", got.Header)
	}
	if got.Header["Subject"][0] != "hello" {
		t.Errorf("Subject header not set")
	}
	if got.Body[0] != "body" || got.Text[0] != "text" {
		t.Errorf("Body/Text not set")
	}
	if !got.Since.Equal(since) || !got.Before.Equal(before) {
		t.Errorf("dates not set")
	}
	if got.Larger != 100 || got.Smaller != 1000 {
		t.Errorf("size filters not set")
	}
	wantWith := map[string]bool{"Important": true, imap.SeenFlag: true, imap.FlaggedFlag: true}
	for _, f := range got.WithFlags {
		if !wantWith[f] {
			t.Errorf("unexpected WithFlag: %q", f)
		}
	}
}

func TestBuildSeenFalse(t *testing.T) {
	got := Build(Criteria{Seen: ptrBool(false)})
	found := false
	for _, f := range got.WithoutFlags {
		if f == imap.SeenFlag {
			found = true
		}
	}
	if !found {
		t.Errorf("Seen=false should add to WithoutFlags, got %v", got.WithoutFlags)
	}
}

func TestBuildEmpty(t *testing.T) {
	got := Build(Criteria{})
	if got == nil {
		t.Fatal("Build(empty) returned nil")
	}
	if len(got.Header) != 0 || len(got.WithFlags) != 0 || len(got.WithoutFlags) != 0 {
		t.Errorf("empty criteria should produce empty SearchCriteria, got %+v", got)
	}
}

func TestOr(t *testing.T) {
	crit := Or(Criteria{Subject: "a"}, Criteria{Subject: "b"})
	if len(crit.Or) != 1 {
		t.Fatalf("expected one OR branch, got %d", len(crit.Or))
	}
}

func TestAppendUnique(t *testing.T) {
	s := appendUnique(nil, "A")
	s = appendUnique(s, "a")
	s = appendUnique(s, "B")
	if len(s) != 2 {
		t.Errorf("dedupe failed: %v", s)
	}
}

func seedMsg(subject, from string, day int) []byte {
	return []byte("From: " + from + "\r\n" +
		"To: user@example.com\r\n" +
		"Subject: " + subject + "\r\n" +
		"Date: Wed, " + pad(day) + " Jan 2026 12:00:00 +0000\r\n" +
		"Message-ID: <" + subject + "@test>\r\n" +
		"\r\nhello body here\r\n")
}

func pad(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestSearchIntegration(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: seedMsg("Hello", "alice@example.com", 1)},
		{RFC822: seedMsg("Ping", "bob@example.com", 2)},
		{RFC822: seedMsg("Pong", "alice@example.com", 3)},
	}))
	cfg := imaptest.Config(srv)
	c, err := imapclient.Dial(cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)
	if _, err := c.Select("INBOX", true); err != nil {
		t.Fatalf("select: %v", err)
	}
	matches, err := Search(c, Criteria{From: "alice@example.com"}, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches for alice, got %d: %+v", len(matches), matches)
	}
	for _, m := range matches {
		if m.UID == 0 {
			t.Errorf("UID zero")
		}
		if m.From != "alice@example.com" {
			t.Errorf("unexpected From: %q", m.From)
		}
	}
}

func TestSearchLimit(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: seedMsg("A", "x@x.com", 1)},
		{RFC822: seedMsg("B", "x@x.com", 2)},
		{RFC822: seedMsg("C", "x@x.com", 3)},
	}))
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)
	if _, err := c.Select("INBOX", true); err != nil {
		t.Fatalf("select: %v", err)
	}
	matches, err := Search(c, Criteria{}, 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 2 {
		t.Errorf("limit=2 should return 2 matches, got %d", len(matches))
	}
}

func TestSearchUIDs(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: seedMsg("S1", "x@x.com", 1)},
	}))
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)
	if _, err := c.Select("INBOX", true); err != nil {
		t.Fatalf("select: %v", err)
	}
	uids, err := SearchUIDs(c, Criteria{From: "x@x.com"})
	if err != nil {
		t.Fatalf("searchUIDs: %v", err)
	}
	if len(uids) != 1 {
		t.Errorf("expected 1 UID, got %v", uids)
	}
}
