// Package imapsearch wraps IMAP UID SEARCH with typed criteria and returns
// lightweight match descriptors suitable for streaming output.
package imapsearch

import (
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/higgscli/higgs/internal/termio"
)

// Criteria is a high-level search specification. Zero values are ignored.
type Criteria struct {
	From        string
	To          string
	Cc          string
	Subject     string
	Body        string
	Text        string
	Since       time.Time
	Before      time.Time
	SentSince   time.Time
	SentBefore  time.Time
	LargerThan  uint32
	SmallerThan uint32
	Keywords    []string
	Unkeywords  []string
	Seen        *bool
	Flagged     *bool
	Answered    *bool
	Deleted     *bool
	Draft       *bool
	Recent      *bool
}

// Match is one hit returned by Search.
type Match struct {
	UID     uint32   `json:"uid"`
	Subject string   `json:"subject"`
	From    string   `json:"from"`
	To      string   `json:"to,omitempty"`
	Date    string   `json:"date"`
	Flags   []string `json:"flags"`
	Size    uint32   `json:"size"`
}

// Build translates Criteria to the go-imap SearchCriteria. Returns nil for an
// empty Criteria (matches everything — callers should layer their own limits).
func Build(c Criteria) *imap.SearchCriteria {
	out := &imap.SearchCriteria{}
	addHeader := func(name, val string) {
		if val == "" {
			return
		}
		if out.Header == nil {
			out.Header = make(map[string][]string)
		}
		out.Header[name] = append(out.Header[name], val)
	}
	addHeader("From", c.From)
	addHeader("To", c.To)
	addHeader("Cc", c.Cc)
	addHeader("Subject", c.Subject)
	if c.Body != "" {
		out.Body = append(out.Body, c.Body)
	}
	if c.Text != "" {
		out.Text = append(out.Text, c.Text)
	}
	if !c.Since.IsZero() {
		out.Since = c.Since
	}
	if !c.Before.IsZero() {
		out.Before = c.Before
	}
	if !c.SentSince.IsZero() {
		out.SentSince = c.SentSince
	}
	if !c.SentBefore.IsZero() {
		out.SentBefore = c.SentBefore
	}
	if c.LargerThan > 0 {
		out.Larger = uint32(c.LargerThan)
	}
	if c.SmallerThan > 0 {
		out.Smaller = uint32(c.SmallerThan)
	}
	for _, k := range c.Keywords {
		if k != "" {
			out.WithFlags = append(out.WithFlags, k)
		}
	}
	for _, k := range c.Unkeywords {
		if k != "" {
			out.WithoutFlags = append(out.WithoutFlags, k)
		}
	}
	if c.Seen != nil {
		if *c.Seen {
			out.WithFlags = appendUnique(out.WithFlags, imap.SeenFlag)
		} else {
			out.WithoutFlags = appendUnique(out.WithoutFlags, imap.SeenFlag)
		}
	}
	if c.Flagged != nil {
		if *c.Flagged {
			out.WithFlags = appendUnique(out.WithFlags, imap.FlaggedFlag)
		} else {
			out.WithoutFlags = appendUnique(out.WithoutFlags, imap.FlaggedFlag)
		}
	}
	if c.Answered != nil {
		if *c.Answered {
			out.WithFlags = appendUnique(out.WithFlags, imap.AnsweredFlag)
		} else {
			out.WithoutFlags = appendUnique(out.WithoutFlags, imap.AnsweredFlag)
		}
	}
	if c.Deleted != nil {
		if *c.Deleted {
			out.WithFlags = appendUnique(out.WithFlags, imap.DeletedFlag)
		} else {
			out.WithoutFlags = appendUnique(out.WithoutFlags, imap.DeletedFlag)
		}
	}
	if c.Draft != nil {
		if *c.Draft {
			out.WithFlags = appendUnique(out.WithFlags, imap.DraftFlag)
		} else {
			out.WithoutFlags = appendUnique(out.WithoutFlags, imap.DraftFlag)
		}
	}
	if c.Recent != nil {
		if *c.Recent {
			out.WithFlags = appendUnique(out.WithFlags, imap.RecentFlag)
		} else {
			out.WithoutFlags = appendUnique(out.WithoutFlags, imap.RecentFlag)
		}
	}
	return out
}

// Or returns a criteria that matches either branch (IMAP's OR).
func Or(a, b Criteria) *imap.SearchCriteria {
	return &imap.SearchCriteria{Or: [][2]*imap.SearchCriteria{{Build(a), Build(b)}}}
}

// Proton Bridge's virtual "All Mail" mailbox can answer the same UID SEARCH
// with different results while its view settles (observed: identical
// back-to-back queries returning 122 then 28 matches). stableUIDSearch
// re-runs the search until two consecutive runs agree, so a single flaky
// answer is never reported as the result.
const (
	searchStableMaxRuns = 5
	searchStableDelay   = 500 * time.Millisecond
)

func stableUIDSearch(c *client.Client, crit *imap.SearchCriteria) ([]uint32, error) {
	prev, err := c.UidSearch(crit)
	if err != nil {
		return nil, err
	}
	sort.Slice(prev, func(i, j int) bool { return prev[i] < prev[j] })
	for run := 1; run < searchStableMaxRuns; run++ {
		cur, err := c.UidSearch(crit)
		if err != nil {
			return nil, err
		}
		sort.Slice(cur, func(i, j int) bool { return cur[i] < cur[j] })
		if equalUIDs(prev, cur) {
			return cur, nil
		}
		termio.Warn("UID SEARCH returned %d then %d matches for the same query; retrying until stable", len(prev), len(cur))
		prev = cur
		time.Sleep(searchStableDelay)
	}
	termio.Warn("UID SEARCH did not stabilize after %d runs; using the last result (%d matches)", searchStableMaxRuns, len(prev))
	return prev, nil
}

func equalUIDs(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Search runs UID SEARCH against the already-selected mailbox and returns
// ordered matches with envelope + flags + size for each hit, capped by limit
// (0 = no cap).
func Search(c *client.Client, crit Criteria, limit int) ([]Match, error) {
	uids, err := stableUIDSearch(c, Build(crit))
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(uids) > limit {
		uids = uids[len(uids)-limit:]
	}
	if len(uids) == 0 {
		return nil, nil
	}
	return fetchEnvelopes(c, uids)
}

// SearchUIDs is like Search but returns only UIDs (no fetch).
func SearchUIDs(c *client.Client, crit Criteria) ([]uint32, error) {
	return stableUIDSearch(c, Build(crit))
}

func fetchEnvelopes(c *client.Client, uids []uint32) ([]Match, error) {
	seqSet := &imap.SeqSet{}
	seqSet.AddNum(uids...)
	resCh := make(chan *imap.Message, len(uids))
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.UidFetch(seqSet, []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchFlags, imap.FetchRFC822Size}, resCh)
	}()
	out := make([]Match, 0, len(uids))
	for m := range resCh {
		out = append(out, messageToMatch(m))
	}
	if err := <-errCh; err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

func messageToMatch(m *imap.Message) Match {
	mm := Match{UID: m.Uid, Flags: append([]string{}, m.Flags...), Size: m.Size}
	if m.Envelope != nil {
		mm.Subject = m.Envelope.Subject
		if len(m.Envelope.From) > 0 {
			mm.From = m.Envelope.From[0].Address()
		}
		if len(m.Envelope.To) > 0 {
			mm.To = m.Envelope.To[0].Address()
		}
		if !m.Envelope.Date.IsZero() {
			mm.Date = m.Envelope.Date.UTC().Format(time.RFC3339)
		}
	}
	if mm.Flags == nil {
		mm.Flags = []string{}
	}
	return mm
}

func appendUnique(slice []string, v string) []string {
	for _, s := range slice {
		if strings.EqualFold(s, v) {
			return slice
		}
	}
	return append(slice, v)
}
