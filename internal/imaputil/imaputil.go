// Package imaputil provides IMAP mailbox discovery and name-resolution helpers.
package imaputil

import (
	"fmt"
	"sort"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/akeemjenkins/protoncli/internal/termio"
)

type MailboxInfo struct {
	Name  string
	Delim rune
	Attrs []string

	NumMessages *uint32
	NumUnseen   *uint32
}

// ListMailboxes lists all mailboxes using the same LIST "" "*" as Proton Bridge
// tests (proton-bridge/tests/imap_test.go clientList). useStatus is ignored for
// v1 client (no LIST-STATUS); we only do plain LIST.
func ListMailboxes(c *client.Client, useStatus bool) ([]MailboxInfo, error) {
	_ = useStatus
	resCh := make(chan *imap.MailboxInfo, 64)

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.List("", "*", resCh)
	}()

	var raw []*imap.MailboxInfo
	for m := range resCh {
		raw = append(raw, m)
	}
	// c.List closes resCh before returning, but read listErr from a
	// synchronized channel so the race detector is satisfied.
	listErr := <-errCh
	if listErr != nil {
		termio.Error("LIST failed: %v", listErr)
		return nil, fmt.Errorf("LIST failed (connection may have been closed by server): %w", listErr)
	}
	if len(raw) == 0 {
		return nil, nil
	}

	out := make([]MailboxInfo, 0, len(raw))
	for _, m := range raw {
		delim := rune(0)
		if m.Delimiter != "" {
			delim = rune(m.Delimiter[0])
		}
		attrs := append([]string(nil), m.Attributes...)
		sort.Strings(attrs)
		out = append(out, MailboxInfo{
			Name:  m.Name,
			Delim: delim,
			Attrs: attrs,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func DetectAllMail(mailboxes []MailboxInfo) (name string, ok bool) {
	for _, m := range mailboxes {
		for _, a := range m.Attrs {
			if strings.EqualFold(a, "\\All") {
				return m.Name, true
			}
		}
	}

	for _, cand := range []string{"All Mail", "All mail", "ALL MAIL"} {
		for _, m := range mailboxes {
			if m.Name == cand {
				return m.Name, true
			}
		}
	}

	return "", false
}

// ResolveMailboxName maps a user-provided mailbox name to the server's canonical
// name. IMAP mailbox names are case-sensitive; Proton Bridge uses e.g.
// "Folders/Accounts" not "folders/Accounts".
func ResolveMailboxName(requested string, mailboxes []MailboxInfo) (string, error) {
	for _, m := range mailboxes {
		if m.Name == requested {
			return requested, nil
		}
	}
	reqLower := strings.ToLower(requested)
	var matches []string
	for _, m := range mailboxes {
		if strings.ToLower(m.Name) == reqLower {
			matches = append(matches, m.Name)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no such mailbox %q (names are case-sensitive; run scan-folders for exact spelling)", requested)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous mailbox %q (case-insensitive matches: %v)", requested, matches)
	}
}

// DetectLabelsRoot returns the IMAP mailbox name for the Labels parent.
// Proton Bridge uses "Labels" (proton-bridge internal/services/imapservice/connector.go labelPrefix).
func DetectLabelsRoot(mailboxes []MailboxInfo) (name string, ok bool) {
	for _, m := range mailboxes {
		if strings.EqualFold(m.Name, "Labels") {
			return m.Name, true
		}
	}

	best := ""
	for _, m := range mailboxes {
		d := m.Delim
		if d == 0 {
			continue
		}
		parts := strings.Split(m.Name, string(d))
		if len(parts) == 0 {
			continue
		}
		if strings.EqualFold(parts[len(parts)-1], "Labels") {
			if best == "" || len(m.Name) < len(best) {
				best = m.Name
			}
		}
	}
	if best != "" {
		return best, true
	}

	return "", false
}
