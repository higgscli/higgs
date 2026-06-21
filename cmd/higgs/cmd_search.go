package main

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/imapsearch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/termio"
)

type searchFlags struct {
	from, to, cc, subject, body, text string
	since, before                     string
	sentSince, sentBefore             string
	largerThan, smallerThan           uint32
	keywords, unkeywords              []string
	unseen, seen                      bool
	flagged, answered                 bool
	limit                             int
}

func newSearchCmd() *cobra.Command {
	f := &searchFlags{}
	cmd := &cobra.Command{
		Use:   "search [mailbox]",
		Short: "Search IMAP messages by typed criteria, stream matches as NDJSON",
		Long: `Search uses IMAP UID SEARCH with the given criteria. Match rows include
UID, Subject, From, Date, Flags, and Size. Results end with a {"type":"summary"} line.`,
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,2,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mailbox := "INBOX"
			if len(args) > 0 {
				mailbox = args[0]
			}
			return cmdSearch(mailbox, f)
		},
	}
	addSearchFlags(cmd, f)
	return cmd
}

func addSearchFlags(cmd *cobra.Command, f *searchFlags) {
	cmd.Flags().StringVar(&f.from, "from", "", "Filter by From header (substring)")
	cmd.Flags().StringVar(&f.to, "to", "", "Filter by To header")
	cmd.Flags().StringVar(&f.cc, "cc", "", "Filter by Cc header")
	cmd.Flags().StringVar(&f.subject, "subject", "", "Filter by Subject")
	cmd.Flags().StringVar(&f.body, "body", "", "Substring in message body")
	cmd.Flags().StringVar(&f.text, "text", "", "Substring in headers OR body")
	cmd.Flags().StringVar(&f.since, "since", "", "Internal-date since YYYY-MM-DD")
	cmd.Flags().StringVar(&f.before, "before", "", "Internal-date before YYYY-MM-DD")
	cmd.Flags().StringVar(&f.sentSince, "sent-since", "", "Sent-date since YYYY-MM-DD")
	cmd.Flags().StringVar(&f.sentBefore, "sent-before", "", "Sent-date before YYYY-MM-DD")
	cmd.Flags().Uint32Var(&f.largerThan, "larger", 0, "Only messages larger than N bytes")
	cmd.Flags().Uint32Var(&f.smallerThan, "smaller", 0, "Only messages smaller than N bytes")
	cmd.Flags().StringSliceVar(&f.keywords, "keyword", nil, "Require a keyword flag (repeatable)")
	cmd.Flags().StringSliceVar(&f.unkeywords, "without-keyword", nil, "Forbid a keyword flag (repeatable)")
	cmd.Flags().BoolVar(&f.unseen, "unseen", false, "Only unseen messages")
	cmd.Flags().BoolVar(&f.seen, "seen", false, "Only seen messages")
	cmd.Flags().BoolVar(&f.flagged, "flagged", false, "Only flagged/starred messages")
	cmd.Flags().BoolVar(&f.answered, "answered", false, "Only answered messages")
	cmd.Flags().IntVar(&f.limit, "limit", 0, "Cap results (most recent N UIDs)")
}

func buildCriteria(f *searchFlags) (imapsearch.Criteria, error) {
	c := imapsearch.Criteria{
		From: f.from, To: f.to, Cc: f.cc,
		Subject: f.subject, Body: f.body, Text: f.text,
		LargerThan: f.largerThan, SmallerThan: f.smallerThan,
		Keywords: trimAll(f.keywords), Unkeywords: trimAll(f.unkeywords),
	}
	var err error
	if c.Since, err = parseDate(f.since, "since"); err != nil {
		return c, err
	}
	if c.Before, err = parseDate(f.before, "before"); err != nil {
		return c, err
	}
	if c.SentSince, err = parseDate(f.sentSince, "sent-since"); err != nil {
		return c, err
	}
	if c.SentBefore, err = parseDate(f.sentBefore, "sent-before"); err != nil {
		return c, err
	}
	if f.unseen && f.seen {
		return c, cerr.Validation("--unseen and --seen are mutually exclusive")
	}
	if f.unseen {
		b := false
		c.Seen = &b
	} else if f.seen {
		b := true
		c.Seen = &b
	}
	if f.flagged {
		b := true
		c.Flagged = &b
	}
	if f.answered {
		b := true
		c.Answered = &b
	}
	return c, nil
}

func parseDate(s, name string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, cerr.Validation("--%s must be YYYY-MM-DD: %s", name, err.Error())
	}
	return t, nil
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func cmdSearch(mailbox string, f *searchFlags) error {
	crit, err := buildCriteria(f)
	if err != nil {
		return err
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
	matches, err := imapsearch.Search(c, crit, f.limit)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "UID SEARCH")
	}

	w := termio.Default()
	for _, m := range matches {
		row := map[string]any{
			"type":    "match",
			"mailbox": resolved,
			"uid":     m.UID,
			"subject": m.Subject,
			"from":    m.From,
			"to":      m.To,
			"date":    m.Date,
			"flags":   m.Flags,
			"size":    m.Size,
		}
		if err := w.PrintNDJSON(row); err != nil {
			return cerr.Internal(err, "print match")
		}
	}
	return w.PrintNDJSON(map[string]any{
		"type":    "summary",
		"mailbox": resolved,
		"count":   len(matches),
	})
}
