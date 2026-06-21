package main

import (
	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapwrite"
	"github.com/higgscli/higgs/internal/termio"
)

func newMoveCmd() *cobra.Command {
	t := &writeTarget{}
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "move <src-mailbox> <dst-mailbox>",
		Short: "Move messages between mailboxes (IMAP MOVE, falls back to COPY+EXPUNGE)",
		Args:  cobra.ExactArgs(2),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,2,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdMove(args[0], args[1], t, dryRun)
		},
	}
	addTargetFlags(cmd, t)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Emit the planned moves without changing state")
	return cmd
}

func cmdMove(src, dst string, t *writeTarget, dryRun bool) error {
	c, resolvedSrc, uids, err := dialAndResolve(t, src)
	if err != nil {
		return err
	}
	defer imapclient.CloseAndLogout(c)

	w := termio.Default()
	if dryRun {
		for _, uid := range uids {
			if err := w.PrintNDJSON(map[string]any{
				"type": "pending", "uid": uid, "src": resolvedSrc, "dst": dst,
			}); err != nil {
				return cerr.Internal(err, "print")
			}
		}
		return w.PrintNDJSON(map[string]any{
			"type": "summary", "src": resolvedSrc, "dst": dst, "planned": len(uids),
		})
	}
	if len(uids) == 0 {
		return w.PrintNDJSON(map[string]any{
			"type": "summary", "src": resolvedSrc, "dst": dst, "moved": 0,
		})
	}
	if err := imapwrite.Move(c, resolvedSrc, dst, uids); err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "MOVE %q→%q", resolvedSrc, dst)
	}
	for _, uid := range uids {
		if err := w.PrintNDJSON(map[string]any{
			"type": "moved", "uid": uid, "src": resolvedSrc, "dst": dst,
		}); err != nil {
			return cerr.Internal(err, "print")
		}
	}
	return w.PrintNDJSON(map[string]any{
		"type": "summary", "src": resolvedSrc, "dst": dst, "moved": len(uids),
	})
}
