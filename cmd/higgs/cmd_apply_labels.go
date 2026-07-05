// Command higgs is an agent-first CLI for Proton Mail: IMAP mailbox
// management and local Ollama classification behind a machine-readable schema
// contract (structured JSON on stdout, typed errors, stable exit codes).
package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapapply"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/state"
	"github.com/higgscli/higgs/internal/termio"
)

func newApplyLabelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply-labels [mailbox]",
		Short: "Apply labels from state DB to messages, output NDJSON per message",
		Args:  cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,2,4,5,7",
		},
	}

	limitFlag := cmd.Flags().Int("limit", 0, "Max messages to process (0 = all unapplied)")
	dryRun := cmd.Flags().Bool("dry-run", false, "Show what would be applied without making changes")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		mailbox := "Folders/Accounts"
		if len(args) > 0 {
			mailbox = args[0]
		}
		return cmdApplyLabels(mailbox, *limitFlag, *dryRun)
	}

	return cmd
}

func cmdApplyLabels(mailbox string, limitFlag int, dryRun bool) error {
	tio := termio.Default()

	termio.Info("Loading config from environment")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return cerr.Config("%s", err.Error())
	}

	dbPath := os.Getenv("PM_STATE_DB")
	stateDB, err := state.Open(dbPath)
	if err != nil {
		return cerr.State(err, "open state DB")
	}
	defer stateDB.Close()

	c, err := imapclient.Dial(cfg.IMAP)
	if err != nil {
		return cerr.Auth("IMAP connect: %s", err.Error())
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

	termio.Info("Selecting mailbox %q", mailbox)
	snap, err := imapfetch.SelectMailbox(c, mailbox)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "SELECT %q", mailbox)
	}
	termio.Info("Mailbox UIDVALIDITY=%d", snap.UIDValidity)

	limit := limitFlag
	unapplied, err := stateDB.GetUnappliedMessages(mailbox, snap.UIDValidity, limit)
	if err != nil {
		return cerr.State(err, "get unapplied messages")
	}

	if len(unapplied) == 0 {
		termio.Info("No unapplied messages in state DB for %q", mailbox)
		return tio.PrintNDJSON(map[string]any{
			"type":    "summary",
			"mailbox": mailbox,
			"applied": 0,
			"failed":  0,
		})
	}

	termio.Info("Found %d messages needing label application", len(unapplied))

	if dryRun {
		for _, msg := range unapplied {
			if err := tio.PrintNDJSON(map[string]any{
				"type":    "pending",
				"uid":     msg.UID,
				"labels":  msg.SuggestedLabels,
				"subject": truncate(msg.Subject, 80),
				"dry_run": true,
			}); err != nil {
				return cerr.Internal(err, "write NDJSON")
			}
		}
		return tio.PrintNDJSON(map[string]any{
			"type":    "summary",
			"dry_run": true,
			"mailbox": mailbox,
			"pending": len(unapplied),
			"applied": 0,
			"failed":  0,
		})
	}

	applyTimeout := time.Duration(getEnvInt("PM_IMAP_APPLY_TIMEOUT", 180)) * time.Second
	c.Timeout = applyTimeout

	existingMailboxes := imapapply.BuildMailboxSet(mboxes)
	termio.Info("Existing mailboxes: %d", len(existingMailboxes))

	reconnect := func(reason string) error {
		termio.Warn("Connection lost (%s), reconnecting...", reason)
		_ = c.Terminate()
		var rerr error
		c, rerr = imapclient.Dial(cfg.IMAP)
		if rerr != nil {
			return cerr.Auth("reconnect failed: %s", rerr.Error())
		}
		c.Timeout = applyTimeout
		mboxes, rerr = imaputil.ListMailboxes(c, false)
		if rerr != nil {
			return cerr.IMAP(imapclient.Wrap(rerr), "LIST after reconnect")
		}
		existingMailboxes = imapapply.BuildMailboxSet(mboxes)
		termio.Info("Reconnected successfully")
		return nil
	}

	applied := 0
	failedCount := 0

	termio.Info("Applying labels to %d messages", len(unapplied))

	for i, msg := range unapplied {
		if len(msg.SuggestedLabels) == 0 {
			markApplied(stateDB, mailbox, snap.UIDValidity, msg.UID, true, "")
			if err := tio.PrintNDJSON(map[string]any{
				"uid":     msg.UID,
				"labels":  []string{},
				"applied": true,
			}); err != nil {
				return cerr.Internal(err, "write NDJSON")
			}
			applied++
			continue
		}

		termio.Info("[%d/%d] uid=%d labels=%v", i+1, len(unapplied), msg.UID, msg.SuggestedLabels)

		var applyErr error
		for attempt := 0; attempt < 3; attempt++ {
			applyErr = imapapply.ApplyLabels(c, mailbox, msg.UID, msg.SuggestedLabels, existingMailboxes)
			if applyErr == nil {
				break
			}
			if imapclient.IsConnectionError(imapclient.Wrap(applyErr)) {
				if rerr := reconnect(applyErr.Error()); rerr != nil {
					return rerr
				}
				continue
			}
			break
		}

		if applyErr != nil {
			termio.Error("Failed uid=%d: %v", msg.UID, applyErr)
			markApplied(stateDB, mailbox, snap.UIDValidity, msg.UID, false, applyErr.Error())
			rowErr := cerr.IMAP(imapclient.Wrap(applyErr), "%s", applyErr.Error())
			if err := tio.PrintNDJSON(map[string]any{
				"uid":     msg.UID,
				"labels":  msg.SuggestedLabels,
				"applied": false,
				"error":   cerr.From(rowErr).ToEnvelope()["error"],
			}); err != nil {
				return cerr.Internal(err, "write NDJSON")
			}
			failedCount++
		} else {
			termio.Info("Applied uid=%d", msg.UID)
			markApplied(stateDB, mailbox, snap.UIDValidity, msg.UID, true, "")
			if err := tio.PrintNDJSON(map[string]any{
				"uid":     msg.UID,
				"labels":  msg.SuggestedLabels,
				"applied": true,
			}); err != nil {
				return cerr.Internal(err, "write NDJSON")
			}
			applied++
		}
	}

	summary := map[string]any{
		"type":    "summary",
		"mailbox": mailbox,
		"applied": applied,
		"failed":  failedCount,
	}

	termio.Info("Apply complete: applied=%d failed=%d", applied, failedCount)
	return tio.PrintNDJSON(summary)
}

// markApplied records apply state, surfacing (rather than swallowing) DB
// write failures: a lost applied=true record means the message is re-applied
// on the next run.
func markApplied(db *state.DB, mailbox string, uidValidity, uid uint32, applied bool, applyErr string) {
	if err := db.MarkLabelsApplied(mailbox, uidValidity, uid, applied, applyErr); err != nil {
		termio.Warn("state DB: record applied=%v for uid=%d failed: %v", applied, uid, err)
	}
}
