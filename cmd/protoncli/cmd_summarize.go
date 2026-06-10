package main

import (
	"bytes"
	"context"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	imapclientgo "github.com/emersion/go-imap/client"
	"github.com/spf13/cobra"

	"github.com/akeemjenkins/protoncli/internal/cerr"
	"github.com/akeemjenkins/protoncli/internal/config"
	"github.com/akeemjenkins/protoncli/internal/imapclient"
	"github.com/akeemjenkins/protoncli/internal/imapfetch"
	"github.com/akeemjenkins/protoncli/internal/imaputil"
	"github.com/akeemjenkins/protoncli/internal/llm"
	"github.com/akeemjenkins/protoncli/internal/parse"
	"github.com/akeemjenkins/protoncli/internal/termio"
)

const defaultSummarizeBodyChars = 8000

func newSummarizeCmd() *cobra.Command {
	var (
		uidsFlag       string
		threadUID      uint32
		limit          int
		userContext    string
		model          string
		maxBulletCount int
	)
	cmd := &cobra.Command{
		Use:   "summarize <mailbox>",
		Short: "Summarize messages with Ollama and stream NDJSON results",
		Long: `Produce a structured summary for each UID (--uid) or one summary
for a whole thread (--thread UID).`,
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,3,4,5,6",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdSummarize(args[0], summarizeArgs{
				uids:           uidsFlag,
				threadUID:      threadUID,
				limit:          limit,
				userContext:    userContext,
				model:          model,
				maxBulletCount: maxBulletCount,
			})
		},
	}
	cmd.Flags().StringVar(&uidsFlag, "uid", "", "Comma-separated UIDs to summarize")
	cmd.Flags().Uint32Var(&threadUID, "thread", 0, "Summarize the full thread containing this UID")
	cmd.Flags().IntVar(&limit, "limit", 0, "Cap the number of UIDs processed (0 = no cap)")
	cmd.Flags().StringVar(&userContext, "user-context", "", "Optional extra context added to the prompt")
	cmd.Flags().StringVar(&model, "model", "", "Override Ollama model (defaults to PM_OLLAMA_MODEL)")
	cmd.Flags().IntVar(&maxBulletCount, "max-bullets", 5, "Maximum bullets per summary")
	return cmd
}

type summarizeArgs struct {
	uids           string
	threadUID      uint32
	limit          int
	userContext    string
	model          string
	maxBulletCount int
}

func cmdSummarize(mailbox string, a summarizeArgs) error {
	if strings.TrimSpace(a.uids) == "" && a.threadUID == 0 {
		return cerr.Validation("one of --uid or --thread is required")
	}
	if strings.TrimSpace(a.uids) != "" && a.threadUID != 0 {
		return cerr.Validation("--uid and --thread are mutually exclusive")
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
	model := a.model
	if model == "" {
		model = cfg.Ollama.Model
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
	snap, err := imapfetch.SelectMailbox(c, resolved)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "SELECT %q", resolved)
	}

	var targetUIDs []uint32
	threadMode := a.threadUID != 0
	if threadMode {
		targetUIDs, err = resolveThreadUIDs(c, a.threadUID)
		if err != nil {
			return err
		}
		if len(targetUIDs) == 0 {
			targetUIDs = []uint32{a.threadUID}
		}
	} else {
		targetUIDs, err = parseUIDList(a.uids)
		if err != nil {
			return cerr.Validation("%s", err.Error())
		}
	}
	if a.limit > 0 && len(targetUIDs) > a.limit {
		targetUIDs = targetUIDs[:a.limit]
	}

	tio := termio.Default()
	ctx := context.Background()
	opts := llm.SummarizeOpts{
		MaxBulletCount: a.maxBulletCount,
		UserContext:    a.userContext,
		MaxInput:       defaultSummarizeBodyChars,
	}

	if len(targetUIDs) == 0 {
		return tio.PrintNDJSON(map[string]any{
			"type": "summary", "mailbox": resolved, "count": 0, "failed": 0,
		})
	}

	fetched, err := imapfetch.FetchRFC822(c, targetUIDs)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "FETCH")
	}
	_ = snap

	if threadMode {
		msgs := make([]llm.Message, 0, len(fetched))
		for _, f := range fetched {
			msgs = append(msgs, fetchedToLLMMessage(f, resolved))
		}
		sum, sErr := llm.SummarizeThread(ctx, cfg.Ollama.BaseURL, model, msgs, opts)
		if sErr != nil {
			return sErr
		}
		if err := tio.PrintNDJSON(map[string]any{
			"type":    "summary_item",
			"uid":     a.threadUID,
			"mailbox": resolved,
			"thread":  true,
			"summary": sum,
		}); err != nil {
			return cerr.Internal(err, "write NDJSON")
		}
		return tio.PrintNDJSON(map[string]any{
			"type": "summary", "mailbox": resolved, "count": 1, "failed": 0,
		})
	}

	var failed int
	for _, f := range fetched {
		msg := fetchedToLLMMessage(f, resolved)
		sum, sErr := llm.Summarize(ctx, cfg.Ollama.BaseURL, model, msg, opts)
		if sErr != nil {
			failed++
			if perr := tio.PrintNDJSON(map[string]any{
				"type":    "summary_item",
				"uid":     f.UID,
				"mailbox": resolved,
				"error":   cerr.From(sErr).ToEnvelope()["error"],
			}); perr != nil {
				return cerr.Internal(perr, "write NDJSON")
			}
			continue
		}
		if err := tio.PrintNDJSON(map[string]any{
			"type":    "summary_item",
			"uid":     f.UID,
			"mailbox": resolved,
			"summary": sum,
		}); err != nil {
			return cerr.Internal(err, "write NDJSON")
		}
	}
	return tio.PrintNDJSON(map[string]any{
		"type":    "summary",
		"mailbox": resolved,
		"count":   len(fetched) - failed,
		"failed":  failed,
	})
}

// fetchedToLLMMessage converts a fetched IMAP message into an llm.Message,
// extracting a body snippet from the MIME tree.
func fetchedToLLMMessage(f imapfetch.FetchedMessage, mailbox string) llm.Message {
	body, _ := parse.BestBodyText(bytes.NewReader(f.RFC822))
	m := llm.Message{UID: f.UID, Mailbox: mailbox, Body: body}
	if f.Envelope != nil {
		m.Subject = f.Envelope.Subject
		m.From = envelopeFrom(f.Envelope)
		if !f.Envelope.Date.IsZero() {
			m.Date = f.Envelope.Date.UTC().Format(time.RFC3339)
		}
	}
	return m
}

// resolveThreadUIDs finds a best-effort thread for the given UID within the
// currently-selected mailbox. It fetches envelope metadata for every UID,
// then walks the References / In-Reply-To chain transitively.
func resolveThreadUIDs(c *imapclientgo.Client, seedUID uint32) ([]uint32, error) {
	all, err := c.UidSearch(&imap.SearchCriteria{})
	if err != nil {
		return nil, cerr.IMAP(imapclient.Wrap(err), "UID SEARCH thread")
	}
	if len(all) == 0 {
		return []uint32{seedUID}, nil
	}
	seqSet := &imap.SeqSet{}
	seqSet.AddNum(all...)
	resCh := make(chan *imap.Message, len(all))
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.UidFetch(seqSet, []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid}, resCh)
	}()
	type entry struct {
		uid        uint32
		messageID  string
		references []string
		inReplyTo  string
	}
	var seed *entry
	byUID := map[uint32]*entry{}
	byMsgID := map[string]*entry{}
	for m := range resCh {
		e := &entry{uid: m.Uid}
		if m.Envelope != nil {
			e.messageID = strings.TrimSpace(m.Envelope.MessageId)
			e.inReplyTo = strings.TrimSpace(m.Envelope.InReplyTo)
			e.references = append(e.references, splitRefs(m.Envelope.InReplyTo)...)
		}
		byUID[m.Uid] = e
		if e.messageID != "" {
			byMsgID[e.messageID] = e
		}
		if m.Uid == seedUID {
			seed = e
		}
	}
	if fetchErr := <-errCh; fetchErr != nil {
		return nil, cerr.IMAP(imapclient.Wrap(fetchErr), "UID FETCH thread envelopes")
	}
	if seed == nil {
		return []uint32{seedUID}, nil
	}

	// Build the connected set walking InReplyTo transitively.
	related := map[uint32]bool{seed.uid: true}
	queue := []*entry{seed}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		// parents
		if cur.inReplyTo != "" {
			if p := byMsgID[cur.inReplyTo]; p != nil && !related[p.uid] {
				related[p.uid] = true
				queue = append(queue, p)
			}
		}
		// children: anyone with InReplyTo pointing to cur.messageID.
		if cur.messageID != "" {
			for _, e := range byUID {
				if related[e.uid] {
					continue
				}
				if e.inReplyTo == cur.messageID {
					related[e.uid] = true
					queue = append(queue, e)
				}
			}
		}
	}

	out := make([]uint32, 0, len(related))
	for u := range related {
		out = append(out, u)
	}
	return out, nil
}

func splitRefs(s string) []string {
	var out []string
	for _, p := range strings.Fields(s) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
