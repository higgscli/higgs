package main

import (
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/imapsearch"
	"github.com/higgscli/higgs/internal/imapthread"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/termio"
)

type threadsFlags struct {
	since string
	limit int
}

func newThreadsCmd() *cobra.Command {
	f := &threadsFlags{}
	cmd := &cobra.Command{
		Use:   "threads [mailbox]",
		Short: "Group messages into threads and stream one row per thread",
		Long: `threads fetches envelopes from the given mailbox (default INBOX), groups them
into threads by In-Reply-To / References links (with a normalized-Subject
fallback), and streams one NDJSON row per thread.`,
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mailbox := "INBOX"
			if len(args) > 0 {
				mailbox = args[0]
			}
			return cmdThreads(mailbox, f)
		},
	}
	cmd.Flags().StringVar(&f.since, "since", "", "Only include messages on or after YYYY-MM-DD")
	cmd.Flags().IntVar(&f.limit, "limit", 0, "Cap input to most-recent N messages before grouping")
	return cmd
}

func cmdThreads(mailbox string, f *threadsFlags) error {
	since, err := parseDate(f.since, "since")
	if err != nil {
		return err
	}
	if f.limit < 0 {
		return cerr.Validation("--limit must be >= 0")
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

	envs, err := fetchThreadEnvelopes(c, imapsearch.Criteria{Since: since}, f.limit)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "fetch envelopes")
	}

	threads := imapthread.Build(envs)
	w := termio.Default()
	for i, th := range threads {
		if err := w.PrintNDJSON(map[string]any{
			"type":         "thread",
			"thread_id":    threadID(i, th),
			"count":        th.Count,
			"root_uid":     th.Root.UID,
			"subject":      th.Subject,
			"participants": th.Participants,
			"first_date":   th.FirstDate,
			"last_date":    th.LastDate,
			"uids":         th.UIDs,
		}); err != nil {
			return cerr.Internal(err, "print thread")
		}
	}
	return w.PrintNDJSON(map[string]any{
		"type":    "summary",
		"mailbox": resolved,
		"count":   len(threads),
	})
}

// threadID derives a stable-ish identifier from the root Message-ID when
// available; otherwise falls back to "thread-<i>-uid-<uid>".
func threadID(i int, th *imapthread.Thread) string {
	if th.Root != nil && th.Root.MessageID != "" {
		return th.Root.MessageID
	}
	return simpleID(i, th)
}

func simpleID(i int, th *imapthread.Thread) string {
	root := uint32(0)
	if th.Root != nil {
		root = th.Root.UID
	}
	return "thread-" + strconv.Itoa(i) + "-uid-" + strconv.FormatUint(uint64(root), 10)
}

// fetchThreadEnvelopes runs UID SEARCH + UID FETCH for envelope + References
// header, converting results into imapthread.Envelope values.
func fetchThreadEnvelopes(c *client.Client, crit imapsearch.Criteria, limit int) ([]imapthread.Envelope, error) {
	uids, err := c.UidSearch(imapsearch.Build(crit))
	if err != nil {
		return nil, err
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	if limit > 0 && len(uids) > limit {
		uids = uids[len(uids)-limit:]
	}
	if len(uids) == 0 {
		return nil, nil
	}
	return fetchEnvelopesWithReferences(c, uids)
}

func fetchEnvelopesWithReferences(c *client.Client, uids []uint32) ([]imapthread.Envelope, error) {
	seqSet := &imap.SeqSet{}
	seqSet.AddNum(uids...)

	refsSection, err := imap.ParseBodySectionName("BODY.PEEK[HEADER.FIELDS (References)]")
	if err != nil {
		return nil, err
	}
	refsItem := refsSection.FetchItem()

	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, refsItem}
	resCh := make(chan *imap.Message, len(uids))
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.UidFetch(seqSet, items, resCh)
	}()

	out := make([]imapthread.Envelope, 0, len(uids))
	for m := range resCh {
		env := imapthread.Envelope{UID: m.Uid}
		if m.Envelope != nil {
			env.Subject = m.Envelope.Subject
			env.MessageID = m.Envelope.MessageId
			env.InReplyTo = m.Envelope.InReplyTo
			env.Date = m.Envelope.Date
			if len(m.Envelope.From) > 0 {
				env.From = m.Envelope.From[0].Address()
			}
		}
		if lit := m.GetBody(refsSection); lit != nil {
			if raw, err := io.ReadAll(lit); err == nil {
				env.References = imapthread.ParseReferences(extractReferencesValue(string(raw)))
			}
		}
		out = append(out, env)
	}
	if err := <-errCh; err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

// extractReferencesValue pulls the References header value out of a mini
// header block returned by BODY[HEADER.FIELDS (References)]. Handles
// continuation lines (folded) and is tolerant of CRLF/LF.
func extractReferencesValue(block string) string {
	block = strings.ReplaceAll(block, "\r\n", "\n")
	lines := strings.Split(block, "\n")
	var out []string
	inRefs := false
	for _, line := range lines {
		if inRefs && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			out = append(out, strings.TrimSpace(line))
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "references:") {
			inRefs = true
			out = append(out, strings.TrimSpace(line[len("References:"):]))
			continue
		}
		inRefs = false
	}
	return strings.Join(out, " ")
}
