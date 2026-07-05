package main

import (
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/labels"
	"github.com/higgscli/higgs/internal/termio"
)

func newCleanupLabelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup-labels",
		Short: "Consolidate old labels into the canonical set, output NDJSON per label + summary",
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,2,4,5",
		},
	}

	dryRun := cmd.Flags().Bool("dry-run", false, "Show what would be done without making changes")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return cmdCleanupLabels(*dryRun)
	}

	return cmd
}

func cmdCleanupLabels(dryRun bool) error {
	tio := termio.Default()

	termio.Info("Loading config from environment")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return cerr.Config("%s", err.Error())
	}

	c, err := imapclient.Dial(cfg.IMAP)
	if err != nil {
		return cerr.Auth("IMAP connect: %s", err.Error())
	}
	defer imapclient.CloseAndLogout(c)

	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "LIST")
	}

	var toCleanup []struct {
		Old string
		New string
	}
	for _, m := range mboxes {
		if !strings.HasPrefix(m.Name, "Labels/") {
			continue
		}
		labelName := strings.TrimPrefix(m.Name, "Labels/")
		if labels.Default.IsCanonical(labelName) {
			continue
		}
		if newLabel, ok := labels.Default.CanonicalFor(labelName); ok {
			toCleanup = append(toCleanup, struct{ Old, New string }{m.Name, "Labels/" + newLabel})
		} else {
			termio.Warn("No mapping for label %q, will skip", m.Name)
		}
	}

	if len(toCleanup) == 0 {
		termio.Info("No labels need cleanup")
		return tio.PrintNDJSON(map[string]any{
			"type":      "summary",
			"processed": 0,
			"skipped":   0,
			"failed":    0,
		})
	}

	if dryRun {
		for _, item := range toCleanup {
			if err := tio.PrintNDJSON(map[string]any{
				"type":      "pending",
				"old_label": item.Old,
				"new_label": item.New,
				"dry_run":   true,
			}); err != nil {
				return cerr.Internal(err, "write NDJSON")
			}
		}
		return tio.PrintNDJSON(map[string]any{
			"type":      "summary",
			"dry_run":   true,
			"pending":   len(toCleanup),
			"processed": 0,
			"skipped":   0,
			"failed":    0,
		})
	}

	termio.Info("Found %d labels to consolidate", len(toCleanup))

	c.Timeout = 180 * time.Second

	existingMboxes := make(map[string]bool)
	for _, m := range mboxes {
		existingMboxes[m.Name] = true
	}
	for _, label := range labels.Default.Canonical() {
		dest := "Labels/" + label
		if !existingMboxes[dest] {
			termio.Info("Creating canonical label %q", dest)
			if err := c.Create(dest); err != nil {
				termio.Warn("CREATE %q: %v (may already exist)", dest, err)
			}
			existingMboxes[dest] = true
		}
	}

	reconnectCount := 0
	reconnect := func(reason string) error {
		reconnectCount++
		termio.Warn("Connection lost (%s), reconnecting (attempt #%d)...", reason, reconnectCount)
		_ = c.Terminate()
		var rerr error
		c, rerr = imapclient.Dial(cfg.IMAP)
		if rerr != nil {
			return cerr.Auth("reconnect failed: %s", rerr.Error())
		}
		c.Timeout = 180 * time.Second
		termio.Info("Reconnected successfully")
		return nil
	}

	processed := 0
	skipped := 0
	failed := 0

	writeFailRow := func(item struct{ Old, New string }, stage string, cause error) error {
		wrapped := cerr.IMAP(imapclient.Wrap(cause), "%s %q: %s", stage, item.Old, cause.Error())
		return tio.PrintNDJSON(map[string]any{
			"label":     item.Old,
			"canonical": item.New,
			"status":    "failed",
			"error":     cerr.From(wrapped).ToEnvelope()["error"],
		})
	}

	writeSkipRow := func(item struct{ Old, New string }, stage string, cause error) error {
		wrapped := cerr.IMAP(imapclient.Wrap(cause), "%s %q: %s", stage, item.Old, cause.Error())
		return tio.PrintNDJSON(map[string]any{
			"label":     item.Old,
			"canonical": item.New,
			"status":    "skipped",
			"error":     cerr.From(wrapped).ToEnvelope()["error"],
		})
	}

	for i, item := range toCleanup {
		termio.Info("[%d/%d] Processing %s -> %s", i+1, len(toCleanup), item.Old, item.New)

		var status *imap.MailboxStatus
		var selectErr error
		for attempt := 0; attempt < 3; attempt++ {
			status, selectErr = c.Select(item.Old, false)
			if selectErr == nil {
				break
			}
			if imapclient.IsConnectionError(imapclient.Wrap(selectErr)) {
				if rerr := reconnect(selectErr.Error()); rerr != nil {
					return rerr
				}
				continue
			}
			break
		}
		if selectErr != nil {
			termio.Error("SELECT %q: %v (skipping)", item.Old, selectErr)
			if werr := writeSkipRow(item, "SELECT", selectErr); werr != nil {
				return cerr.Internal(werr, "write NDJSON")
			}
			skipped++
			continue
		}

		if status.Messages == 0 {
			termio.Info("Empty mailbox, deleting")
			deleteErr := c.Delete(item.Old)
			if deleteErr != nil && imapclient.IsConnectionError(imapclient.Wrap(deleteErr)) {
				if rerr := reconnect(deleteErr.Error()); rerr != nil {
					return rerr
				}
				deleteErr = c.Delete(item.Old)
			}
			if deleteErr != nil {
				termio.Warn("DELETE %q: %v", item.Old, deleteErr)
				if werr := writeFailRow(item, "DELETE", deleteErr); werr != nil {
					return cerr.Internal(werr, "write NDJSON")
				}
				failed++
				continue
			}
			if werr := tio.PrintNDJSON(map[string]any{
				"label":          item.Old,
				"canonical":      item.New,
				"messages_moved": 0,
				"status":         "ok",
			}); werr != nil {
				return cerr.Internal(werr, "write NDJSON")
			}
			processed++
			continue
		}

		var uids []uint32
		var searchErr error
		for attempt := 0; attempt < 3; attempt++ {
			uids, searchErr = c.UidSearch(&imap.SearchCriteria{})
			if searchErr == nil {
				break
			}
			if imapclient.IsConnectionError(imapclient.Wrap(searchErr)) {
				if rerr := reconnect(searchErr.Error()); rerr != nil {
					return rerr
				}
				if _, serr := c.Select(item.Old, false); serr != nil {
					termio.Warn("Re-SELECT after reconnect: %v", serr)
				}
				continue
			}
			break
		}
		if searchErr != nil {
			termio.Error("SEARCH %q: %v (skipping)", item.Old, searchErr)
			if werr := writeSkipRow(item, "SEARCH", searchErr); werr != nil {
				return cerr.Internal(werr, "write NDJSON")
			}
			skipped++
			continue
		}
		termio.Info("Found %d messages to move", len(uids))

		msgsMoved := len(uids)

		if len(uids) > 0 {
			seqSet := &imap.SeqSet{}
			seqSet.AddNum(uids...)
			var copyErr error
			for attempt := 0; attempt < 3; attempt++ {
				copyErr = c.UidCopy(seqSet, item.New)
				if copyErr == nil {
					break
				}
				if imapclient.IsConnectionError(imapclient.Wrap(copyErr)) {
					if rerr := reconnect(copyErr.Error()); rerr != nil {
						return rerr
					}
					if _, serr := c.Select(item.Old, false); serr != nil {
						termio.Warn("Re-SELECT after reconnect: %v", serr)
					}
					continue
				}
				break
			}
			if copyErr != nil {
				termio.Error("COPY to %q: %v (skipping)", item.New, copyErr)
				if werr := writeFailRow(item, "COPY", copyErr); werr != nil {
					return cerr.Internal(werr, "write NDJSON")
				}
				failed++
				continue
			}
			termio.Info("Copied %d messages to %s", len(uids), item.New)

			deleteItem := imap.FormatFlagsOp(imap.AddFlags, true)
			if err := c.UidStore(seqSet, deleteItem, []interface{}{imap.DeletedFlag}, nil); err != nil {
				if imapclient.IsConnectionError(imapclient.Wrap(err)) {
					if rerr := reconnect(err.Error()); rerr != nil {
						return rerr
					}
				}
				termio.Error("STORE \\Deleted: %v", err)
				if werr := writeFailRow(item, "STORE_Deleted", err); werr != nil {
					return cerr.Internal(werr, "write NDJSON")
				}
				failed++
				continue
			}

			if err := c.Expunge(nil); err != nil {
				if imapclient.IsConnectionError(imapclient.Wrap(err)) {
					if rerr := reconnect(err.Error()); rerr != nil {
						return rerr
					}
				}
				termio.Error("EXPUNGE: %v", err)
				if werr := writeFailRow(item, "EXPUNGE", err); werr != nil {
					return cerr.Internal(werr, "write NDJSON")
				}
				failed++
				continue
			}
		}

		if err := c.Close(); err != nil {
			if imapclient.IsConnectionError(imapclient.Wrap(err)) {
				if rerr := reconnect(err.Error()); rerr != nil {
					return rerr
				}
			} else {
				termio.Warn("CLOSE: %v", err)
			}
		}
		deleteErr := c.Delete(item.Old)
		if deleteErr != nil && imapclient.IsConnectionError(imapclient.Wrap(deleteErr)) {
			if rerr := reconnect(deleteErr.Error()); rerr != nil {
				return rerr
			}
			deleteErr = c.Delete(item.Old)
		}
		if deleteErr != nil {
			// The messages were moved but the old label remains — reporting
			// "ok" here would hide the leftover mailbox from consumers.
			termio.Warn("DELETE %q: %v", item.Old, deleteErr)
			wrapped := cerr.IMAP(imapclient.Wrap(deleteErr), "DELETE %q after moving %d messages: %s", item.Old, msgsMoved, deleteErr.Error())
			if werr := tio.PrintNDJSON(map[string]any{
				"label":          item.Old,
				"canonical":      item.New,
				"messages_moved": msgsMoved,
				"status":         "failed",
				"error":          cerr.From(wrapped).ToEnvelope()["error"],
			}); werr != nil {
				return cerr.Internal(werr, "write NDJSON")
			}
			failed++
			continue
		}
		termio.Info("Deleted old label %s", item.Old)

		if werr := tio.PrintNDJSON(map[string]any{
			"label":          item.Old,
			"canonical":      item.New,
			"messages_moved": msgsMoved,
			"status":         "ok",
		}); werr != nil {
			return cerr.Internal(werr, "write NDJSON")
		}
		processed++
	}

	summary := map[string]any{
		"type":       "summary",
		"processed":  processed,
		"skipped":    skipped,
		"failed":     failed,
		"reconnects": reconnectCount,
	}

	termio.Info("Label cleanup complete: processed=%d skipped=%d failed=%d", processed, skipped, failed)
	return tio.PrintNDJSON(summary)
}
