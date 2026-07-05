package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/emersion/go-imap"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/email"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/termio"
)

// reportMissingUIDs emits a "type":"error" row for each requested UID absent
// from a FETCH result and returns how many there were. Servers return nothing
// for UIDs that don't exist, so commands reporting per-UID outcomes must
// account for them or they silently vanish from the output.
func reportMissingUIDs(w *termio.Writer, mailbox string, requested []uint32, fetched []imapfetch.FetchedMessage) (int, error) {
	missing := imapfetch.MissingUIDs(requested, fetched)
	for _, uid := range missing {
		env := cerr.Validation("uid %d not found in %q", uid, mailbox).ToEnvelope()["error"]
		if err := w.PrintNDJSON(map[string]any{
			"type": "error", "uid": uid, "mailbox": mailbox, "error": env,
		}); err != nil {
			return len(missing), cerr.Internal(err, "print")
		}
	}
	return len(missing), nil
}

// envelopeFrom extracts the first From address string from an IMAP envelope.
func envelopeFrom(env *imap.Envelope) string {
	if env == nil || len(env.From) == 0 {
		return ""
	}
	return env.From[0].Address()
}

// fetchedToMessage converts a fetched IMAP message into the email.Message
// shape used by the classifier.
func fetchedToMessage(f *imapfetch.FetchedMessage, body, bodySnippet, mailbox string, uidValidity uint32) email.Message {
	msg := email.Message{
		Mailbox:     mailbox,
		UIDValidity: uidValidity,
		UID:         f.UID,
		Body:        body,
		BodySnippet: bodySnippet,
	}
	if f.Envelope != nil {
		msg.Subject = f.Envelope.Subject
		msg.From = envelopeFrom(f.Envelope)
		msg.Date = f.Envelope.Date
		msg.MessageID = f.Envelope.MessageId
	}
	return msg
}

// truncate shortens s to n characters, appending an ellipsis when truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// getEnvInt reads an int env var, returning defaultVal when unset or malformed.
func getEnvInt(key string, defaultVal int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}
