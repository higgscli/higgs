package main

import (
	"github.com/emersion/go-imap"
	"github.com/spf13/cobra"

	"github.com/akeemjenkins/protoncli/internal/config"
	"github.com/akeemjenkins/protoncli/internal/termio"
)

type draftFlags struct {
	from          string
	to            []string
	cc            []string
	bcc           []string
	subject       string
	bodyFile      string
	bodyHTMLFile  string
	inReplyToUID  string
	sourceMailbox string
	draftsMailbox string
	dryRun        bool
}

func newDraftCmd() *cobra.Command {
	f := &draftFlags{}
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Compose a message and save it to the Drafts mailbox via IMAP APPEND (does NOT send)",
		Long: `draft composes an RFC5322 message from the given flags and APPENDs it to
the Drafts mailbox with the \Draft flag set. This command does NOT send email
over SMTP — use 'protoncli send' for outbound delivery.`,
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,2,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdDraft(f)
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
	cmd.Flags().StringVar(&f.draftsMailbox, "drafts-mailbox", "Drafts", "Target mailbox for the saved draft")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Emit the composed RFC5322 bytes instead of APPENDing")
	return cmd
}

func cmdDraft(f *draftFlags) error {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}

	_, raw, err := buildEnvelope(f, cfg)
	if err != nil {
		return err
	}

	w := termio.Default()
	if f.dryRun {
		return w.PrintJSON(map[string]any{
			"type":   "draft_preview",
			"bytes":  len(raw),
			"rfc822": string(raw),
		})
	}

	resolved, err := appendMessage(cfg, f.draftsMailbox, []string{imap.DraftFlag, imap.SeenFlag}, raw)
	if err != nil {
		return err
	}

	return w.PrintJSON(map[string]any{
		"drafted":    true,
		"mailbox":    resolved,
		"size":       len(raw),
		"message_id": extractMessageID(raw),
	})
}
