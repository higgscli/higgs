package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/termio"
)

func newVerifyCmd() *cobra.Command {
	var uidList, expect string
	cmd := &cobra.Command{
		Use:   "verify <mailbox>",
		Short: "Audit a mailbox against an expected UID set, stream violations as NDJSON",
		Long: `Verify checks the mailbox against the UID set given via --uid without
mutating anything. --expect present (default) requires every given UID to
exist; absent requires none to exist; exact requires the mailbox UID set to
equal the given set. One {"type":"violation"} row per mismatch, then a
{"type":"summary"} line. Any violation exits non-zero.`,
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,2,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdVerify(args[0], uidList, expect)
		},
	}
	cmd.Flags().StringVar(&uidList, "uid", "", `Comma-separated UIDs to check, or "-" to read from stdin`)
	cmd.Flags().StringVar(&expect, "expect", "present", "Expectation: present, absent, or exact")
	return cmd
}

// verifyViolation is one mismatch between the expected and actual UID state.
type verifyViolation struct {
	uid              uint32
	expected, actual string
}

func cmdVerify(mailbox, uidList, expect string) error {
	switch expect {
	case "present", "absent", "exact":
	default:
		return cerr.Validation("--expect must be present, absent, or exact (got %q)", expect)
	}
	if uidList == "" {
		return cerr.Validation("--uid is required")
	}
	uids, err := resolveUIDList(uidList)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
	c, err := imapclient.Dial(cfg.IMAP)
	if err != nil {
		return cerr.Auth("%s", err.Error())
	}
	defer imapclient.CloseAndLogout(c)

	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "LIST failed")
	}
	resolved, err := imaputil.ResolveMailboxName(mailbox, mboxes)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}
	if _, err := imapfetch.SelectMailbox(c, resolved); err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "SELECT %q", resolved)
	}
	inbox, err := imapfetch.SearchUIDs(c, time.Time{}, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "UID SEARCH")
	}

	given := make(map[uint32]bool, len(uids))
	for _, uid := range uids {
		given[uid] = true
	}
	actual := make(map[uint32]bool, len(inbox))
	for _, uid := range inbox {
		actual[uid] = true
	}

	// checked covers every UID examined: the given set for present/absent,
	// or the union of the given and mailbox sets for exact.
	var checked int
	var violations []verifyViolation
	switch expect {
	case "present":
		checked = len(given)
		for uid := range given {
			if !actual[uid] {
				violations = append(violations, verifyViolation{uid, "present", "absent"})
			}
		}
	case "absent":
		checked = len(given)
		for uid := range given {
			if actual[uid] {
				violations = append(violations, verifyViolation{uid, "absent", "present"})
			}
		}
	case "exact":
		union := make(map[uint32]bool, len(given)+len(actual))
		for uid := range given {
			union[uid] = true
		}
		for uid := range actual {
			union[uid] = true
		}
		checked = len(union)
		for uid := range union {
			switch {
			case given[uid] && !actual[uid]:
				violations = append(violations, verifyViolation{uid, "present", "absent"})
			case !given[uid] && actual[uid]:
				violations = append(violations, verifyViolation{uid, "absent", "present"})
			}
		}
	}
	sort.Slice(violations, func(i, j int) bool { return violations[i].uid < violations[j].uid })

	w := termio.Default()
	for _, v := range violations {
		if perr := w.PrintNDJSON(map[string]any{
			"type": "violation", "uid": v.uid, "mailbox": resolved,
			"expected": v.expected, "actual": v.actual,
		}); perr != nil {
			return cerr.Internal(perr, "print violation")
		}
	}
	if perr := w.PrintNDJSON(map[string]any{
		"type":       "summary",
		"mailbox":    resolved,
		"expect":     expect,
		"checked":    checked,
		"ok":         checked - len(violations),
		"violations": len(violations),
	}); perr != nil {
		return cerr.Internal(perr, "print summary")
	}
	if n := len(violations); n > 0 {
		return cerr.IMAP(
			fmt.Errorf("%d of %d UIDs violated --expect %s in %q", n, checked, expect, resolved),
			"verify %q: expectation not met", resolved)
	}
	return nil
}
