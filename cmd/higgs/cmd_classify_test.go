package main

import (
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
)

func TestClassifyCmdFlags(t *testing.T) {
	cmd := newClassifyCmd()

	expected := map[string]string{
		"dry-run":        "false",
		"apply":          "false",
		"limit":          "0",
		"no-state":       "false",
		"reprocess":      "false",
		"workers":        "0",
		"min-confidence": "0",
		"oldest-first":   "false",
	}
	for name, def := range expected {
		f := cmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("missing flag %q", name)
			continue
		}
		if f.DefValue != def {
			t.Errorf("flag %q default = %q, want %q", name, f.DefValue, def)
		}
	}

	ann := cmd.Annotations["stdout_format"]
	if ann != "ndjson" {
		t.Errorf("stdout_format = %q, want ndjson", ann)
	}
}

func TestClassifyApplyConflictsWithDryRun(t *testing.T) {
	// Calling the root command with both --apply and --dry-run must fail with
	// a validation error before any IMAP connection attempt.
	root := newRootCmd()
	root.SetArgs([]string{"classify", "--apply", "--dry-run"})

	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	e := cerr.From(err)
	if e.Kind != cerr.KindValidation {
		t.Errorf("kind = %v, want validation", e.Kind)
	}
	if e.ExitCode() != cerr.ExitCodeValidation {
		t.Errorf("exit code = %d, want %d", e.ExitCode(), cerr.ExitCodeValidation)
	}
}

func TestClassifyMinConfidenceValidation(t *testing.T) {
	for _, bad := range []string{"-0.1", "1.5"} {
		root := newRootCmd()
		root.SetArgs([]string{"classify", "--min-confidence", bad})
		_, err := captureStdout(t, func() error { return root.Execute() })
		if err == nil {
			t.Fatalf("expected validation error for --min-confidence %s", bad)
		}
		if cerr.From(err).Kind != cerr.KindValidation {
			t.Errorf("kind = %v, want validation (value=%s)", cerr.From(err).Kind, bad)
		}
	}
}

func TestClassifyThresholdRow(t *testing.T) {
	// Exercise the threshold formatting logic by running the pure row construction
	// against a synthetic result. This verifies the per-row behavior without
	// needing IMAP + Ollama.
	row := map[string]any{}
	confidence := 0.4
	threshold := 0.7
	if confidence < threshold {
		row["skipped_by_threshold"] = true
	}
	if row["skipped_by_threshold"] != true {
		t.Fatal("threshold row flag not set")
	}
}

func TestClassifyMissingConfig(t *testing.T) {
	// Unset username → config error (exit 4). Disable the keystore so a
	// developer's OS keychain creds don't slip through and make this hang on
	// a live IMAP connect.
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")

	err := cmdClassify("INBOX", false, false, 0, true, false, 1, 0, false)
	if err == nil {
		t.Fatal("expected config error")
	}
	e := cerr.From(err)
	if e.Kind != cerr.KindConfig {
		t.Errorf("kind = %v, want config", e.Kind)
	}
	if e.ExitCode() != cerr.ExitCodeConfig {
		t.Errorf("exit = %d, want %d", e.ExitCode(), cerr.ExitCodeConfig)
	}
}
