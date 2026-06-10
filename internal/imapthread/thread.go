// Package imapthread groups messages into conversation threads.
//
// Threading is done client-side by walking In-Reply-To / References links and
// falling back to normalized-Subject grouping when no reference headers exist.
// The server's THREAD extension is not used because the in-memory IMAP backend
// used by tests does not support it — the same pure algorithm works against
// real Proton Bridge and lets us unit-test with fabricated envelopes.
package imapthread

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Envelope is the minimal view of an IMAP message needed for threading. The
// caller is expected to derive it from imap.Envelope + References header
// (fetched via BODY.PEEK[HEADER.FIELDS (References)]).
type Envelope struct {
	UID        uint32
	MessageID  string
	InReplyTo  string
	References []string
	Subject    string
	From       string
	Date       time.Time
}

// Node is one message in a thread tree.
type Node struct {
	UID        uint32
	MessageID  string
	InReplyTo  string
	References []string
	Subject    string
	From       string
	Date       string
	Children   []*Node
}

// Thread is a connected group of messages plus a convenience root.
type Thread struct {
	Root         *Node
	UIDs         []uint32
	Count        int
	Subject      string
	Participants []string
	FirstDate    string
	LastDate     string
}

// Build groups envelopes into threads using a MessageID graph with a
// Subject-prefix fallback for messages that share no explicit references.
// Threads are returned sorted by their most recent message, ascending.
func Build(envs []Envelope) []*Thread {
	if len(envs) == 0 {
		return nil
	}

	// Build nodes, keyed by UID (Message-ID may be empty or duplicated).
	nodes := make([]*Node, len(envs))
	byMsgID := map[string]*Node{}
	for i, e := range envs {
		n := &Node{
			UID:        e.UID,
			MessageID:  e.MessageID,
			InReplyTo:  e.InReplyTo,
			References: append([]string{}, e.References...),
			Subject:    e.Subject,
			From:       e.From,
			Date:       formatDate(e.Date),
		}
		nodes[i] = n
		if e.MessageID != "" {
			if _, exists := byMsgID[e.MessageID]; !exists {
				byMsgID[e.MessageID] = n
			}
		}
	}

	// Disjoint-set over node indexes.
	uf := newUnionFind(len(nodes))

	// Link each node to its parent if discoverable.
	indexByMsgID := map[string]int{}
	for i, n := range nodes {
		if n.MessageID != "" {
			if _, exists := indexByMsgID[n.MessageID]; !exists {
				indexByMsgID[n.MessageID] = i
			}
		}
	}
	for i, n := range nodes {
		parentCandidates := make([]string, 0, len(n.References)+1)
		parentCandidates = append(parentCandidates, n.References...)
		if n.InReplyTo != "" {
			parentCandidates = append(parentCandidates, n.InReplyTo)
		}
		for _, ref := range parentCandidates {
			if j, ok := indexByMsgID[ref]; ok {
				uf.union(i, j)
			}
		}
	}

	// Subject-prefix fallback: group singletons that share a normalized subject.
	rootsBySubject := map[string]int{}
	for i, n := range nodes {
		norm := NormalizeSubject(n.Subject)
		if norm == "" {
			continue
		}
		// Only merge when neither side has a pre-existing reference link
		// (otherwise we may over-merge unrelated "Re: Meeting" threads).
		if len(n.References) == 0 && n.InReplyTo == "" {
			if j, ok := rootsBySubject[norm]; ok {
				uf.union(i, j)
			} else {
				rootsBySubject[norm] = i
			}
		}
	}

	// Collect groups.
	groups := map[int][]int{}
	for i := range nodes {
		r := uf.find(i)
		groups[r] = append(groups[r], i)
	}

	threads := make([]*Thread, 0, len(groups))
	for _, idxs := range groups {
		t := buildThread(nodes, idxs)
		threads = append(threads, t)
	}

	// Stable order: earliest thread (by FirstDate) first; tiebreak by Root UID.
	sort.Slice(threads, func(i, j int) bool {
		if threads[i].FirstDate != threads[j].FirstDate {
			return threads[i].FirstDate < threads[j].FirstDate
		}
		return threads[i].Root.UID < threads[j].Root.UID
	})
	return threads
}

func buildThread(nodes []*Node, idxs []int) *Thread {
	members := make([]*Node, 0, len(idxs))
	for _, i := range idxs {
		members = append(members, nodes[i])
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].Date != members[j].Date {
			return members[i].Date < members[j].Date
		}
		return members[i].UID < members[j].UID
	})

	t := &Thread{
		Count: len(members),
	}

	// Link children: each node's parent is its latest referenced Message-ID
	// present in the set; fallback to the earliest message as root.
	memberByMsgID := map[string]*Node{}
	for _, n := range members {
		if n.MessageID != "" {
			if _, exists := memberByMsgID[n.MessageID]; !exists {
				memberByMsgID[n.MessageID] = n
			}
		}
	}
	var orphans []*Node
	for _, n := range members {
		parent := findParent(n, memberByMsgID)
		if parent == nil {
			orphans = append(orphans, n)
			continue
		}
		parent.Children = append(parent.Children, n)
	}
	// The root is the first orphan; if none, use the earliest-dated node.
	if len(orphans) > 0 {
		t.Root = orphans[0]
		// Chain additional orphans as siblings under the earliest root.
		t.Root.Children = append(t.Root.Children, orphans[1:]...)
	} else {
		t.Root = members[0]
	}
	t.Subject = t.Root.Subject

	// Collect UIDs (sorted ascending) + participants (order-preserving dedup).
	seenFrom := map[string]bool{}
	for _, n := range members {
		t.UIDs = append(t.UIDs, n.UID)
		if n.From != "" && !seenFrom[n.From] {
			seenFrom[n.From] = true
			t.Participants = append(t.Participants, n.From)
		}
	}
	sort.Slice(t.UIDs, func(i, j int) bool { return t.UIDs[i] < t.UIDs[j] })

	// Date range.
	t.FirstDate = members[0].Date
	t.LastDate = members[len(members)-1].Date
	return t
}

func findParent(n *Node, byMsgID map[string]*Node) *Node {
	// Walk from the nearest reference backwards: InReplyTo first, then
	// References from end → start (closest ancestor first).
	if n.InReplyTo != "" {
		if p, ok := byMsgID[n.InReplyTo]; ok && p != n {
			return p
		}
	}
	for i := len(n.References) - 1; i >= 0; i-- {
		ref := n.References[i]
		if p, ok := byMsgID[ref]; ok && p != n {
			return p
		}
	}
	return nil
}

// NormalizeSubject strips reply/forward prefixes and collapses whitespace so
// that "Re: Foo", "re: foo " and "Fwd: Re: Foo" all normalize to "foo".
func NormalizeSubject(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Repeatedly strip leading Re:/Fwd:/Fw: prefixes (case-insensitive,
	// optional numeric tags like "Re[2]:"), then collapse whitespace.
	for {
		m := prefixRE.FindStringIndex(s)
		if m == nil || m[0] != 0 {
			break
		}
		s = strings.TrimSpace(s[m[1]:])
	}
	return strings.ToLower(spaceRE.ReplaceAllString(s, " "))
}

var (
	prefixRE = regexp.MustCompile(`(?i)^(re|fwd|fw)(\s*\[[^\]]*\])?\s*:\s*`)
	spaceRE  = regexp.MustCompile(`\s+`)
)

// ParseReferences splits the raw References header value into individual
// "<id@host>" tokens, preserving order and removing duplicates.
func ParseReferences(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, tok := range referenceRE.FindAllString(raw, -1) {
		tok = strings.TrimSpace(tok)
		if tok == "" || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

var referenceRE = regexp.MustCompile(`<[^<>]+>`)

func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// --- union-find --------------------------------------------------------------

type unionFind struct {
	parent []int
	rank   []int
}

func newUnionFind(n int) *unionFind {
	u := &unionFind{parent: make([]int, n), rank: make([]int, n)}
	for i := range u.parent {
		u.parent[i] = i
	}
	return u
}

func (u *unionFind) find(i int) int {
	for u.parent[i] != i {
		u.parent[i] = u.parent[u.parent[i]]
		i = u.parent[i]
	}
	return i
}

func (u *unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	switch {
	case u.rank[ra] < u.rank[rb]:
		u.parent[ra] = rb
	case u.rank[ra] > u.rank[rb]:
		u.parent[rb] = ra
	default:
		u.parent[rb] = ra
		u.rank[ra]++
	}
}
