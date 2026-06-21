package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/state"
)

func TestBackfillCmdShape(t *testing.T) {
	cmd := newBackfillCmd()
	if cmd.Annotations["stdout_format"] != "json" {
		t.Errorf("stdout_format = %q", cmd.Annotations["stdout_format"])
	}
	if cmd.Use != "backfill <logfile>" {
		t.Errorf("use = %q", cmd.Use)
	}
}

func TestBackfillMissingFile(t *testing.T) {
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	err := cmdBackfill(filepath.Join(t.TempDir(), "nope.log"))
	if err == nil {
		t.Fatal("expected error opening file")
	}
	e := cerr.From(err)
	if e.Kind != cerr.KindInternal {
		t.Errorf("kind = %v, want internal", e.Kind)
	}
}

func TestBackfillFromLog(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("PM_STATE_DB", dbPath)

	// Construct a small classify-style log with:
	// - 2 valid JSON records (one with apply succeeded as indicated by the
	//   human-readable "Applied labels to uid=" line)
	// - 1 record with an error field (skipped)
	// - 1 record with missing mailbox (skipped)
	// - 1 non-JSON line (ignored)
	// - 1 malformed JSON (errors++)
	logContent := `Loading config from environment
Applied labels to uid=101
{"mailbox":"Folders/Accounts","uid":101,"uid_validity":10,"subject":"s1","from":"a@b.com","date":"2024-01-01T00:00:00Z","suggested_labels":["Orders"],"confidence":0.9,"rationale":"r","is_mailing_list":false}
{"mailbox":"Folders/Accounts","uid":102,"uid_validity":10,"subject":"s2","from":"c@d.com","date":"2024-01-01T00:00:00Z","suggested_labels":["Finance"],"confidence":0.8,"rationale":"r","is_mailing_list":false}
{"mailbox":"Folders/Accounts","uid":103,"uid_validity":10,"subject":"err","error":"boom"}
{"mailbox":"","uid":0,"uid_validity":0}
{malformed json
Done.
`
	logFile := filepath.Join(t.TempDir(), "classify.log")
	if err := os.WriteFile(logFile, []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, err := captureStdout(t, func() error { return cmdBackfill(logFile) })
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil {
		t.Fatalf("unmarshal: %v\nstdout: %s", err, stdout)
	}

	if got := obj["inserted"].(float64); got != 2 {
		t.Errorf("inserted = %v, want 2", got)
	}
	// 2 skipped: record with error field, and record with empty mailbox.
	if got := obj["skipped"].(float64); got != 2 {
		t.Errorf("skipped = %v, want 2", got)
	}
	// 1 error: malformed JSON line.
	if got := obj["errors"].(float64); got != 1 {
		t.Errorf("errors = %v, want 1", got)
	}

	// Verify DB contents.
	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	total, applied, _, err := db.GetStats("Folders/Accounts")
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("state total = %d, want 2", total)
	}
	// uid=101 was matched by the "Applied labels to uid=101" line. uid=102 was not.
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}
}
