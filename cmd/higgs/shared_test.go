package main

import (
	"testing"
	"time"

	"github.com/emersion/go-imap"

	"github.com/higgscli/higgs/internal/imapfetch"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"empty", "", 5, ""},
		{"exact", "hello", 5, "hello"},
		{"shorter", "hi", 5, "hi"},
		{"over", "helloworld", 5, "hello..."},
		{"zero", "x", 0, "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.in, tt.n)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	const key = "PM_TEST_GETENV_INT"

	t.Run("unset", func(t *testing.T) {
		t.Setenv(key, "")
		if got := getEnvInt(key, 42); got != 42 {
			t.Errorf("unset default: got %d, want 42", got)
		}
	})

	t.Run("set", func(t *testing.T) {
		t.Setenv(key, "7")
		if got := getEnvInt(key, 42); got != 7 {
			t.Errorf("set: got %d, want 7", got)
		}
	})

	t.Run("set_negative", func(t *testing.T) {
		t.Setenv(key, "-3")
		if got := getEnvInt(key, 42); got != -3 {
			t.Errorf("negative: got %d, want -3", got)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		t.Setenv(key, "not-an-int")
		if got := getEnvInt(key, 42); got != 42 {
			t.Errorf("malformed: got %d, want 42", got)
		}
	})

	t.Run("whitespace_empty", func(t *testing.T) {
		t.Setenv(key, "   ")
		if got := getEnvInt(key, 13); got != 13 {
			t.Errorf("ws empty: got %d, want 13", got)
		}
	})
}

func TestEnvelopeFrom(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := envelopeFrom(nil); got != "" {
			t.Errorf("nil: got %q, want empty", got)
		}
	})

	t.Run("empty_from", func(t *testing.T) {
		env := &imap.Envelope{}
		if got := envelopeFrom(env); got != "" {
			t.Errorf("empty: got %q, want empty", got)
		}
	})

	t.Run("populated", func(t *testing.T) {
		env := &imap.Envelope{
			From: []*imap.Address{{MailboxName: "jane", HostName: "example.com"}},
		}
		got := envelopeFrom(env)
		want := "jane@example.com"
		if got != want {
			t.Errorf("populated: got %q, want %q", got, want)
		}
	})
}

func TestFetchedToMessage(t *testing.T) {
	t.Run("nil_envelope", func(t *testing.T) {
		f := &imapfetch.FetchedMessage{UID: 42}
		msg := fetchedToMessage(f, "body", "snip", "INBOX", 100)
		if msg.UID != 42 || msg.Mailbox != "INBOX" || msg.UIDValidity != 100 {
			t.Errorf("fields: %+v", msg)
		}
		if msg.Body != "body" || msg.BodySnippet != "snip" {
			t.Errorf("body fields: body=%q snippet=%q", msg.Body, msg.BodySnippet)
		}
		if msg.Subject != "" || msg.From != "" || msg.MessageID != "" {
			t.Errorf("non-nil envelope fields leaked: %+v", msg)
		}
	})

	t.Run("populated", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		f := &imapfetch.FetchedMessage{
			UID: 7,
			Envelope: &imap.Envelope{
				Subject:   "hi",
				Date:      now,
				MessageId: "<m@x>",
				From:      []*imap.Address{{MailboxName: "a", HostName: "b.com"}},
			},
		}
		msg := fetchedToMessage(f, "B", "S", "Folders/Accounts", 11)
		if msg.Subject != "hi" || msg.MessageID != "<m@x>" || msg.From != "a@b.com" {
			t.Errorf("fields: %+v", msg)
		}
		if !msg.Date.Equal(now) {
			t.Errorf("date: got %v, want %v", msg.Date, now)
		}
	})
}
