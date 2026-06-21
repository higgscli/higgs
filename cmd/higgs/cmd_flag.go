package main

import (
	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapwrite"
	"github.com/higgscli/higgs/internal/termio"
)

func newFlagCmd() *cobra.Command {
	t := &writeTarget{}
	var add, remove string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "flag <mailbox>",
		Short: "Add or remove a flag/keyword on a set of UIDs",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,2,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdFlag(args[0], t, add, remove, dryRun)
		},
	}
	addTargetFlags(cmd, t)
	cmd.Flags().StringVar(&add, "add", "", `Flag to add (e.g. "\\Flagged", "\\Seen", "urgent")`)
	cmd.Flags().StringVar(&remove, "remove", "", "Flag to remove")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Emit the planned STORE without changing state")
	return cmd
}

func cmdFlag(mailbox string, t *writeTarget, addFlag, removeFlag string, dryRun bool) error {
	if addFlag == "" && removeFlag == "" {
		return cerr.Validation("one of --add or --remove is required")
	}
	if addFlag != "" && removeFlag != "" {
		return cerr.Validation("--add and --remove are mutually exclusive")
	}
	c, resolved, uids, err := dialAndResolve(t, mailbox)
	if err != nil {
		return err
	}
	defer imapclient.CloseAndLogout(c)

	flag := addFlag
	op := "add"
	if removeFlag != "" {
		flag = removeFlag
		op = "remove"
	}
	w := termio.Default()
	if dryRun {
		for _, uid := range uids {
			if err := w.PrintNDJSON(map[string]any{
				"type": "pending", "uid": uid, "mailbox": resolved, "op": op, "flag": flag,
			}); err != nil {
				return cerr.Internal(err, "print")
			}
		}
		return w.PrintNDJSON(map[string]any{
			"type": "summary", "mailbox": resolved, "op": op, "flag": flag, "planned": len(uids),
		})
	}
	if len(uids) > 0 {
		if err := imapwrite.SetFlag(c, resolved, uids, flag, op == "add"); err != nil {
			return cerr.IMAP(imapclient.Wrap(err), "UID STORE %s %q", op, flag)
		}
	}
	for _, uid := range uids {
		if err := w.PrintNDJSON(map[string]any{
			"type": "flagged", "uid": uid, "mailbox": resolved, "op": op, "flag": flag,
		}); err != nil {
			return cerr.Internal(err, "print")
		}
	}
	return w.PrintNDJSON(map[string]any{
		"type": "summary", "mailbox": resolved, "op": op, "flag": flag, "count": len(uids),
	})
}
