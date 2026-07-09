package main

import (
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imaptest"
)

// These tests pin `--uid -`: every command that accepts an explicit --uid
// list must also accept "-", meaning "read the UID set from stdin". Stdin
// accepts two shapes, mixed freely line by line:
//
//   - plain UID tokens separated by commas and/or whitespace ("1,2 3\n4")
//   - NDJSON objects, from which a numeric top-level "uid" field is taken;
//     object lines without a numeric "uid" (e.g. summary rows) are skipped
//
// UIDs are deduplicated. This makes `higgs search ... | higgs archive
// INBOX --uid -` work directly on higgs's own NDJSON output.

func seedInbox(t *testing.T, n int) *imaptest.Server {
	t.Helper()
	msgs := make([]imaptest.Message, n)
	for i := range msgs {
		msgs[i] = imaptest.Message{RFC822: testMsg("m"+string(rune('a'+i)), "a@x.com")}
	}
	srv := imaptest.Start(t,
		imaptest.WithMailbox("INBOX", msgs),
		imaptest.WithMailbox("Archive", nil),
	)
	applyTestConfig(t, srv)
	return srv
}

// archiveDryRunUIDs runs `archive INBOX --uid - --dry-run` with the given
// stdin and returns the planned UIDs.
func archiveDryRunUIDs(t *testing.T, stdin string) ([]uint32, map[string]any, error) {
	t.Helper()
	seedInbox(t, 4)
	var stdout string
	var err error
	withStdin(t, stdin, func() {
		root := newRootCmd()
		root.SetArgs([]string{"archive", "INBOX", "--uid", "-", "--dry-run"})
		stdout, err = captureStdout(t, func() error { return root.Execute() })
	})
	if err != nil {
		return nil, nil, err
	}
	rows := ndjsonRows(t, stdout)
	return uidsOfType(t, rows, "pending"), summaryRow(t, rows), nil
}

func TestUIDStdin_PlainList(t *testing.T) {
	uids, sum, err := archiveDryRunUIDs(t, "1,2\n3 4\n")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !equalUIDs(uids, []uint32{1, 2, 3, 4}) {
		t.Errorf("uids = %v, want [1 2 3 4]", uids)
	}
	if sum["planned"].(float64) != 4 {
		t.Errorf("planned = %v, want 4", sum["planned"])
	}
}

func TestUIDStdin_NDJSON(t *testing.T) {
	stdin := `{"type":"match","mailbox":"INBOX","uid":2,"subject":"x"}` + "\n" +
		`{"type":"match","mailbox":"INBOX","uid":3,"subject":"y"}` + "\n" +
		`{"type":"summary","mailbox":"INBOX","count":2}` + "\n"
	uids, sum, err := archiveDryRunUIDs(t, stdin)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !equalUIDs(uids, []uint32{2, 3}) {
		t.Errorf("uids = %v, want [2 3]", uids)
	}
	if sum["planned"].(float64) != 2 {
		t.Errorf("planned = %v, want 2", sum["planned"])
	}
}

func TestUIDStdin_Dedupe(t *testing.T) {
	uids, _, err := archiveDryRunUIDs(t, "1,1,2\n1\n")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !equalUIDs(uids, []uint32{1, 2}) {
		t.Errorf("uids = %v, want [1 2]", uids)
	}
}

func TestUIDStdin_InvalidToken(t *testing.T) {
	_, _, err := archiveDryRunUIDs(t, "1,abc\n")
	if err == nil {
		t.Fatal("expected validation error for non-numeric token")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v, want validation", cerr.From(err).Kind)
	}
}

func TestUIDStdin_Empty(t *testing.T) {
	_, _, err := archiveDryRunUIDs(t, "")
	if err == nil {
		t.Fatal("expected validation error for empty stdin UID set")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v, want validation", cerr.From(err).Kind)
	}
}

// End-to-end: search output piped into a real (non-dry-run) archive.
func TestUIDStdin_ArchiveMovesMessage(t *testing.T) {
	seedInbox(t, 2)
	var stdout string
	var err error
	withStdin(t, `{"type":"match","uid":1}`+"\n", func() {
		root := newRootCmd()
		root.SetArgs([]string{"archive", "INBOX", "--uid", "-"})
		stdout, err = captureStdout(t, func() error { return root.Execute() })
	})
	if err != nil {
		t.Fatalf("archive: %v (%s)", err, stdout)
	}
	rows := ndjsonRows(t, stdout)
	if got := uidsOfType(t, rows, "archived"); !equalUIDs(got, []uint32{1}) {
		t.Errorf("archived uids = %v, want [1]", got)
	}
}

// The parseUIDList family (explicit --uid commands like attachments) must
// honor "-" too, not just the search-capable write commands.
func TestUIDStdin_Attachments(t *testing.T) {
	seedInbox(t, 1)
	var stdout string
	var err error
	withStdin(t, "1\n", func() {
		root := newRootCmd()
		root.SetArgs([]string{"attachments", "INBOX", "--uid", "-", "--dry-run", "--out", t.TempDir()})
		stdout, err = captureStdout(t, func() error { return root.Execute() })
	})
	if err != nil {
		t.Fatalf("attachments --uid -: %v (%s)", err, stdout)
	}
	summaryRow(t, ndjsonRows(t, stdout))
}

// --uid - must still conflict with --all-matching like any explicit list.
func TestUIDStdin_MutuallyExclusiveWithAllMatching(t *testing.T) {
	seedInbox(t, 1)
	var err error
	withStdin(t, "1\n", func() {
		root := newRootCmd()
		root.SetArgs([]string{"archive", "INBOX", "--uid", "-", "--all-matching"})
		_, err = captureStdout(t, func() error { return root.Execute() })
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v, want validation", cerr.From(err).Kind)
	}
}
