package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/state"
)

// seedStateQueryDB creates a temp state DB, points PM_STATE_DB at it, and
// seeds:
//
//	INBOX   uid 1: mailing list, conf 0.9,  ["Newsletters"],        applied
//	INBOX   uid 2: personal,     conf 0.4,  ["Personal"],           not applied
//	INBOX   uid 3: personal,     conf 0.95, ["Personal","Finance"], apply error "boom"
//	Archive uid 9: mailing list, conf 0.7,  ["Newsletters"],        applied
func seedStateQueryDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	t.Setenv("PM_STATE_DB", path)
	db, err := state.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	seed := func(mailbox string, uid uint32, isML bool, conf float64, labels []string, applied bool, applyErr string) {
		t.Helper()
		if err := db.MarkProcessed(&state.ProcessedMessage{
			Mailbox: mailbox, UIDValidity: 100, UID: uid,
			Subject: "subj", From: "sender@example.com",
			Date:            time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			SuggestedLabels: labels, Confidence: conf, Rationale: "because",
			IsMailingList: isML, LabelsApplied: applied, ApplyError: applyErr,
			ProcessedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("seed uid=%d: %v", uid, err)
		}
	}
	seed("INBOX", 1, true, 0.9, []string{"Newsletters"}, true, "")
	seed("INBOX", 2, false, 0.4, []string{"Personal"}, false, "")
	seed("INBOX", 3, false, 0.95, []string{"Personal", "Finance"}, false, "boom")
	seed("Archive", 9, true, 0.7, []string{"Newsletters"}, true, "")
}

func runStateQuery(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	root.SetArgs(append([]string{"state", "query"}, args...))
	return captureStdout(t, func() error { return root.Execute() })
}

// stateQueryUIDs runs `state query` with args and returns the uids of the
// emitted "message" rows in output order, after checking the summary count.
func stateQueryUIDs(t *testing.T, args ...string) []uint32 {
	t.Helper()
	stdout, err := runStateQuery(t, args...)
	if err != nil {
		t.Fatalf("state query %v: %v (stdout=%s)", args, err, stdout)
	}
	rows := ndjsonRows(t, stdout)
	uids := uidsOfType(t, rows, "message")
	sum := summaryRow(t, rows)
	if int(sum["count"].(float64)) != len(uids) {
		t.Fatalf("summary count %v != %d message rows", sum["count"], len(uids))
	}
	return uids
}

// state query is a purely local command: none of these tests set IMAP env,
// so any attempt to dial IMAP fails loudly.

func TestStateQueryAll(t *testing.T) {
	seedStateQueryDB(t)
	uids := stateQueryUIDs(t)
	if len(uids) != 4 {
		t.Fatalf("got %d rows, want 4: %v", len(uids), uids)
	}
}

func TestStateQueryMailboxArg(t *testing.T) {
	seedStateQueryDB(t)
	if got := stateQueryUIDs(t, "INBOX"); !equalUIDs(got, []uint32{1, 2, 3}) {
		t.Errorf("INBOX: got %v", got)
	}
	if got := stateQueryUIDs(t, "Archive"); !equalUIDs(got, []uint32{9}) {
		t.Errorf("Archive: got %v", got)
	}
}

func TestStateQueryIsMailingList(t *testing.T) {
	seedStateQueryDB(t)
	if got := stateQueryUIDs(t, "INBOX", "--is-mailing-list", "false"); !equalUIDs(got, []uint32{2, 3}) {
		t.Errorf("false: got %v", got)
	}
	if got := stateQueryUIDs(t, "INBOX", "--is-mailing-list", "true"); !equalUIDs(got, []uint32{1}) {
		t.Errorf("true: got %v", got)
	}
}

func TestStateQueryConfidence(t *testing.T) {
	seedStateQueryDB(t)
	if got := stateQueryUIDs(t, "INBOX", "--min-confidence", "0.5"); !equalUIDs(got, []uint32{1, 3}) {
		t.Errorf("min: got %v", got)
	}
	if got := stateQueryUIDs(t, "INBOX", "--max-confidence", "0.5"); !equalUIDs(got, []uint32{2}) {
		t.Errorf("max: got %v", got)
	}
}

func TestStateQueryLabel(t *testing.T) {
	seedStateQueryDB(t)
	if got := stateQueryUIDs(t, "INBOX", "--label", "Personal"); !equalUIDs(got, []uint32{2, 3}) {
		t.Errorf("Personal: got %v", got)
	}
	// Exact element match: a prefix of a label must not match.
	if got := stateQueryUIDs(t, "INBOX", "--label", "Fin"); len(got) != 0 {
		t.Errorf("Fin must match nothing, got %v", got)
	}
}

func TestStateQueryFailedAndApplied(t *testing.T) {
	seedStateQueryDB(t)
	if got := stateQueryUIDs(t, "INBOX", "--failed"); !equalUIDs(got, []uint32{3}) {
		t.Errorf("--failed: got %v", got)
	}
	if got := stateQueryUIDs(t, "INBOX", "--applied", "true"); !equalUIDs(got, []uint32{1}) {
		t.Errorf("--applied=true: got %v", got)
	}
	if got := stateQueryUIDs(t, "INBOX", "--applied", "false"); !equalUIDs(got, []uint32{2, 3}) {
		t.Errorf("--applied=false: got %v", got)
	}
}

func TestStateQueryLimit(t *testing.T) {
	seedStateQueryDB(t)
	if got := stateQueryUIDs(t, "INBOX", "--limit", "2"); len(got) != 2 {
		t.Errorf("--limit 2: got %v", got)
	}
}

func TestStateQueryRowShape(t *testing.T) {
	seedStateQueryDB(t)
	stdout, err := runStateQuery(t, "INBOX", "--failed")
	if err != nil {
		t.Fatalf("state query: %v", err)
	}
	rows := rowsOfType(ndjsonRows(t, stdout), "message")
	if len(rows) != 1 {
		t.Fatalf("got %d message rows, want 1", len(rows))
	}
	row := rows[0]
	// Message rows must carry the full classification record so results are
	// finally queryable after the fact (the original feedback's gap #1), and
	// a numeric "uid" so rows pipe straight into `--uid -` consumers.
	if row["uid"].(float64) != 3 || row["mailbox"] != "INBOX" {
		t.Errorf("identity: %v", row)
	}
	if row["is_mailing_list"] != false || row["confidence"].(float64) != 0.95 {
		t.Errorf("classification: %v", row)
	}
	if row["rationale"] != "because" || row["apply_error"] != "boom" {
		t.Errorf("detail fields: %v", row)
	}
	labels, ok := row["suggested_labels"].([]any)
	if !ok || len(labels) != 2 {
		t.Errorf("suggested_labels: %v", row["suggested_labels"])
	}
}

func TestStateQueryValidation(t *testing.T) {
	seedStateQueryDB(t)
	for _, args := range [][]string{
		{"--is-mailing-list", "bogus"},
		{"--applied", "bogus"},
		{"--min-confidence", "1.5"},
		{"--min-confidence", "-0.1"},
	} {
		if _, err := runStateQuery(t, args...); err == nil {
			t.Errorf("args %v: expected validation error", args)
		} else if cerr.From(err).Kind != cerr.KindValidation {
			t.Errorf("args %v: kind = %v, want validation", args, cerr.From(err).Kind)
		}
	}
}

func TestStateQueryEmptyDB(t *testing.T) {
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	stdout, err := runStateQuery(t)
	if err != nil {
		t.Fatalf("empty DB must not error: %v", err)
	}
	rows := ndjsonRows(t, stdout)
	if got := len(rowsOfType(rows, "message")); got != 0 {
		t.Errorf("got %d message rows, want 0", got)
	}
	if summaryRow(t, rows)["count"].(float64) != 0 {
		t.Errorf("summary count != 0")
	}
}

func TestStateQueryCmdWiring(t *testing.T) {
	stateCmd := newStateCmd()
	var found bool
	for _, sub := range stateCmd.Commands() {
		if sub.Name() == "query" {
			found = true
			if sub.Annotations["stdout_format"] != "ndjson" {
				t.Errorf("stdout_format = %q, want ndjson", sub.Annotations["stdout_format"])
			}
		}
	}
	if !found {
		t.Fatal("state command has no query subcommand")
	}
}
