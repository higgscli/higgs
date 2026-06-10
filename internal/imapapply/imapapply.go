// Package imapapply applies label and mailbox changes back to IMAP.
package imapapply

import (
	"fmt"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/akeemjenkins/protoncli/internal/imaputil"
	"github.com/akeemjenkins/protoncli/internal/termio"
)

// LabelsRoot is the IMAP mailbox name for the Labels parent (e.g. "Labels").
const LabelsRoot = "Labels"

// EnsureLabelMailbox creates the mailbox labelsRoot/labelName if it does not exist.
// existingNames is a set of mailbox names (from LIST); it is updated when a new mailbox is created.
func EnsureLabelMailbox(c *client.Client, labelsRoot, labelName string, existingNames map[string]bool) (string, error) {
	if labelName == "" {
		return "", nil
	}
	dest := labelsRoot + "/" + strings.TrimSpace(labelName)
	if existingNames[dest] {
		return dest, nil
	}
	termio.Info("Creating label %q", dest)
	if err := c.Create(dest); err != nil {
		return "", fmt.Errorf("CREATE %q: %w", dest, err)
	}
	termio.Info("🏷️  Created label %q", dest)
	existingNames[dest] = true
	return dest, nil
}

// ApplyLabels ensures each label mailbox exists (CREATE if needed), then applies each label to the
// message by UID COPY to the label mailbox (Bridge treats this as adding the label to the message;
// the message stays in its folder). Same pattern as proton-bridge/tests/imap_test.go clientCopy.
func ApplyLabels(c *client.Client, sourceMailbox string, uid uint32, labels []string, existingNames map[string]bool) error {
	if len(labels) == 0 {
		return nil
	}
	labelsRoot := LabelsRoot
	// Ensure each label mailbox exists
	var dests []string
	for _, name := range labels {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		dest, err := EnsureLabelMailbox(c, labelsRoot, name, existingNames)
		if err != nil {
			return err
		}
		if dest != "" {
			dests = append(dests, dest)
		}
	}
	if len(dests) == 0 {
		return nil
	}
	// Select source mailbox (read-write for COPY)
	termio.Info("Selecting %q for COPY", sourceMailbox)
	if _, err := c.Select(sourceMailbox, false); err != nil {
		return fmt.Errorf("SELECT %q for COPY: %w", sourceMailbox, err)
	}
	seqSet := &imap.SeqSet{}
	seqSet.AddNum(uid)
	for _, dest := range dests {
		termio.Info("COPY uid=%d to %q", uid, dest)
		if err := c.UidCopy(seqSet, dest); err != nil {
			return fmt.Errorf("UID COPY uid=%d to %q: %w", uid, dest, err)
		}
	}
	termio.Info("Applied labels to uid=%d: %v", uid, dests)
	return nil
}

// BuildMailboxSet returns a map of mailbox name -> true from the given list (for use with EnsureLabelMailbox).
func BuildMailboxSet(mboxes []imaputil.MailboxInfo) map[string]bool {
	out := make(map[string]bool, len(mboxes))
	for _, m := range mboxes {
		out[m.Name] = true
	}
	return out
}
