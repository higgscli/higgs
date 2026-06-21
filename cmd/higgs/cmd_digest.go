package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/imapsearch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/labels"
	"github.com/higgscli/higgs/internal/llm"
	"github.com/higgscli/higgs/internal/termio"
)

func newDigestCmd() *cobra.Command {
	var (
		since       string
		maxMessages int
		userContext string
		model       string
	)
	cmd := &cobra.Command{
		Use:   "digest [mailbox]",
		Short: "Produce a structured JSON digest of recent messages",
		Long: `digest fetches recent messages in the given mailbox (default INBOX) and
asks the model to produce a compact digest with highlights, per-category
buckets, and counts.`,
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,3,4,5,6",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mailbox := "INBOX"
			if len(args) > 0 {
				mailbox = args[0]
			}
			return cmdDigest(mailbox, since, maxMessages, userContext, model)
		},
	}
	cmd.Flags().StringVar(&since, "since", "7d", "Window duration (e.g. 7d, 24h, 90m)")
	cmd.Flags().IntVar(&maxMessages, "max-messages", 100, "Cap on fetched messages before digesting")
	cmd.Flags().StringVar(&userContext, "user-context", "", "Extra context appended to the digest prompt")
	cmd.Flags().StringVar(&model, "model", "", "Override Ollama model (defaults to PM_OLLAMA_MODEL)")
	return cmd
}

// parseWindow accepts durations with a `d` suffix (e.g. "7d") in addition to
// everything time.ParseDuration supports.
func parseWindow(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid day count %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func cmdDigest(mailbox, since string, maxMessages int, userContext, model string) error {
	dur, err := parseWindow(since)
	if err != nil {
		return cerr.Validation("--since: %s", err.Error())
	}
	if maxMessages <= 0 {
		return cerr.Validation("--max-messages must be > 0")
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
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
	if _, err := imapfetch.SelectMailbox(c, resolved); err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "SELECT %q", resolved)
	}

	crit := imapsearch.Criteria{Since: time.Now().Add(-dur)}
	uids, err := imapsearch.SearchUIDs(c, crit)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "UID SEARCH")
	}
	if len(uids) > maxMessages {
		uids = uids[len(uids)-maxMessages:]
	}

	tio := termio.Default()
	if len(uids) == 0 {
		return tio.PrintJSON(llm.Digest{
			Window:     since,
			Highlights: []llm.Highlight{},
			ByCategory: map[string][]string{},
			Counts:     map[string]int{},
		})
	}

	fetched, err := imapfetch.FetchRFC822(c, uids)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "FETCH")
	}
	msgs := make([]llm.Message, 0, len(fetched))
	for _, f := range fetched {
		msgs = append(msgs, fetchedToLLMMessage(f, resolved))
	}

	ctx := context.Background()
	out, err := llm.BuildDigest(ctx, cfg.Ollama.BaseURL, model, msgs, llm.DigestOpts{
		Window:          since,
		UserContext:     userContext,
		CanonicalLabels: labels.Default.Canonical(),
	})
	if err != nil {
		return err
	}
	out.Window = since
	return tio.PrintJSON(out)
}
