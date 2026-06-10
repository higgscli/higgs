package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akeemjenkins/protoncli/internal/cerr"
	"github.com/akeemjenkins/protoncli/internal/imaptest"
)

func TestExtractCmdFlags(t *testing.T) {
	cmd := newExtractCmd()
	if cmd.Annotations["stdout_format"] != "ndjson" {
		t.Errorf("stdout_format = %q", cmd.Annotations["stdout_format"])
	}
	for _, name := range []string{"schema", "preset", "uid", "model"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag %q", name)
		}
	}
}

func TestLoadExtractSchema_Validation(t *testing.T) {
	if _, err := loadExtractSchema("", ""); err == nil {
		t.Error("expected error when neither provided")
	}
	if _, err := loadExtractSchema("a.json", "invoice"); err == nil {
		t.Error("expected mutex error")
	}
	if _, err := loadExtractSchema("", "bogus-preset"); err == nil {
		t.Error("expected bad preset error")
	}

	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExtractSchema(good, ""); err != nil {
		t.Errorf("unexpected: %v", err)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExtractSchema(bad, ""); err == nil {
		t.Error("expected parse error")
	}

	missing := filepath.Join(dir, "missing.json")
	if _, err := loadExtractSchema(missing, ""); err == nil {
		t.Error("expected read error")
	}

	// Valid preset.
	if _, err := loadExtractSchema("", "invoice"); err != nil {
		t.Errorf("preset invoice: %v", err)
	}
}

func TestExtractValidation_NoUID(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	root := newExtractCmd()
	root.SetArgs([]string{"INBOX", "--preset", "invoice"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v", cerr.From(err).Kind)
	}
}

func TestExtractValidation_NoSchemaOrPreset(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	root := newExtractCmd()
	root.SetArgs([]string{"INBOX", "--uid", "1"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v", cerr.From(err).Kind)
	}
}

func TestExtractHappy(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("Invoice", "billing@x.com")},
	}))
	applyTestConfig(t, srv)
	ollama := fakeOllamaJSON(t, `{"amount":42,"currency":"USD","vendor":"Acme","invoice_number":"INV-1","due_date":"2026-05-01","line_items":[]}`)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "m")

	root := newExtractCmd()
	root.SetArgs([]string{"INBOX", "--preset", "invoice", "--uid", "1"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("extract: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"extraction"`) {
		t.Errorf("missing extraction row: %s", stdout)
	}
	if !strings.Contains(stdout, `"vendor":"Acme"`) {
		t.Errorf("missing data: %s", stdout)
	}
}
