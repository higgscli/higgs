// Package mbox provides streaming writers and readers for the classic
// mbox format used by Unix mail spools. Writes are in mboxrd style (only
// lines matching ^>*From  are escaped by prefixing an additional '>');
// reads accept both mboxo and mboxrd conventions, unescaping any line of
// the form ^>+From .
package mbox

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// asctime is the traditional mbox timestamp format, e.g.
// "Mon Jan  2 15:04:05 2006". We emit it in UTC.
const asctime = "Mon Jan _2 15:04:05 2006"

// fromLineRE matches any line starting with zero-or-more '>' characters
// followed by "From " — the pattern whose body occurrences must be
// escaped on write (mboxrd) and unescaped on read.
var fromLineRE = regexp.MustCompile(`^>*From `)

// Writer serializes messages to an io.Writer in mboxrd format.
// Multiple messages are separated by a blank line. Writer does not own
// or close the underlying writer.
type Writer struct {
	w       io.Writer
	count   int
	lastErr error
}

// NewWriter returns a Writer that serializes messages to w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Write emits one message. sender is used in the "From " separator line;
// it is sanitized to avoid embedded whitespace that would confuse
// readers. ts is the envelope timestamp (UTC asctime). The body is
// normalized to LF line endings and mboxrd-escaped on the fly.
func (w *Writer) Write(rfc822 []byte, sender string, ts time.Time) error {
	if w.lastErr != nil {
		return w.lastErr
	}
	if w.count > 0 {
		if _, err := io.WriteString(w.w, "\n"); err != nil {
			w.lastErr = err
			return err
		}
	}
	sender = sanitizeSender(sender)
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	header := fmt.Sprintf("From %s %s\n", sender, ts.UTC().Format(asctime))
	if _, err := io.WriteString(w.w, header); err != nil {
		w.lastErr = err
		return err
	}
	if err := writeEscapedBody(w.w, rfc822); err != nil {
		w.lastErr = err
		return err
	}
	w.count++
	return nil
}

// Close is a no-op retained for API symmetry and forward compatibility.
func (w *Writer) Close() error { return nil }

// writeEscapedBody writes body with \r\n normalized to \n and lines
// matching ^>*From  prefixed with an additional '>'. It guarantees the
// output ends with exactly one trailing newline.
func writeEscapedBody(out io.Writer, body []byte) error {
	if len(body) == 0 {
		// Ensure at least a blank line terminator follows the From line.
		_, err := io.WriteString(out, "\n")
		return err
	}
	// Normalize CRLF -> LF for stable scanning.
	normalized := bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	// Ensure trailing newline so the next message's blank separator is clean.
	if normalized[len(normalized)-1] != '\n' {
		normalized = append(normalized, '\n')
	}
	bw := bufio.NewWriter(out)
	start := 0
	for i := 0; i < len(normalized); i++ {
		if normalized[i] != '\n' {
			continue
		}
		line := normalized[start:i]
		if fromLineRE.Match(line) {
			if err := bw.WriteByte('>'); err != nil {
				return err
			}
		}
		if _, err := bw.Write(line); err != nil {
			return err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return err
		}
		start = i + 1
	}
	return bw.Flush()
}

func sanitizeSender(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown@localhost"
	}
	// Collapse whitespace; any space inside breaks the "From " separator.
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			out = append(out, '_')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// Reader parses messages from an mbox stream. It is tolerant of both
// mboxo and mboxrd conventions on read.
type Reader struct {
	r     *bufio.Reader
	bufLn []byte // peeked "From " header line for the next message, if any
	done  bool
}

// NewReader returns a Reader wrapping r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReader(r)}
}

// Next returns the next message as its RFC822 body along with the sender
// and timestamp parsed from the "From " separator. Returns io.EOF when
// the stream is exhausted.
func (r *Reader) Next() (rfc822 []byte, sender string, ts time.Time, err error) {
	if r.done {
		return nil, "", time.Time{}, io.EOF
	}
	var header []byte
	if r.bufLn != nil {
		header = r.bufLn
		r.bufLn = nil
	} else {
		// Skip any blank lines between messages.
		for {
			line, e := r.readLine()
			if e == io.EOF {
				r.done = true
				return nil, "", time.Time{}, io.EOF
			}
			if e != nil {
				return nil, "", time.Time{}, e
			}
			if len(bytes.TrimRight(line, "\r\n")) == 0 {
				continue
			}
			header = line
			break
		}
	}
	if !bytes.HasPrefix(header, []byte("From ")) {
		return nil, "", time.Time{}, fmt.Errorf("mbox: expected From line, got %q", truncForErr(header))
	}
	sender, ts = parseFromLine(header)

	var body bytes.Buffer
	hasNext := false
	for {
		line, e := r.readLine()
		if e == io.EOF {
			r.done = true
			break
		}
		if e != nil {
			return nil, "", time.Time{}, e
		}
		if bytes.HasPrefix(line, []byte("From ")) {
			// Start of next message. Stash it and stop.
			r.bufLn = line
			hasNext = true
			break
		}
		// mboxrd/mboxo: strip one leading '>' from lines matching >+From .
		if fromLineRE.Match(line) {
			// Remove exactly one '>' prefix.
			line = line[1:]
		}
		body.Write(line)
	}
	out := body.Bytes()
	if hasNext {
		// Writer always injects a blank "\n" separator before the next
		// message. Strip one trailing "\n" to recover the original body.
		out = bytes.TrimSuffix(out, []byte("\n"))
	}
	return out, sender, ts, nil
}

// readLine reads one newline-terminated line including its '\n'.
// Returns io.EOF when there is no more data. A final unterminated line
// is returned with its content and no error.
func (r *Reader) readLine() ([]byte, error) {
	line, err := r.r.ReadBytes('\n')
	if err == io.EOF {
		if len(line) == 0 {
			return nil, io.EOF
		}
		// Unterminated trailing line — synthesize a newline so body
		// reconstruction is stable.
		line = append(line, '\n')
		return line, nil
	}
	if err != nil {
		return nil, err
	}
	return line, nil
}

func parseFromLine(line []byte) (string, time.Time) {
	s := strings.TrimRight(string(line), "\r\n")
	rest := strings.TrimPrefix(s, "From ")
	// Split sender <space> timestamp.
	// The sender token has no spaces (we sanitized on write) but we handle
	// "unknown" third-party mboxes defensively by taking the first field.
	space := strings.IndexByte(rest, ' ')
	if space < 0 {
		return strings.TrimSpace(rest), time.Time{}
	}
	sender := rest[:space]
	tsStr := strings.TrimSpace(rest[space+1:])
	for _, layout := range []string{asctime, time.ANSIC, time.UnixDate, time.RFC1123Z, time.RFC1123, time.RFC3339} {
		if ts, err := time.Parse(layout, tsStr); err == nil {
			return sender, ts.UTC()
		}
	}
	return sender, time.Time{}
}

func truncForErr(b []byte) string {
	s := strings.TrimRight(string(b), "\r\n")
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// ErrCorrupt is returned for unrecoverable framing errors. Currently all
// framing errors surface directly from Next, but the sentinel is exposed
// for callers that want to branch on it.
var ErrCorrupt = errors.New("mbox: corrupt stream")
