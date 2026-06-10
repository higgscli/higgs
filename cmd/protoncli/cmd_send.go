package main

import (
	"strings"

	"github.com/emersion/go-imap"
	"github.com/spf13/cobra"

	"github.com/akeemjenkins/protoncli/internal/cerr"
	"github.com/akeemjenkins/protoncli/internal/config"
	"github.com/akeemjenkins/protoncli/internal/smtp"
	"github.com/akeemjenkins/protoncli/internal/termio"
)

type sendFlags struct {
	draftFlags
	saveToSent  bool
	sentMailbox string
}

func newSendCmd() *cobra.Command {
	f := &sendFlags{}
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Compose and send a message via SMTP",
		Long: `send composes an RFC5322 message from the given flags and delivers it over
SMTP using the PM_SMTP_* configuration. With --in-reply-to it threads a reply
to an existing message (fetched over IMAP), making it suitable for replying to
invites and conversations.

Proton Mail Bridge already auto-saves SMTP-sent mail to Sent, so --save-to-sent
defaults to false; enable it for non-Bridge SMTP servers that do not file sent
mail automatically.`,
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,1,2,3,4,5,9",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdSend(f)
		},
	}
	cmd.Flags().StringVar(&f.from, "from", "", "From address (defaults to PM_IMAP_USERNAME)")
	cmd.Flags().StringSliceVar(&f.to, "to", nil, "Recipient(s) — may repeat or use comma-separated list")
	cmd.Flags().StringSliceVar(&f.cc, "cc", nil, "Cc recipient(s)")
	cmd.Flags().StringSliceVar(&f.bcc, "bcc", nil, "Bcc recipient(s)")
	cmd.Flags().StringVar(&f.subject, "subject", "", "Subject")
	cmd.Flags().StringVar(&f.bodyFile, "body-file", "", "Path to UTF-8 plain-text body (use '-' for stdin)")
	cmd.Flags().StringVar(&f.bodyHTMLFile, "body-html-file", "", "Optional path to HTML body (makes message multipart/alternative)")
	cmd.Flags().StringVar(&f.inReplyToUID, "in-reply-to", "", "UID of source message to reply to (fetches its Message-ID)")
	cmd.Flags().StringVar(&f.sourceMailbox, "source-mailbox", "INBOX", "Mailbox containing the source message for --in-reply-to")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Emit the composed RFC5322 bytes instead of sending")
	cmd.Flags().BoolVar(&f.saveToSent, "save-to-sent", false, "APPEND a copy to the Sent mailbox after delivery (off by default; Proton Bridge auto-saves)")
	cmd.Flags().StringVar(&f.sentMailbox, "sent-mailbox", "Sent", "Target mailbox for the saved copy when --save-to-sent is set")
	return cmd
}

func cmdSend(f *sendFlags) error {
	// IMAP is only required to default --from, resolve --in-reply-to, or save
	// a copy to Sent. A pure SMTP send (explicit --from, no reply, no save)
	// needs only PM_SMTP_* and must not fail on missing IMAP credentials.
	needIMAP := f.saveToSent ||
		strings.TrimSpace(f.inReplyToUID) != "" ||
		strings.TrimSpace(f.from) == ""

	var cfg config.Config
	if needIMAP {
		loaded, err := config.LoadFromEnv()
		if err != nil {
			return err
		}
		cfg = loaded
	}

	env, raw, err := buildEnvelope(&f.draftFlags, cfg)
	if err != nil {
		return err
	}

	w := termio.Default()
	if f.dryRun {
		return w.PrintJSON(map[string]any{
			"type":   "send_preview",
			"bytes":  len(raw),
			"rfc822": string(raw),
		})
	}

	smtpCfg, ok := smtp.ConfigFromEnv()
	if !ok {
		return cerr.Config("SMTP not configured: set PM_SMTP_HOST and PM_SMTP_PORT")
	}

	recipients := append(append([]string{}, env.To...), env.Cc...)
	recipients = append(recipients, env.Bcc...)
	if err := smtp.Send(smtpCfg, env.From, recipients, raw); err != nil {
		// No dedicated SMTP kind in cerr; KindAPI (exit 1) with a stable
		// reason keeps SMTP failures inside the published exit-code enum.
		return cerr.API(502, "smtpError", err.Error(), "")
	}

	result := map[string]any{
		"sent":       true,
		"message_id": extractMessageID(raw),
		"size":       len(raw),
		"recipients": len(recipients),
	}

	if f.saveToSent {
		resolved, err := appendMessage(cfg, f.sentMailbox, []string{imap.SeenFlag}, raw)
		if err != nil {
			return err
		}
		result["saved_to"] = resolved
	}

	return w.PrintJSON(result)
}
