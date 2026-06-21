package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/parse"
	"github.com/higgscli/higgs/internal/termio"
)

type attachmentFlags struct {
	uids          string
	out           string
	filenameGlob  string
	minSize       int64
	maxSize       int64
	dryRun        bool
}

func newAttachmentsCmd() *cobra.Command {
	f := &attachmentFlags{}
	cmd := &cobra.Command{
		Use:   "attachments <mailbox>",
		Short: "Extract attachment bytes from one or more messages",
		Long: `Fetches the given UIDs from <mailbox> and writes their attachment
payloads to --out (default ./attachments/<mailbox>/<uid>/). Emits one NDJSON
row per attachment, terminated by a summary line.`,
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdAttachments(args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.uids, "uid", "", "Comma-separated UIDs to extract from (required)")
	cmd.Flags().StringVar(&f.out, "out", "", "Output directory (default ./attachments/<mailbox>/<uid>/)")
	cmd.Flags().StringVar(&f.filenameGlob, "filename-glob", "", "Only extract attachments whose filename matches this glob")
	cmd.Flags().Int64Var(&f.minSize, "min-size", 0, "Only extract attachments >= N decoded bytes")
	cmd.Flags().Int64Var(&f.maxSize, "max-size", 0, "Only extract attachments <= N decoded bytes (0 = unlimited)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Emit planned extractions without writing to disk")
	return cmd
}

func cmdAttachments(mailbox string, f *attachmentFlags) error {
	uids, err := parseUIDList(f.uids)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}
	if len(uids) == 0 {
		return cerr.Validation("--uid is required and must list at least one UID")
	}
	if f.minSize < 0 || f.maxSize < 0 {
		return cerr.Validation("--min-size and --max-size must be non-negative")
	}
	if f.maxSize > 0 && f.minSize > f.maxSize {
		return cerr.Validation("--min-size (%d) exceeds --max-size (%d)", f.minSize, f.maxSize)
	}
	if f.filenameGlob != "" {
		// Validate the glob pattern up-front so users get fast feedback.
		if _, err := filepath.Match(f.filenameGlob, "probe"); err != nil {
			return cerr.Validation("invalid --filename-glob: %s", err.Error())
		}
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		return cerr.Config("%s", err.Error())
	}
	c, err := imapclient.Dial(cfg.IMAP)
	if err != nil {
		return cerr.Auth("%s", err.Error())
	}
	defer imapclient.CloseAndLogout(c)

	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "LIST mailboxes")
	}
	resolved, err := imaputil.ResolveMailboxName(mailbox, mboxes)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}
	if _, err := imapfetch.SelectMailbox(c, resolved); err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "SELECT %q", resolved)
	}
	msgs, err := imapfetch.FetchRFC822(c, uids)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "FETCH")
	}

	w := termio.Default()
	extracted, skipped, failed := 0, 0, 0

	filter := func(a parse.Attachment) bool {
		if f.filenameGlob != "" {
			ok, _ := filepath.Match(f.filenameGlob, a.Filename)
			if !ok {
				return false
			}
		}
		if f.minSize > 0 && a.Size < f.minSize {
			return false
		}
		if f.maxSize > 0 && a.Size > f.maxSize {
			return false
		}
		return true
	}

	for _, m := range msgs {
		outDir := f.out
		if outDir == "" {
			outDir = filepath.Join("attachments", sanitizeMailboxForPath(resolved), fmt.Sprintf("%d", m.UID))
		} else if len(msgs) > 1 {
			// When multiple UIDs share an explicit --out, keep them in
			// per-UID subdirectories so filenames can't clash across messages.
			outDir = filepath.Join(outDir, fmt.Sprintf("%d", m.UID))
		}

		atts, err := parse.WalkAttachments(m.RFC822)
		if err != nil {
			failed++
			env := cerr.Internal(err, "walk attachments uid=%d", m.UID).ToEnvelope()["error"]
			_ = w.PrintNDJSON(map[string]any{
				"type":  "error",
				"uid":   m.UID,
				"error": env,
			})
			continue
		}
		for _, a := range atts {
			if !filter(a) {
				skipped++
				continue
			}
			if f.dryRun {
				if err := w.PrintNDJSON(map[string]any{
					"type":         "pending",
					"uid":          m.UID,
					"filename":     a.Filename,
					"content_type": a.ContentType,
					"size":         a.Size,
				}); err != nil {
					return cerr.Internal(err, "print pending")
				}
				extracted++
				continue
			}
		}
		if f.dryRun {
			continue
		}

		// Real mode: extract all matching attachments at once.
		results, err := parse.ExtractAll(m.RFC822, outDir, filter)
		if err != nil {
			failed++
			env := cerr.Internal(err, "extract uid=%d", m.UID).ToEnvelope()["error"]
			_ = w.PrintNDJSON(map[string]any{
				"type":  "error",
				"uid":   m.UID,
				"error": env,
			})
			continue
		}
		for _, a := range results {
			if err := w.PrintNDJSON(map[string]any{
				"type":     "attachment",
				"uid":      m.UID,
				"filename": a.Filename,
				"path":     filepath.Join(outDir, a.Filename),
				"sha256":   a.SHA256,
				"size":     a.Size,
			}); err != nil {
				return cerr.Internal(err, "print attachment")
			}
			extracted++
		}
	}
	return w.PrintNDJSON(map[string]any{
		"type":      "summary",
		"extracted": extracted,
		"skipped":   skipped,
		"failed":    failed,
	})
}

// sanitizeMailboxForPath turns an IMAP mailbox name into a directory-safe
// path component.
func sanitizeMailboxForPath(s string) string {
	r := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"\x00", "_",
	)
	name := r.Replace(s)
	name = strings.Trim(name, ". ")
	if name == "" {
		name = "mailbox"
	}
	return name
}

