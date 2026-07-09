// Package imapwrite provides typed operations that mutate messages:
// flag changes, moves, copies, archive/trash, and read/unread toggling.
package imapwrite

import (
	"fmt"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// DefaultArchiveMailbox is the mailbox Archive uses if the caller does not
// override it.
const DefaultArchiveMailbox = "Archive"

// DefaultTrashMailbox is the mailbox Trash uses if the caller does not
// override it.
const DefaultTrashMailbox = "Trash"

// MarkRead sets or clears the \Seen flag on the given UIDs.
func MarkRead(c *client.Client, mailbox string, uids []uint32, read bool) error {
	return SetFlag(c, mailbox, uids, imap.SeenFlag, read)
}

// FlagResult is the verified per-UID outcome of SetFlagVerified.
type FlagResult struct {
	// Updated holds UIDs confirmed to be in the requested flag state.
	Updated []uint32
	// Failed holds UIDs still in the wrong state after a retry, plus UIDs not
	// present in the mailbox at all — STORE on a nonexistent UID is silently
	// ignored by servers.
	Failed []uint32
}

// SetFlag adds or removes a flag (system or custom keyword) on the UID set,
// verified. Returns an error unless every UID was confirmed updated.
func SetFlag(c *client.Client, mailbox string, uids []uint32, flag string, add bool) error {
	res, err := SetFlagVerified(c, mailbox, uids, flag, add)
	if err != nil {
		return err
	}
	if n := len(res.Failed); n > 0 {
		return fmt.Errorf("UID STORE %s %q on %q: %d of %d messages not updated", storeOpString(add), flag, mailbox, n, len(uids))
	}
	return nil
}

// SetFlagVerified applies the flag change in chunks of MoveChunkSize and
// confirms each chunk with UID SEARCH before reporting it updated — servers
// can acknowledge a STORE without applying it, and silently ignore UIDs that
// don't exist. UIDs still in the wrong state after one retry land in Failed.
// A non-nil error means the operation aborted early; the result reflects
// everything verified up to that point, with the rest in Failed.
func SetFlagVerified(c *client.Client, mailbox string, uids []uint32, flag string, add bool) (*FlagResult, error) {
	flag = strings.TrimSpace(flag)
	if flag == "" {
		return nil, fmt.Errorf("empty flag")
	}
	res := &FlagResult{}
	if len(uids) == 0 {
		return res, nil
	}
	if _, err := c.Select(mailbox, false); err != nil {
		res.Failed = uids
		return res, fmt.Errorf("SELECT %q: %w", mailbox, err)
	}
	op := imap.FormatFlagsOp(imap.AddFlags, true)
	if !add {
		op = imap.FormatFlagsOp(imap.RemoveFlags, true)
	}
	store := func(set []uint32) error {
		seq := &imap.SeqSet{}
		seq.AddNum(set...)
		return c.UidStore(seq, op, []interface{}{flag}, nil)
	}
	for start := 0; start < len(uids); start += MoveChunkSize {
		chunk := uids[start:min(start+MoveChunkSize, len(uids))]
		attemptErr := store(chunk)
		remaining, err := wrongFlagState(c, chunk, flag, add)
		if err != nil {
			res.Failed = append(res.Failed, uids[start:]...)
			if attemptErr != nil {
				return res, fmt.Errorf("STORE chunk on %q: %v; verify: %w", mailbox, attemptErr, err)
			}
			return res, fmt.Errorf("verify STORE on %q: %w", mailbox, err)
		}
		if len(remaining) > 0 {
			// Retry just the stragglers once, then re-verify.
			_ = store(remaining)
			remaining, err = wrongFlagState(c, remaining, flag, add)
			if err != nil {
				res.Failed = append(res.Failed, uids[start:]...)
				return res, fmt.Errorf("verify STORE retry on %q: %w", mailbox, err)
			}
		}
		res.Updated = append(res.Updated, subtractUIDs(chunk, remaining)...)
		res.Failed = append(res.Failed, remaining...)
	}
	return res, nil
}

// wrongFlagState returns the subset of uids not in the desired flag state in
// the currently selected mailbox: messages still lacking (add) or still
// carrying (remove) the flag, plus UIDs missing from the mailbox entirely.
func wrongFlagState(c *client.Client, uids []uint32, flag string, add bool) ([]uint32, error) {
	present, err := presentUIDs(c, uids)
	if err != nil {
		return nil, err
	}
	seq := &imap.SeqSet{}
	seq.AddNum(uids...)
	crit := &imap.SearchCriteria{Uid: seq}
	if add {
		crit.WithoutFlags = []string{flag}
	} else {
		crit.WithFlags = []string{flag}
	}
	wrong, err := uidSearch(c, crit)
	if err != nil {
		return nil, err
	}
	return append(subtractUIDs(uids, present), wrong...), nil
}

// Copy UID-copies the given UIDs from srcMailbox to dstMailbox.
func Copy(c *client.Client, srcMailbox, dstMailbox string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	if _, err := c.Select(srcMailbox, false); err != nil {
		return fmt.Errorf("SELECT %q: %w", srcMailbox, err)
	}
	seq := &imap.SeqSet{}
	seq.AddNum(uids...)
	if err := c.UidCopy(seq, dstMailbox); err != nil {
		return fmt.Errorf("UID COPY %q→%q: %w", srcMailbox, dstMailbox, err)
	}
	return nil
}

// MoveChunkSize bounds how many UIDs are sent in a single MOVE (or fallback
// COPY/STORE) command. Proton Bridge has been observed to answer OK to a
// single MOVE of 10k+ UIDs while applying it only partially, and very large
// UID sets can also exceed server command-length limits.
const MoveChunkSize = 250

// MoveResult is the verified per-UID outcome of MoveVerified.
type MoveResult struct {
	// Moved holds UIDs confirmed absent from the source mailbox afterwards.
	Moved []uint32
	// Failed holds UIDs still present in the source mailbox after a retry,
	// plus any UIDs whose outcome could not be verified.
	Failed []uint32
}

// Move moves messages from srcMailbox to dstMailbox in verified chunks. Uses
// IMAP MOVE when the server advertises it; falls back to COPY + STORE
// \Deleted + EXPUNGE. Returns an error unless every message was confirmed
// moved out of the source mailbox.
func Move(c *client.Client, srcMailbox, dstMailbox string, uids []uint32) error {
	res, err := MoveVerified(c, srcMailbox, dstMailbox, uids)
	if err != nil {
		return err
	}
	if n := len(res.Failed); n > 0 {
		return fmt.Errorf("MOVE %q→%q: %d of %d messages still in source after retry", srcMailbox, dstMailbox, n, len(uids))
	}
	return nil
}

// MoveVerified moves messages in chunks of MoveChunkSize and confirms each
// chunk with UID SEARCH against the source mailbox before reporting it moved
// — servers can acknowledge a MOVE without fully applying it. UIDs still
// present after one retry land in Failed instead of Moved. A non-nil error
// means the operation aborted early (e.g. dead connection); the result still
// reflects everything verified up to that point, with the rest in Failed.
func MoveVerified(c *client.Client, srcMailbox, dstMailbox string, uids []uint32) (*MoveResult, error) {
	res := &MoveResult{}
	if len(uids) == 0 {
		return res, nil
	}
	if _, err := c.Select(srcMailbox, false); err != nil {
		res.Failed = uids
		return res, fmt.Errorf("SELECT %q: %w", srcMailbox, err)
	}
	for start := 0; start < len(uids); start += MoveChunkSize {
		chunk := uids[start:min(start+MoveChunkSize, len(uids))]
		attemptErr := moveOnce(c, srcMailbox, dstMailbox, chunk)
		remaining, err := presentUIDs(c, chunk)
		if err != nil {
			res.Failed = append(res.Failed, uids[start:]...)
			if attemptErr != nil {
				return res, fmt.Errorf("move chunk on %q: %v; verify: %w", srcMailbox, attemptErr, err)
			}
			return res, fmt.Errorf("verify move on %q: %w", srcMailbox, err)
		}
		if len(remaining) > 0 {
			// Retry just the stragglers once, then re-verify.
			_ = moveOnce(c, srcMailbox, dstMailbox, remaining)
			remaining, err = presentUIDs(c, remaining)
			if err != nil {
				res.Failed = append(res.Failed, uids[start:]...)
				return res, fmt.Errorf("verify move retry on %q: %w", srcMailbox, err)
			}
		}
		res.Moved = append(res.Moved, subtractUIDs(chunk, remaining)...)
		res.Failed = append(res.Failed, remaining...)
	}
	return res, nil
}

// moveOnce issues a single MOVE (or the COPY + STORE \Deleted + EXPUNGE
// fallback) for one chunk. The source mailbox must already be selected.
func moveOnce(c *client.Client, srcMailbox, dstMailbox string, uids []uint32) error {
	seq := &imap.SeqSet{}
	seq.AddNum(uids...)

	if hasMove, _ := c.Support("MOVE"); hasMove {
		if err := c.UidMove(seq, dstMailbox); err == nil {
			return nil
		}
		// Fallthrough: server advertised MOVE but rejected the call (some
		// backends advertise capabilities they don't fully implement). A
		// failed MOVE may still have moved part of the set, so narrow the
		// fallback to what is still present — COPYing the full set would
		// duplicate the messages that did move.
		remaining, err := presentUIDs(c, uids)
		if err != nil {
			return fmt.Errorf("UID SEARCH on %q after failed MOVE: %w", srcMailbox, err)
		}
		if len(remaining) == 0 {
			return nil
		}
		seq = &imap.SeqSet{}
		seq.AddNum(remaining...)
	}

	if err := c.UidCopy(seq, dstMailbox); err != nil {
		return fmt.Errorf("UID COPY %q→%q: %w", srcMailbox, dstMailbox, err)
	}
	if err := c.UidStore(seq, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.DeletedFlag}, nil); err != nil {
		return fmt.Errorf("UID STORE \\Deleted on %q: %w", srcMailbox, err)
	}
	if err := c.Expunge(nil); err != nil {
		return fmt.Errorf("EXPUNGE %q: %w", srcMailbox, err)
	}
	return nil
}

// presentUIDs returns the subset of uids still present in the currently
// selected mailbox.
func presentUIDs(c *client.Client, uids []uint32) ([]uint32, error) {
	seq := &imap.SeqSet{}
	seq.AddNum(uids...)
	return uidSearch(c, &imap.SearchCriteria{Uid: seq})
}

// uidSearch runs a verification UID SEARCH, tolerating a Proton Bridge
// (gluon) quirk: a SEARCH whose criteria reference UIDs answers "NO no such
// message" when the selected mailbox is empty, instead of matching nothing —
// gluon resolves UID search keys against its mailbox snapshot, and that
// resolution errors when the snapshot holds no messages. A move that drains
// the source mailbox would then read as a verification failure even though
// the empty source is exactly the expected outcome. On error, re-check with
// a criteria-less SEARCH (which gluon answers normally): a provably empty
// mailbox can match no UIDs, so report no matches; otherwise surface the
// original error.
func uidSearch(c *client.Client, crit *imap.SearchCriteria) ([]uint32, error) {
	matches, err := c.UidSearch(crit)
	if err == nil {
		return matches, nil
	}
	all, allErr := c.UidSearch(&imap.SearchCriteria{})
	if allErr == nil && len(all) == 0 {
		return nil, nil
	}
	return nil, err
}

func subtractUIDs(all, remove []uint32) []uint32 {
	if len(remove) == 0 {
		return all
	}
	drop := make(map[uint32]struct{}, len(remove))
	for _, u := range remove {
		drop[u] = struct{}{}
	}
	out := make([]uint32, 0, len(all))
	for _, u := range all {
		if _, ok := drop[u]; !ok {
			out = append(out, u)
		}
	}
	return out
}

// Archive is a thin wrapper over Move with a default target mailbox.
func Archive(c *client.Client, srcMailbox string, uids []uint32, target string) error {
	if target == "" {
		target = DefaultArchiveMailbox
	}
	return Move(c, srcMailbox, target, uids)
}

// Trash is a thin wrapper over Move with a default target mailbox.
func Trash(c *client.Client, srcMailbox string, uids []uint32, target string) error {
	if target == "" {
		target = DefaultTrashMailbox
	}
	return Move(c, srcMailbox, target, uids)
}

func storeOpString(add bool) string {
	if add {
		return "+"
	}
	return "-"
}
