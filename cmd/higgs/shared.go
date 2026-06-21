package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/emersion/go-imap"

	"github.com/higgscli/higgs/internal/email"
	"github.com/higgscli/higgs/internal/imapfetch"
)

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
