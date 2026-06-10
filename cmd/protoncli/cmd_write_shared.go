package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/client"
	"github.com/spf13/cobra"

	"github.com/akeemjenkins/protoncli/internal/cerr"
	"github.com/akeemjenkins/protoncli/internal/config"
	"github.com/akeemjenkins/protoncli/internal/imapclient"
	"github.com/akeemjenkins/protoncli/internal/imapsearch"
	"github.com/akeemjenkins/protoncli/internal/imaputil"
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
