package main

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/termio"
)

func newScanFoldersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan-folders",
		Short: "List IMAP mailboxes as JSON",
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,2,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdScanFolders()
		},
	}
	return cmd
}

func cmdScanFolders() error {
	termio.Info("Loading config from environment")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return cerr.Config("%s", err.Error())
	}

	c, err := imapclient.Dial(cfg.IMAP)
	if err != nil {
		return cerr.Auth("failed to connect/login IMAP: %s", err.Error())
	}
	defer imapclient.CloseAndLogout(c)

	termio.Info("Listing mailboxes (IMAP LIST)")
	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "LIST failed")
	}
	termio.Info("Found %d mailboxes", len(mboxes))

	type mailboxJSON struct {
		Name       string   `json:"name"`
		Delimiter  string   `json:"delimiter"`
		Messages   *uint32  `json:"messages,omitempty"`
		Unseen     *uint32  `json:"unseen,omitempty"`
		Attributes []string `json:"attributes"`
	}

	rows := make([]mailboxJSON, 0, len(mboxes))
	for _, m := range mboxes {
		r := mailboxJSON{
			Name:       m.Name,
			Delimiter:  string(m.Delim),
			Attributes: m.Attrs,
		}
		if r.Attributes == nil {
			r.Attributes = []string{}
		}
		if m.NumMessages != nil && m.NumUnseen != nil {
			r.Messages = m.NumMessages
			r.Unseen = m.NumUnseen
		}
		rows = append(rows, r)
	}

	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})

	allMail := ""
	if name, ok := imaputil.DetectAllMail(mboxes); ok {
		allMail = name
	}
	labelsRoot := ""
	if name, ok := imaputil.DetectLabelsRoot(mboxes); ok {
		labelsRoot = name
	}

	result := map[string]any{
		"mailboxes":   rows,
		"all_mail":    allMail,
		"labels_root": labelsRoot,
	}

	return termio.Default().PrintJSON(result)
}
