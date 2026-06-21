package main

import (
	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapwrite"
	"github.com/higgscli/higgs/internal/termio"
)

func newArchiveCmd() *cobra.Command {
	t := &writeTarget{}
	var target string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "archive <src-mailbox>",
		Short: "Move messages to the Archive mailbox (default: Archive)",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,2,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdArchive(args[0], target, t, dryRun)
		},
	}
	addTargetFlags(cmd, t)
	cmd.Flags().StringVar(&target, "target", imapwrite.DefaultArchiveMailbox, "Destination mailbox")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Emit planned archive without changing state")
	return cmd
}

func cmdArchive(src, target string, t *writeTarget, dryRun bool) error {
	c, resolved, uids, err := dialAndResolve(t, src)
	if err != nil {
		return err
	}
	defer imapclient.CloseAndLogout(c)
	w := termio.Default()
	if dryRun {
		for _, uid := range uids {
			if err := w.PrintNDJSON(map[string]any{
				"type": "pending", "uid": uid, "src": resolved, "dst": target,
			}); err != nil {
				return cerr.Internal(err, "print")
			}
		}
		return w.PrintNDJSON(map[string]any{
			"type": "summary", "src": resolved, "dst": target, "planned": len(uids),
		})
	}
	if len(uids) > 0 {
		if err := imapwrite.Archive(c, resolved, uids, target); err != nil {
			return cerr.IMAP(imapclient.Wrap(err), "ARCHIVE %q→%q", resolved, target)
		}
	}
	for _, uid := range uids {
		if err := w.PrintNDJSON(map[string]any{
			"type": "archived", "uid": uid, "src": resolved, "dst": target,
		}); err != nil {
			return cerr.Internal(err, "print")
		}
	}
	return w.PrintNDJSON(map[string]any{
		"type": "summary", "src": resolved, "dst": target, "archived": len(uids),
	})
}
