package main

import (
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imaptest"
)

// `higgs verify <mailbox> --uid <list|-> [--expect present|absent|exact]`
// audits a mailbox against an expected UID set without mutating anything:
//
//   - present (default): every given UID must exist in the mailbox
//   - absent:            no given UID may exist in the mailbox
//   - exact:             the mailbox UID set must equal the given set
//
// One {"type":"violation"} row per mismatch (with "uid", "mailbox",
// "expected", "actual"), then a {"type":"summary"} row with "expect",
// "checked", "ok", "violations". Zero violations exits 0; otherwise the
// command returns an IMAP-kind error (exit 5), mirroring how the write
// commands report post-verification failures.

func seedVerifyInbox(t *testing.T) {
	t.Helper()
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("a", "a@x.com")},
		{RFC822: testMsg("b", "b@x.com")},
		{RFC822: testMsg("c", "c@x.com")},
	}))
	applyTestConfig(t, srv)
}

func runVerify(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	root.SetArgs(append([]string{"verify"}, args...))
	return captureStdout(t, func() error { return root.Execute() })
}

func TestVerifyPresentOK(t *testing.T) {
	seedVerifyInbox(t)
	stdout, err := runVerify(t, "INBOX", "--uid", "1,2,3")
	if err != nil {
		t.Fatalf("verify: %v (%s)", err, stdout)
	}
	rows := ndjsonRows(t, stdout)
	if n := len(rowsOfType(rows, "violation")); n != 0 {
		t.Errorf("got %d violation rows, want 0", n)
	}
	sum := summaryRow(t, rows)
	if sum["expect"] != "present" || sum["checked"].(float64) != 3 ||
		sum["ok"].(float64) != 3 || sum["violations"].(float64) != 0 {
		t.Errorf("summary: %v", sum)
	}
}

func TestVerifyPresentMissing(t *testing.T) {
	seedVerifyInbox(t)
	stdout, err := runVerify(t, "INBOX", "--uid", "2,9")
	if err == nil {
		t.Fatal("expected error when an expected-present UID is missing")
	}
	if cerr.From(err).Kind != cerr.KindIMAP {
		t.Errorf("kind = %v, want IMAP", cerr.From(err).Kind)
	}
	rows := ndjsonRows(t, stdout)
	viol := rowsOfType(rows, "violation")
	if len(viol) != 1 {
		t.Fatalf("got %d violations, want 1: %s", len(viol), stdout)
	}
	if viol[0]["uid"].(float64) != 9 || viol[0]["expected"] != "present" || viol[0]["actual"] != "absent" {
		t.Errorf("violation row: %v", viol[0])
	}
	sum := summaryRow(t, rows)
	if sum["ok"].(float64) != 1 || sum["violations"].(float64) != 1 || sum["checked"].(float64) != 2 {
		t.Errorf("summary: %v", sum)
	}
}

func TestVerifyAbsent(t *testing.T) {
	seedVerifyInbox(t)
	// All absent: fine.
	stdout, err := runVerify(t, "INBOX", "--uid", "8,9", "--expect", "absent")
	if err != nil {
		t.Fatalf("absent ok case: %v (%s)", err, stdout)
	}
	if sum := summaryRow(t, ndjsonRows(t, stdout)); sum["violations"].(float64) != 0 {
		t.Errorf("summary: %v", sum)
	}

	// UID 1 still present: violation.
	stdout, err = runVerify(t, "INBOX", "--uid", "1,9", "--expect", "absent")
	if err == nil {
		t.Fatal("expected error when an expected-absent UID is present")
	}
	rows := ndjsonRows(t, stdout)
	viol := rowsOfType(rows, "violation")
	if len(viol) != 1 || viol[0]["uid"].(float64) != 1 ||
		viol[0]["expected"] != "absent" || viol[0]["actual"] != "present" {
		t.Errorf("violation rows: %v", viol)
	}
}

func TestVerifyExact(t *testing.T) {
	seedVerifyInbox(t)
	// Exact match passes.
	if _, err := runVerify(t, "INBOX", "--uid", "1,2,3", "--expect", "exact"); err != nil {
		t.Fatalf("exact ok case: %v", err)
	}

	// Mailbox has an extra UID 3 beyond the expected set.
	stdout, err := runVerify(t, "INBOX", "--uid", "1,2", "--expect", "exact")
	if err == nil {
		t.Fatal("expected error for unexpected extra UID")
	}
	rows := ndjsonRows(t, stdout)
	viol := rowsOfType(rows, "violation")
	if len(viol) != 1 || viol[0]["uid"].(float64) != 3 ||
		viol[0]["expected"] != "absent" || viol[0]["actual"] != "present" {
		t.Errorf("extra-UID violation rows: %v", viol)
	}

	// Expected set has UID 9 the mailbox lacks, and mailbox has extra 2,3.
	stdout, err = runVerify(t, "INBOX", "--uid", "1,9", "--expect", "exact")
	if err == nil {
		t.Fatal("expected error for exact mismatch")
	}
	rows = ndjsonRows(t, stdout)
	if n := len(rowsOfType(rows, "violation")); n != 3 {
		t.Errorf("got %d violations, want 3 (missing 9, extra 2, extra 3): %s", n, stdout)
	}
}

func TestVerifyStdinUIDs(t *testing.T) {
	seedVerifyInbox(t)
	stdin := `{"type":"match","uid":1}` + "\n" + `{"type":"match","uid":2}` + "\n" +
		`{"type":"summary","count":2}` + "\n"
	var stdout string
	var err error
	withStdin(t, stdin, func() {
		root := newRootCmd()
		root.SetArgs([]string{"verify", "INBOX", "--uid", "-"})
		stdout, err = captureStdout(t, func() error { return root.Execute() })
	})
	if err != nil {
		t.Fatalf("verify --uid -: %v (%s)", err, stdout)
	}
	if sum := summaryRow(t, ndjsonRows(t, stdout)); sum["checked"].(float64) != 2 {
		t.Errorf("summary: %v", sum)
	}
}

func TestVerifyValidation(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")

	// Missing --uid.
	if _, err := runVerify(t, "INBOX"); err == nil {
		t.Fatal("expected validation error without --uid")
	} else if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v, want validation", cerr.From(err).Kind)
	}

	// Bad --expect value.
	if _, err := runVerify(t, "INBOX", "--uid", "1", "--expect", "bogus"); err == nil {
		t.Fatal("expected validation error for bad --expect")
	} else if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v, want validation", cerr.From(err).Kind)
	}
}

func TestVerifyCmdWiring(t *testing.T) {
	root := newRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "verify" {
			if c.Annotations["stdout_format"] != "ndjson" {
				t.Errorf("stdout_format = %q, want ndjson", c.Annotations["stdout_format"])
			}
			return
		}
	}
	t.Fatal("root command has no verify subcommand")
}
