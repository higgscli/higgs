package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapsearch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/mbox"
	"github.com/higgscli/higgs/internal/termio"
)

type exportFlags struct {
	out    string
	format string
	since  string
	limit  int
	gzip   bool
}

func newExportCmd() *cobra.Command {
	f := &exportFlags{}
	cmd := &cobra.Command{
		Use:   "export <mailbox>",
		Short: "Export a mailbox to an mbox or JSONL file (optionally gzipped)",
		Long: `export writes every message (or the most recent --limit) from <mailbox>
to --out in either mbox or JSONL format. Each exported message emits an
{"type":"exported"} NDJSON row; the stream ends with a summary line.`,
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdExport(args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.out, "out", "", "Output file path (required)")
	cmd.Flags().StringVar(&f.format, "format", "", "Output format: mbox or jsonl (auto-detected from --out if empty)")
	cmd.Flags().StringVar(&f.since, "since", "", "Only export messages with internal-date on/after YYYY-MM-DD")
	cmd.Flags().IntVar(&f.limit, "limit", 0, "Cap to the most recent N matching UIDs (0 = no cap)")
	cmd.Flags().BoolVar(&f.gzip, "gzip", false, "Wrap output file with gzip compression")
	return cmd
}

// formatKind enumerates the supported export/import file encodings.
type formatKind int

const (
	formatUnknown formatKind = iota
	formatMbox
	formatJSONL
)

func (f formatKind) String() string {
	switch f {
	case formatMbox:
		return "mbox"
	case formatJSONL:
		return "jsonl"
	default:
		return "unknown"
	}
}

// resolveFormat picks a format from the explicit --format flag, falling
// back to the file extension. Also reports whether the file is gzipped.
func resolveFormat(explicit, path string, explicitGzip bool) (formatKind, bool, error) {
	lower := strings.ToLower(path)
	gz := explicitGzip || strings.HasSuffix(lower, ".gz")
	trimmed := strings.TrimSuffix(lower, ".gz")

	switch strings.ToLower(strings.TrimSpace(explicit)) {
	case "mbox":
		return formatMbox, gz, nil
	case "jsonl":
		return formatJSONL, gz, nil
	case "":
		switch {
		case strings.HasSuffix(trimmed, ".mbox"):
			return formatMbox, gz, nil
		case strings.HasSuffix(trimmed, ".jsonl"), strings.HasSuffix(trimmed, ".ndjson"):
			return formatJSONL, gz, nil
		}
		return formatUnknown, gz, fmt.Errorf("cannot infer format from path %q; pass --format mbox|jsonl", path)
	default:
		return formatUnknown, gz, fmt.Errorf("unknown --format %q (want mbox or jsonl)", explicit)
	}
}

// countingWriter wraps an io.Writer to track bytes written.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func cmdExport(mailbox string, f *exportFlags) error {
	if strings.TrimSpace(f.out) == "" {
		return cerr.Validation("--out is required")
	}
	format, gz, err := resolveFormat(f.format, f.out, f.gzip)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}
	var since time.Time
	if s := strings.TrimSpace(f.since); s != "" {
		t, perr := time.Parse("2006-01-02", s)
		if perr != nil {
			return cerr.Validation("--since must be YYYY-MM-DD: %s", perr.Error())
		}
		since = t
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
	if _, err := c.Select(resolved, true); err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "SELECT %q", resolved)
	}
	crit := imapsearch.Criteria{Since: since}
	uids, err := imapsearch.SearchUIDs(c, crit)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "UID SEARCH")
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	if f.limit > 0 && len(uids) > f.limit {
		uids = uids[len(uids)-f.limit:]
	}

	// Open output file with optional gzip wrapping.
	file, err := os.Create(f.out)
	if err != nil {
		return cerr.Config("create output: %s", err.Error())
	}
	defer file.Close()

	cw := &countingWriter{w: file}
	var sink io.Writer = cw
	var gzw *gzip.Writer
	if gz {
		gzw = gzip.NewWriter(cw)
		sink = gzw
	}

	w := termio.Default()
	exported := 0
	if len(uids) > 0 {
		exported, err = streamExport(c, uids, format, sink, w, resolved)
		if err != nil {
			if gzw != nil {
				_ = gzw.Close()
			}
			return err
		}
	}
	if gzw != nil {
		if err := gzw.Close(); err != nil {
			return cerr.Internal(err, "gzip close")
		}
	}
	if err := file.Sync(); err != nil {
		return cerr.Internal(err, "fsync")
	}
	return w.PrintNDJSON(map[string]any{
		"type":          "summary",
		"mailbox":       resolved,
		"exported":      exported,
		"path":          f.out,
		"format":        format.String(),
		"gzip":          gz,
		"bytes_written": cw.n,
	})
}

// streamExport fetches the UIDs one-by-one (bounded memory) and emits
// them to the sink. Returns the number of messages successfully written.
func streamExport(c *client.Client, uids []uint32, format formatKind, sink io.Writer, w *termio.Writer, mailbox string) (int, error) {
	var mw *mbox.Writer
	var jw *mbox.JSONLWriter
	switch format {
	case formatMbox:
		mw = mbox.NewWriter(sink)
	case formatJSONL:
		jw = mbox.NewJSONLWriter(sink)
	default:
		return 0, cerr.Validation("unsupported format")
	}

	count := 0
	for _, uid := range uids {
		msg, err := fetchOneFull(c, uid)
		if err != nil {
			return count, cerr.IMAP(imapclient.Wrap(err), "FETCH uid=%d", uid)
		}
		sender := ""
		if msg.env != nil && len(msg.env.From) > 0 {
			sender = msg.env.From[0].Address()
		}
		switch format {
		case formatMbox:
			if err := mw.Write(msg.body, sender, msg.internalDate); err != nil {
				return count, cerr.Internal(err, "mbox write")
			}
		case formatJSONL:
			if err := jw.Write(uid, msg.flags, msg.internalDate, msg.body); err != nil {
				return count, cerr.Internal(err, "jsonl write")
			}
		}
		if err := w.PrintNDJSON(map[string]any{
			"type":          "exported",
			"mailbox":       mailbox,
			"uid":           uid,
			"size":          len(msg.body),
			"internal_date": msg.internalDate.UTC().Format(time.RFC3339),
		}); err != nil {
			return count, cerr.Internal(err, "print")
		}
		count++
	}
	if mw != nil {
		_ = mw.Close()
	}
	if jw != nil {
		_ = jw.Close()
	}
	return count, nil
}

// fullMessage is the subset of IMAP data needed for export.
type fullMessage struct {
	uid          uint32
	body         []byte
	flags        []string
	internalDate time.Time
	env          *imap.Envelope
}

// fetchOneFull issues a single UID FETCH with the body, flags, envelope
// and internal date needed to reconstitute the message.
func fetchOneFull(c *client.Client, uid uint32) (*fullMessage, error) {
	seq := &imap.SeqSet{}
	seq.AddNum(uid)
	resCh := make(chan *imap.Message, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.UidFetch(seq, []imap.FetchItem{
			imap.FetchUid,
			imap.FetchFlags,
			imap.FetchEnvelope,
			imap.FetchInternalDate,
			"BODY.PEEK[]",
		}, resCh)
	}()
	var got *imap.Message
	for m := range resCh {
		got = m
	}
	if err := <-errCh; err != nil {
		return nil, err
	}
	if got == nil {
		return nil, fmt.Errorf("uid %d not found", uid)
	}
	section, err := imap.ParseBodySectionName("BODY[]")
	if err != nil {
		return nil, err
	}
	lit := got.GetBody(section)
	if lit == nil {
		return nil, fmt.Errorf("no BODY[] for uid=%d", uid)
	}
	body, err := io.ReadAll(lit)
	if err != nil {
		return nil, fmt.Errorf("read body uid=%d: %w", uid, err)
	}
	flags := append([]string{}, got.Flags...)
	return &fullMessage{
		uid:          uid,
		body:         body,
		flags:        flags,
		internalDate: got.InternalDate.UTC(),
		env:          got.Envelope,
	}, nil
}
