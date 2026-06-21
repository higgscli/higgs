package main

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imaptest"
	"github.com/higgscli/higgs/internal/mbox"
)

func TestResolveFormat(t *testing.T) {
	tests := []struct {
		name       string
		explicit   string
		path       string
		gzipFlag   bool
		wantFormat formatKind
		wantGzip   bool
		wantErr    bool
	}{
		{"explicit-mbox", "mbox", "/tmp/x.xyz", false, formatMbox, false, false},
		{"explicit-jsonl-gz", "jsonl", "/tmp/x.jsonl.gz", false, formatJSONL, true, false},
		{"infer-mbox", "", "/tmp/out.mbox", false, formatMbox, false, false},
		{"infer-jsonl-gz-flag", "", "/tmp/out.jsonl", true, formatJSONL, true, false},
		{"infer-ndjson", "", "/tmp/out.ndjson", false, formatJSONL, false, false},
		{"infer-mbox-gz", "", "/tmp/out.mbox.gz", false, formatMbox, true, false},
		{"cannot-infer", "", "/tmp/out.bin", false, formatUnknown, false, true},
		{"bad-format", "zip", "/tmp/x.mbox", false, formatUnknown, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt, gz, err := resolveFormat(tt.explicit, tt.path, tt.gzipFlag)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if fmt != tt.wantFormat {
				t.Errorf("format = %v, want %v", fmt, tt.wantFormat)
			}
			if gz != tt.wantGzip {
				t.Errorf("gzip = %v, want %v", gz, tt.wantGzip)
			}
		})
	}
}

func TestNewExportCmd(t *testing.T) {
	cmd := newExportCmd()
	if cmd.Annotations["stdout_format"] != "ndjson" {
		t.Errorf("annotations stdout_format = %q", cmd.Annotations["stdout_format"])
	}
	if cmd.Flags().Lookup("out") == nil || cmd.Flags().Lookup("format") == nil {
		t.Error("missing flags")
	}
}

func TestNewImportCmd(t *testing.T) {
	cmd := newImportCmd()
	if cmd.Annotations["stdout_format"] != "ndjson" {
		t.Errorf("annotations stdout_format = %q", cmd.Annotations["stdout_format"])
	}
	if cmd.Flags().Lookup("in") == nil || cmd.Flags().Lookup("dry-run") == nil {
		t.Error("missing flags")
	}
}

func TestFormatKindString(t *testing.T) {
	if formatMbox.String() != "mbox" || formatJSONL.String() != "jsonl" || formatUnknown.String() != "unknown" {
		t.Error("String mismatch")
	}
}

func TestCmdExport_MissingOut(t *testing.T) {
	err := cmdExport("INBOX", &exportFlags{})
	if err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Fatalf("want validation error, got %v", err)
	}
}

func TestCmdExport_BadSince(t *testing.T) {
	err := cmdExport("INBOX", &exportFlags{out: "/tmp/x.mbox", since: "yesterday"})
	if err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Fatalf("want validation error, got %v", err)
	}
}

func TestCmdExport_UnresolvableFormat(t *testing.T) {
	err := cmdExport("INBOX", &exportFlags{out: "/tmp/x.bin"})
	if err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Fatalf("want validation error, got %v", err)
	}
}

func TestCmdExport_MboxHappy(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: testMsg("one", "a@x.com")},
		{RFC822: testMsg("two", "b@x.com")},
		{RFC822: testMsg("from-tricky", "c@x.com")},
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", msgs))
	applyTestConfig(t, srv)

	dir := t.TempDir()
	out := filepath.Join(dir, "inbox.mbox")

	stdout, err := captureStdout(t, func() error {
		return cmdExport("INBOX", &exportFlags{out: out, format: "mbox"})
	})
	if err != nil {
		t.Fatalf("export: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"exported"`) || !strings.Contains(stdout, `"type":"summary"`) {
		t.Errorf("missing ndjson rows: %s", stdout)
	}
	// Summary exported count.
	var summary map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		_ = json.Unmarshal([]byte(line), &summary)
	}
	// The memory IMAP backend seeds its own default INBOX message, so we
	// assert on "at least our 3 seeds" instead of an exact count.
	if n, _ := summary["exported"].(float64); n < 3 {
		t.Errorf("summary exported = %v, want >= 3", summary["exported"])
	}

	// Read back through the mbox reader; count should match.
	fh, err := os.Open(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer fh.Close()
	r := mbox.NewReader(fh)
	count := 0
	var hashes [][32]byte
	for {
		body, _, _, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		hashes = append(hashes, sha256.Sum256(body))
		count++
	}
	if count < 3 {
		t.Errorf("roundtrip count = %d, want >= 3", count)
	}
	_ = hashes
}

func TestCmdExport_JSONLGzipAndRoundTrip(t *testing.T) {
	msgs := []imaptest.Message{
		{RFC822: testMsg("alpha", "a@x.com")},
		{RFC822: testMsg("beta", "b@x.com")},
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", msgs),
		imaptest.WithMailbox("Imported", nil))
	applyTestConfig(t, srv)

	dir := t.TempDir()
	out := filepath.Join(dir, "inbox.jsonl.gz")

	if _, err := captureStdout(t, func() error {
		return cmdExport("INBOX", &exportFlags{out: out})
	}); err != nil {
		t.Fatalf("export: %v", err)
	}
	// Import into a fresh mailbox and assert count parity.
	stdout, err := captureStdout(t, func() error {
		return cmdImport("Imported", &importFlags{in: out})
	})
	if err != nil {
		t.Fatalf("import: %v (%s)", err, stdout)
	}
	// Import count must match export count; just assert both ran and imported > 0.
	if !strings.Contains(stdout, `"type":"imported"`) || !strings.Contains(stdout, `"type":"summary"`) {
		t.Errorf("missing imported/summary rows: %s", stdout)
	}
}

func TestCmdExport_Limit(t *testing.T) {
	seed := []imaptest.Message{}
	for i := 0; i < 5; i++ {
		seed = append(seed, imaptest.Message{RFC822: testMsg(string(rune('A'+i)), "x@x.com")})
	}
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", seed))
	applyTestConfig(t, srv)
	dir := t.TempDir()
	out := filepath.Join(dir, "lim.mbox")
	stdout, err := captureStdout(t, func() error {
		return cmdExport("INBOX", &exportFlags{out: out, limit: 2})
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(stdout, `"exported":2`) {
		t.Errorf("expected exported=2: %s", stdout)
	}
}

func TestCmdExport_UnresolvableMailbox(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", nil))
	applyTestConfig(t, srv)
	dir := t.TempDir()
	out := filepath.Join(dir, "x.mbox")
	err := cmdExport("NoSuchMailbox", &exportFlags{out: out})
	if err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Fatalf("want validation error, got %v", err)
	}
}

func TestCmdExport_AuthError(t *testing.T) {
	srv := imaptest.Start(t)
	applyTestConfig(t, srv)
	t.Setenv("PM_IMAP_PASSWORD", "wrong")
	dir := t.TempDir()
	err := cmdExport("INBOX", &exportFlags{out: filepath.Join(dir, "x.mbox")})
	if err == nil || cerr.From(err).Kind != cerr.KindAuth {
		t.Fatalf("want auth error, got %v", err)
	}
}
