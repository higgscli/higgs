// Package email defines the normalized message shape shared across protoncli.
package email

import (
	"time"
)

type Message struct {
	Mailbox     string
	UIDValidity uint32
	UID         uint32

	MessageID string
	From      string
	Subject   string
	Date      time.Time

	Body        string
	BodySnippet string
}
