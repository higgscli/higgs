package main

import (
	"bytes"
	"time"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/email"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/parse"
	"github.com/higgscli/higgs/internal/termio"
)

// fetchRowAttachment is the subset of parse.Attachment surfaced on the
// fetch-and-parse NDJSON row. The attachments subcommand exposes the full
// metadata.
type fetchRowAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

// fetchRow embeds email.Message so existing fields keep their JSON key
// names and order; Attachments is an additive field always present (empty
// slice when there are none).
type fetchRow struct {
	email.Message
	Attachments []fetchRowAttachment `json:"attachments"`
}

// fetchRowWithAttachments walks the RFC822 for attachment metadata and
// returns the combined row. Walk errors are non-fatal: a warning is emitted
// and the row carries an empty attachments array.
func fetchRowWithAttachments(msg email.Message, rfc822 []byte) fetchRow {
	row := fetchRow{Message: msg, Attachments: []fetchRowAttachment{}}
	atts, err := parse.WalkAttachments(rfc822)
	if err != nil {
		termio.Warn("attachment walk failed for UID %d: %s", msg.UID, err.Error())
		return row
	}
	for _, a := range atts {
		row.Attachments = append(row.Attachments, fetchRowAttachment{
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Size:        a.Size,
			SHA256:      a.SHA256,
		})
	}
	return row
}

func newFetchAndParseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fetch-and-parse [mailbox]",
		Short: "Fetch messages and emit NDJSON (one JSON object per message)",
		Args:  cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,2,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mailbox := "INBOX"
			if len(args) > 0 {
				mailbox = args[0]
			}
			return cmdFetchAndParse(mailbox)
		},
	}
	return cmd
}

func cmdFetchAndParse(mailbox string) error {
	tio := termio.Default()

	termio.Info("Loading config from environment")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return cerr.Config("%s", err.Error())
	}

	c, err := imapclient.Dial(cfg.IMAP)
	if err != nil {
		return cerr.Auth("failed to connect/login IMAP: %s", err.Error())
	}
	defer imapclient.CloseAndLogout(c)

	requestedMB := mailbox
	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "LIST mailboxes")
	}
	mailbox, err = imaputil.ResolveMailboxName(mailbox, mboxes)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}
	if mailbox != requestedMB {
		termio.Info("Resolved mailbox %q to server name %q", requestedMB, mailbox)
	}
	termio.Info("Selecting mailbox %q (read-only)", mailbox)
	snap, err := imapfetch.SelectMailbox(c, mailbox)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "SELECT %q", mailbox)
	}
	termio.Info("Mailbox UIDVALIDITY=%d", snap.UIDValidity)

	uids, err := imapfetch.SearchUIDs(c, time.Time{}, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "UID SEARCH")
	}
	termio.Info("Found %d UIDs in mailbox", len(uids))

	summary := map[string]any{
		"type":    "summary",
		"mailbox": mailbox,
	}

	if len(uids) == 0 {
		summary["fetched"] = 0
		return tio.PrintNDJSON(summary)
	}
	const maxFetch = 5
	if len(uids) > maxFetch {
		uids = uids[len(uids)-maxFetch:]
		termio.Info("Limiting to last %d UIDs for dry run", maxFetch)
	}

	msgs, err := imapfetch.FetchRFC822(c, uids)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "FETCH")
	}
	termio.Info("Fetched %d messages", len(msgs))

	for _, m := range msgs {
		body, _ := parse.BestBodyText(bytes.NewReader(m.RFC822))
		snippet := parse.Snippet(body, 500)
		msg := fetchedToMessage(&m, body, snippet, mailbox, snap.UIDValidity)
		row := fetchRowWithAttachments(msg, m.RFC822)
		if err := tio.PrintNDJSON(row); err != nil {
			return cerr.Internal(err, "write NDJSON")
		}
	}
	summary["fetched"] = len(msgs)
	return tio.PrintNDJSON(summary)
}
