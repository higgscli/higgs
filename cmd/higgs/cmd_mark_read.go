package main

import (
	"github.com/emersion/go-imap"
	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/termio"
)

func newMarkReadCmd() *cobra.Command {
	t := &writeTarget{}
	var unread bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "mark-read <mailbox>",
		Short: "Mark messages read (or unread with --unread)",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,2,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdMarkRead(args[0], t, unread, dryRun)
		},
	}
	addTargetFlags(cmd, t)
	cmd.Flags().BoolVar(&unread, "unread", false, "Mark unread instead of read")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Emit planned STORE without changing state")
	return cmd
}

func cmdMarkRead(mailbox string, t *writeTarget, unread, dryRun bool) error {
	c, resolved, uids, err := dialAndResolve(t, mailbox)
	if err != nil {
		return err
	}
	defer imapclient.CloseAndLogout(c)
	w := termio.Default()
	state := "read"
	if unread {
		state = "unread"
	}
	if dryRun {
		for _, uid := range uids {
			if err := w.PrintNDJSON(map[string]any{
				"type": "pending", "uid": uid, "mailbox": resolved, "state": state,
			}); err != nil {
				return cerr.Internal(err, "print")
			}
		}
		return w.PrintNDJSON(map[string]any{
			"type": "summary", "mailbox": resolved, "state": state, "planned": len(uids),
		})
	}
	return runVerifiedFlag(c, resolved, uids, imap.SeenFlag, !unread, "marked", "MARK-READ", map[string]any{"state": state})
}
