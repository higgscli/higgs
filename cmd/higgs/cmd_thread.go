package main

import (
	"sort"

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

type threadFlags struct {
	uid       uint32
	messageID string
}

func newThreadCmd() *cobra.Command {
	f := &threadFlags{}
	cmd := &cobra.Command{
		Use:   "thread <mailbox>",
		Short: "Stream every message in the same thread as the given anchor",
		Long: `thread returns every message in the same conversation as the anchor message.
The anchor is selected by --uid or --message-id. Rows are emitted in date
order (ascending) and end with a {"type":"summary"} row.`,
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdThread(args[0], f)
		},
	}
	cmd.Flags().Uint32Var(&f.uid, "uid", 0, "UID of the anchor message")
	cmd.Flags().StringVar(&f.messageID, "message-id", "", "Message-ID of the anchor message")
	return cmd
}

func cmdThread(mailbox string, f *threadFlags) error {
	if f.uid == 0 && f.messageID == "" {
		return cerr.Validation("one of --uid or --message-id is required")
	}
	if f.uid != 0 && f.messageID != "" {
		return cerr.Validation("--uid and --message-id are mutually exclusive")
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

	envs, err := fetchThreadEnvelopes(c, imapsearch.Criteria{}, 0)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "fetch envelopes")
	}

	threads := imapthread.Build(envs)
	target := findThread(threads, f)
	if target == nil {
		return cerr.Validation("anchor not found in mailbox %q", resolved)
	}

	// Flatten target thread into a list sorted by date ascending.
	flat := flattenThread(target)
	sort.SliceStable(flat, func(i, j int) bool {
		if flat[i].Date != flat[j].Date {
			return flat[i].Date < flat[j].Date
		}
		return flat[i].UID < flat[j].UID
	})

	w := termio.Default()
	for _, n := range flat {
		if err := w.PrintNDJSON(map[string]any{
			"type":        "thread_message",
			"uid":         n.UID,
			"subject":     n.Subject,
			"from":        n.From,
			"date":        n.Date,
			"message_id":  n.MessageID,
			"in_reply_to": n.InReplyTo,
			"references":  n.References,
		}); err != nil {
			return cerr.Internal(err, "print thread message")
		}
	}
	return w.PrintNDJSON(map[string]any{
		"type":    "summary",
		"mailbox": resolved,
		"count":   len(flat),
	})
}

func findThread(threads []*imapthread.Thread, f *threadFlags) *imapthread.Thread {
	for _, th := range threads {
		for _, uid := range th.UIDs {
			if f.uid != 0 && uid == f.uid {
				return th
			}
		}
		if f.messageID != "" {
			for _, n := range flattenThread(th) {
				if n.MessageID == f.messageID {
					return th
				}
			}
		}
	}
	return nil
}

func flattenThread(th *imapthread.Thread) []*imapthread.Node {
	if th == nil || th.Root == nil {
		return nil
	}
	var out []*imapthread.Node
	var walk func(n *imapthread.Node)
	walk = func(n *imapthread.Node) {
		out = append(out, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(th.Root)
	return out
}
