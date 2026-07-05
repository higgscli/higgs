package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-imap/client"
	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/mbox"
	"github.com/higgscli/higgs/internal/termio"
)

type importFlags struct {
	in     string
	format string
	dryRun bool
}

func newImportCmd() *cobra.Command {
	f := &importFlags{}
	cmd := &cobra.Command{
		Use:   "import <mailbox>",
		Short: "Import messages from an mbox or JSONL file (reverse of export)",
		Long: `import reads --in and APPENDs each message to <mailbox>, preserving flags
and internal-date when present. Emits {"type":"imported"} (or
{"type":"pending"} for --dry-run) NDJSON lines followed by a summary.`,
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdImport(args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.in, "in", "", "Input file path (required)")
	cmd.Flags().StringVar(&f.format, "format", "", "Input format: mbox or jsonl (auto-detected from --in if empty)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Parse the file and emit pending rows without touching the server")
	return cmd
}

func cmdImport(mailbox string, f *importFlags) error {
	if strings.TrimSpace(f.in) == "" {
		return cerr.Validation("--in is required")
	}
	format, gz, err := resolveFormat(f.format, f.in, false)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}

	file, err := os.Open(f.in)
	if err != nil {
		return cerr.Config("open input: %s", err.Error())
	}
	defer file.Close()

	var src io.Reader = file
	if gz {
		gzr, err := gzip.NewReader(file)
		if err != nil {
			return cerr.Validation("gzip reader: %s", err.Error())
		}
		defer gzr.Close()
		src = gzr
	}

	// Dry-run skips the IMAP connection entirely.
	w := termio.Default()
	if f.dryRun {
		return runImport(nil, "", src, format, true, w)
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
		// Attempt to create the mailbox so imports are self-healing.
		if cerr := c.Create(mailbox); cerr != nil {
			return wrapImportResolveErr(err, cerr, mailbox)
		}
		resolved = mailbox
	}
	return runImport(c, resolved, src, format, false, w)
}

func wrapImportResolveErr(resolveErr, createErr error, mailbox string) error {
	if resolveErr != nil {
		return cerr.Validation("%s (auto-create also failed: %v)", resolveErr.Error(), createErr)
	}
	return cerr.IMAP(createErr, "CREATE %q", mailbox)
}

type pendingMessage struct {
	body         []byte
	flags        []string
	internalDate time.Time
}

func runImport(c *client.Client, mailbox string, src io.Reader, format formatKind, dryRun bool, w *termio.Writer) error {
	iter, err := newImportIter(src, format)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}

	imported := 0
	index := 0
	for {
		msg, err := iter.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return cerr.Validation("parse message %d: %s", index, err.Error())
		}
		index++
		if dryRun {
			if err := w.PrintNDJSON(map[string]any{
				"type":          "pending",
				"index":         index,
				"size":          len(msg.body),
				"internal_date": msg.internalDate.UTC().Format(time.RFC3339),
				"flags":         msg.flags,
			}); err != nil {
				return cerr.Internal(err, "print")
			}
			continue
		}
		date := msg.internalDate
		if date.IsZero() {
			date = time.Now().UTC()
		}
		if err := c.Append(mailbox, msg.flags, date, bytes.NewReader(msg.body)); err != nil {
			return cerr.IMAP(imapclient.Wrap(err), "APPEND message %d into %q", index, mailbox)
		}
		imported++
		if err := w.PrintNDJSON(map[string]any{
			"type":          "imported",
			"mailbox":       mailbox,
			"index":         index,
			"size":          len(msg.body),
			"internal_date": date.UTC().Format(time.RFC3339),
			"flags":         msg.flags,
		}); err != nil {
			return cerr.Internal(err, "print")
		}
	}
	summary := map[string]any{
		"type":   "summary",
		"format": format.String(),
		"read":   index,
	}
	if dryRun {
		summary["planned"] = index
	} else {
		summary["mailbox"] = mailbox
		summary["imported"] = imported
	}
	return w.PrintNDJSON(summary)
}

// importIter is the small streaming-iterator interface over mbox / jsonl.
type importIter interface {
	Next() (*pendingMessage, error)
}

type mboxIter struct{ r *mbox.Reader }

func (m *mboxIter) Next() (*pendingMessage, error) {
	body, _, ts, err := m.r.Next()
	if err != nil {
		return nil, err
	}
	return &pendingMessage{body: body, flags: []string{}, internalDate: ts}, nil
}

type jsonlIter struct{ r *mbox.JSONLReader }

func (j *jsonlIter) Next() (*pendingMessage, error) {
	row, body, err := j.r.Next()
	if err != nil {
		return nil, err
	}
	flags := row.Flags
	if flags == nil {
		flags = []string{}
	}
	return &pendingMessage{body: body, flags: flags, internalDate: row.InternalDate}, nil
}

func newImportIter(src io.Reader, format formatKind) (importIter, error) {
	switch format {
	case formatMbox:
		return &mboxIter{r: mbox.NewReader(src)}, nil
	case formatJSONL:
		return &jsonlIter{r: mbox.NewJSONLReader(src)}, nil
	default:
		return nil, fmt.Errorf("unsupported format")
	}
}
