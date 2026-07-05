package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/client"
	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapsearch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/imapwrite"
	"github.com/higgscli/higgs/internal/termio"
)

// writeTarget represents the set of UIDs to operate on, either explicit or
// resolved from search criteria.
type writeTarget struct {
	allMatching  bool
	searchFlags  *searchFlags
	explicitUIDs string
}

func addTargetFlags(cmd *cobra.Command, t *writeTarget) {
	cmd.Flags().StringVar(&t.explicitUIDs, "uid", "", "Comma-separated UIDs to target")
	cmd.Flags().BoolVar(&t.allMatching, "all-matching", false, "Target every message matching the search flags")
	t.searchFlags = &searchFlags{}
	addSearchFlags(cmd, t.searchFlags)
}

func parseUIDList(s string) ([]uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid UID %q: %w", p, err)
		}
		out = append(out, uint32(n))
	}
	return out, nil
}

// resolveTarget returns the concrete UID list. Exactly one of --uid or
// --all-matching must be provided.
func resolveTarget(c *client.Client, t *writeTarget, mailbox string) ([]uint32, error) {
	explicit, err := parseUIDList(t.explicitUIDs)
	if err != nil {
		return nil, cerr.Validation("%s", err.Error())
	}
	if len(explicit) > 0 && t.allMatching {
		return nil, cerr.Validation("--uid and --all-matching are mutually exclusive")
	}
	if len(explicit) == 0 && !t.allMatching {
		return nil, cerr.Validation("one of --uid or --all-matching is required")
	}
	if len(explicit) > 0 {
		return explicit, nil
	}
	crit, err := buildCriteria(t.searchFlags)
	if err != nil {
		return nil, err
	}
	if _, err := c.Select(mailbox, true); err != nil {
		return nil, cerr.IMAP(imapclient.Wrap(err), "SELECT %q for search", mailbox)
	}
	uids, err := imapsearch.SearchUIDs(c, crit)
	if err != nil {
		return nil, cerr.IMAP(imapclient.Wrap(err), "UID SEARCH")
	}
	if t.searchFlags.limit > 0 && len(uids) > t.searchFlags.limit {
		uids = uids[len(uids)-t.searchFlags.limit:]
	}
	return uids, nil
}

// runVerifiedMove performs a verified move and streams per-UID rows: a verb
// row (e.g. "archived") only for UIDs confirmed gone from the source mailbox,
// an "error" row for each UID that remained, then a summary with both counts.
// Returns a non-nil IMAP error after printing when anything failed, so the
// exit code reflects partial failure.
func runVerifiedMove(c *client.Client, src, dst, verb, opName string, uids []uint32) error {
	w := termio.Default()
	res, err := imapwrite.MoveVerified(c, src, dst, uids)
	for _, uid := range res.Moved {
		if perr := w.PrintNDJSON(map[string]any{
			"type": verb, "uid": uid, "src": src, "dst": dst,
		}); perr != nil {
			return cerr.Internal(perr, "print")
		}
	}
	for _, uid := range res.Failed {
		cause := err
		if cause == nil {
			cause = fmt.Errorf("message still present in %q after %s and one retry", src, opName)
		}
		env := cerr.IMAP(imapclient.Wrap(cause), "%s uid=%d %q→%q", opName, uid, src, dst).ToEnvelope()["error"]
		if perr := w.PrintNDJSON(map[string]any{
			"type": "error", "uid": uid, "src": src, "dst": dst, "error": env,
		}); perr != nil {
			return cerr.Internal(perr, "print")
		}
	}
	summary := map[string]any{"type": "summary", "src": src, "dst": dst, verb: len(res.Moved)}
	if len(res.Failed) > 0 {
		summary["failed"] = len(res.Failed)
	}
	if perr := w.PrintNDJSON(summary); perr != nil {
		return cerr.Internal(perr, "print")
	}
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "%s %q→%q", opName, src, dst)
	}
	if n := len(res.Failed); n > 0 {
		return cerr.IMAP(
			fmt.Errorf("%d of %d messages still present in %q after retry", n, len(uids), src),
			"%s %q→%q: partial failure", opName, src, dst)
	}
	return nil
}

// runVerifiedFlag performs a verified flag change and streams per-UID rows: a
// rowType row (e.g. "marked") only for UIDs confirmed in the requested state,
// an "error" row for each UID that wasn't, then a summary. base is merged
// into every row and the summary. Returns a non-nil IMAP error after printing
// when anything failed, so the exit code reflects partial failure.
func runVerifiedFlag(c *client.Client, mailbox string, uids []uint32, flag string, add bool, rowType, opName string, base map[string]any) error {
	w := termio.Default()
	res, err := imapwrite.SetFlagVerified(c, mailbox, uids, flag, add)
	if res == nil {
		return cerr.Validation("%s", err.Error())
	}
	row := func(uid uint32, typ string) map[string]any {
		m := map[string]any{"type": typ, "uid": uid, "mailbox": mailbox}
		for k, v := range base {
			m[k] = v
		}
		return m
	}
	for _, uid := range res.Updated {
		if perr := w.PrintNDJSON(row(uid, rowType)); perr != nil {
			return cerr.Internal(perr, "print")
		}
	}
	for _, uid := range res.Failed {
		cause := err
		if cause == nil {
			cause = fmt.Errorf("message not in requested state after %s and one retry (or uid not present in %q)", opName, mailbox)
		}
		r := row(uid, "error")
		r["error"] = cerr.IMAP(imapclient.Wrap(cause), "%s uid=%d on %q", opName, uid, mailbox).ToEnvelope()["error"]
		if perr := w.PrintNDJSON(r); perr != nil {
			return cerr.Internal(perr, "print")
		}
	}
	summary := map[string]any{"type": "summary", "mailbox": mailbox, "count": len(res.Updated)}
	for k, v := range base {
		summary[k] = v
	}
	if len(res.Failed) > 0 {
		summary["failed"] = len(res.Failed)
	}
	if perr := w.PrintNDJSON(summary); perr != nil {
		return cerr.Internal(perr, "print")
	}
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "%s on %q", opName, mailbox)
	}
	if n := len(res.Failed); n > 0 {
		return cerr.IMAP(
			fmt.Errorf("%d of %d messages not updated on %q", n, len(uids), mailbox),
			"%s: partial failure", opName)
	}
	return nil
}

// dialAndResolve opens an IMAP connection and resolves mailbox + targets.
// Caller owns defer-close of the returned client.
func dialAndResolve(t *writeTarget, mailboxArg string) (*client.Client, string, []uint32, error) {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return nil, "", nil, err
	}
	c, err := imapclient.Dial(cfg.IMAP)
	if err != nil {
		return nil, "", nil, cerr.Auth("%s", err.Error())
	}
	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		imapclient.CloseAndLogout(c)
		return nil, "", nil, cerr.IMAP(imapclient.Wrap(err), "LIST failed")
	}
	resolved, err := imaputil.ResolveMailboxName(mailboxArg, mboxes)
	if err != nil {
		imapclient.CloseAndLogout(c)
		return nil, "", nil, cerr.Validation("%s", err.Error())
	}
	uids, err := resolveTarget(c, t, resolved)
	if err != nil {
		imapclient.CloseAndLogout(c)
		return nil, "", nil, err
	}
	return c, resolved, uids, nil
}
