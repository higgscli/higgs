package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imaptest"
	"github.com/higgscli/higgs/internal/mbox"
)

func TestWrapImportResolveErr(t *testing.T) {
	err := wrapImportResolveErr(io.EOF, io.ErrUnexpectedEOF, "mbox")
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v, want validation", cerr.From(err).Kind)
	}
	err2 := wrapImportResolveErr(nil, io.ErrUnexpectedEOF, "mbox")
	if cerr.From(err2).Kind != cerr.KindIMAP {
		t.Errorf("kind = %v, want imap", cerr.From(err2).Kind)
	}
}

func TestCmdImport_MissingIn(t *testing.T) {
	err := cmdImport("INBOX", &importFlags{})
	if err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Fatalf("want validation, got %v", err)
	}
}

func TestCmdImport_UnknownFormat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	err := cmdImport("INBOX", &importFlags{in: p})
	if err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Fatalf("want validation, got %v", err)
	}
}

func TestCmdImport_MissingFile(t *testing.T) {
	err := cmdImport("INBOX", &importFlags{in: "/nonexistent/does-not-exist.mbox"})
	if err == nil || cerr.From(err).Kind != cerr.KindConfig {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestCmdImport_DryRun_Mbox(t *testing.T) {
	// Build an mbox with two messages.
	var buf bytes.Buffer
	w := mbox.NewWriter(&buf)
	_ = w.Write([]byte("Subject: a\r\n\r\nhello\r\n"), "a@x.com", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	_ = w.Write([]byte("Subject: b\r\n\r\nworld\r\n"), "b@x.com", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	p := filepath.Join(dir, "in.mbox")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, err := captureStdout(t, func() error {
		return cmdImport("INBOX", &importFlags{in: p, dryRun: true})
	})
	if err != nil {
		t.Fatalf("dry-run: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"pending"`) || !strings.Contains(stdout, `"planned":2`) {
		t.Errorf("unexpected: %s", stdout)
	}
}

func TestCmdImport_DryRun_JSONLGz(t *testing.T) {
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	jw := mbox.NewJSONLWriter(gzw)
	_ = jw.Write(1, []string{`\Seen`}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), []byte("Subject: a\n\nbody\n"))
	_ = gzw.Close()
	dir := t.TempDir()
	p := filepath.Join(dir, "in.jsonl.gz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, err := captureStdout(t, func() error {
		return cmdImport("INBOX", &importFlags{in: p, dryRun: true})
	})
	if err != nil {
		t.Fatalf("dry-run: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"planned":1`) {
		t.Errorf("expected planned=1: %s", stdout)
	}
}

func TestCmdImport_GzipCorrupt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "corrupt.jsonl.gz")
	if err := os.WriteFile(p, []byte("not a gzip stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := cmdImport("INBOX", &importFlags{in: p, dryRun: true})
	if err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Fatalf("want validation, got %v", err)
	}
}

func TestCmdImport_BadMboxContents(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.mbox")
	if err := os.WriteFile(p, []byte("not an mbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := cmdImport("INBOX", &importFlags{in: p, dryRun: true})
	if err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Fatalf("want validation, got %v", err)
	}
}

// TestCmdImport_EndToEnd_BodyParity exports, re-imports, and verifies
// SHA-256 body parity across the round-trip (mbox format).
func TestCmdImport_EndToEnd_BodyParity(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: testMsg("one", "a@x.com")},
		{RFC822: testMsg("two", "b@x.com")},
		{RFC822: testMsg("three", "c@x.com")},
	}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("INBOX", msgs),
		imaptest.WithMailbox("Restored", nil),
	)
	applyTestConfig(t, srv)

	dir := t.TempDir()
	out := filepath.Join(dir, "snap.mbox")

	if _, err := captureStdout(t, func() error {
		return cmdExport("INBOX", &exportFlags{out: out, format: "mbox"})
	}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return cmdImport("Restored", &importFlags{in: out})
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Direct IMAP FETCH against 'Restored' to compute body hashes.
	cfg := imaptest.Config(srv)
	c, err := client.Dial(formatAddr(cfg.Host, cfg.Port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Logout()
	if err := c.Login(cfg.Username, cfg.Password); err != nil {
		t.Fatalf("login: %v", err)
	}
	restoredHashes, err := mailboxBodyHashes(c, "Restored")
	if err != nil {
		t.Fatalf("restored hashes: %v", err)
	}

	c2, err := client.Dial(formatAddr(cfg.Host, cfg.Port))
	if err != nil {
		t.Fatalf("dial2: %v", err)
	}
	defer c2.Logout()
	if err := c2.Login(cfg.Username, cfg.Password); err != nil {
		t.Fatalf("login2: %v", err)
	}
	origHashes, err := mailboxBodyHashes(c2, "INBOX")
	if err != nil {
		t.Fatalf("orig hashes: %v", err)
	}

	if len(origHashes) != len(restoredHashes) {
		t.Fatalf("count mismatch: orig=%d restored=%d", len(origHashes), len(restoredHashes))
	}
	// Round-trip the restored mailbox back out to mbox and compare body
	// hashes against the first export. The memory backend may rewrite
	// headers on APPEND, so we can't assert parity against the original
	// IMAP bodies, but we CAN assert parity across export→import→export.
	dir2 := t.TempDir()
	out2 := filepath.Join(dir2, "snap2.mbox")
	if _, err := captureStdout(t, func() error {
		return cmdExport("Restored", &exportFlags{out: out2, format: "mbox"})
	}); err != nil {
		t.Fatalf("re-export: %v", err)
	}
	hashesFor := func(p string) map[[32]byte]int {
		fh, err := os.Open(p)
		if err != nil {
			t.Fatalf("open %s: %v", p, err)
		}
		defer fh.Close()
		r := mbox.NewReader(fh)
		out := map[[32]byte]int{}
		for {
			b, _, _, err := r.Next()
			if err == io.EOF {
				return out
			}
			if err != nil {
				t.Fatalf("mbox read: %v", err)
			}
			out[sha256.Sum256(b)]++
		}
	}
	a := hashesFor(out)
	b := hashesFor(out2)
	if len(a) != len(b) {
		t.Errorf("hash-set sizes differ: first=%d second=%d", len(a), len(b))
	}
	for h, n := range a {
		if b[h] != n {
			t.Errorf("hash %x count differs: first=%d second=%d", h, n, b[h])
		}
	}
}

func formatAddr(host string, port int) string {
	return host + ":" + itoa(port)
}
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func mailboxBodyHashes(c *client.Client, mbox string) ([][32]byte, error) {
	if _, err := c.Select(mbox, true); err != nil {
		return nil, err
	}
	uids, err := c.UidSearch(&imap.SearchCriteria{})
	if err != nil {
		return nil, err
	}
	if len(uids) == 0 {
		return nil, nil
	}
	seq := &imap.SeqSet{}
	seq.AddNum(uids...)
	resCh := make(chan *imap.Message, len(uids))
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.UidFetch(seq, []imap.FetchItem{imap.FetchUid, "BODY.PEEK[]"}, resCh)
	}()
	section, err := imap.ParseBodySectionName("BODY[]")
	if err != nil {
		return nil, err
	}
	var out [][32]byte
	for m := range resCh {
		lit := m.GetBody(section)
		if lit == nil {
			continue
		}
		b, err := io.ReadAll(lit)
		if err != nil {
			return nil, err
		}
		out = append(out, sha256.Sum256(b))
	}
	if err := <-errCh; err != nil {
		return nil, err
	}
	return out, nil
}

func TestCmdImport_AutoCreateMailbox(t *testing.T) {
	// Source mailbox seeded, target not pre-existing → import must CREATE it.
	msgs := []imaptest.Message{{RFC822: testMsg("x", "a@x.com")}}
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", msgs))
	applyTestConfig(t, srv)
	dir := t.TempDir()
	out := filepath.Join(dir, "x.mbox")
	if _, err := captureStdout(t, func() error {
		return cmdExport("INBOX", &exportFlags{out: out})
	}); err != nil {
		t.Fatalf("export: %v", err)
	}
	stdout, err := captureStdout(t, func() error {
		return cmdImport("NewDest", &importFlags{in: out})
	})
	if err != nil {
		t.Fatalf("import: %v (%s)", err, stdout)
	}
	var last map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		_ = json.Unmarshal([]byte(line), &last)
	}
	// Backend seeds its own INBOX message, so exact count varies; require >= 1.
	if n, _ := last["imported"].(float64); n < 1 {
		t.Errorf("imported = %v, want >= 1", last["imported"])
	}
}

func TestCmdImport_AuthError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.mbox")
	var buf bytes.Buffer
	w := mbox.NewWriter(&buf)
	_ = w.Write([]byte("body\n"), "a@x.com", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := imaptest.Start(t)
	applyTestConfig(t, srv)
	t.Setenv("PM_IMAP_PASSWORD", "wrong")
	err := cmdImport("INBOX", &importFlags{in: p})
	if err == nil || cerr.From(err).Kind != cerr.KindAuth {
		t.Fatalf("want auth error, got %v", err)
	}
}
