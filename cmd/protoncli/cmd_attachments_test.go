package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akeemjenkins/protoncli/internal/cerr"
	"github.com/akeemjenkins/protoncli/internal/email"
	"github.com/akeemjenkins/protoncli/internal/imaptest"
)

// mkAttachmentMessage synthesises a multipart/mixed RFC822 with one text
// body and one attachment. The attachment filename is used verbatim.
func mkAttachmentMessage(subject, from, filename, body string) []byte {
	return []byte("From: " + from + "\r\n" +
		"To: u@x.com\r\n" +
		"Subject: " + subject + "\r\n" +
		"Date: Wed, 01 Jan 2026 00:00:00 +0000\r\n" +
		"Message-ID: <" + subject + "@t>\r\n" +
		"Content-Type: multipart/mixed; boundary=BND\r\n" +
		"\r\n" +
		"--BND\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		body + "\r\n" +
		"--BND\r\n" +
		"Content-Type: application/octet-stream; name=\"" + filename + "\"\r\n" +
		"Content-Disposition: attachment; filename=\"" + filename + "\"\r\n" +
		"\r\n" +
		"payload-bytes-for-" + filename + "\r\n" +
		"--BND--\r\n")
}

// runAttachments executes the attachments subcommand in-process (it is not
// yet registered on the root), returning captured stdout and any error.
func runAttachments(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newAttachmentsCmd()
	cmd.SetArgs(args)
	return captureStdout(t, func() error { return cmd.Execute() })
}

// imaptest purges the memory backend's default INBOX message before seeding,
// so the first message we append via WithMailbox is UID 1.
const firstAppendedUID = "1"

func TestCmdAttachmentsHappy(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: mkAttachmentMessage("A", "a@x.com", "report.pdf", "see attached")},
	}))
	applyTestConfig(t, srv)

	outDir := t.TempDir()
	stdout, err := runAttachments(t, "INBOX", "--uid", firstAppendedUID, "--out", outDir)
	if err != nil {
		t.Fatalf("attachments: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"attachment"`) {
		t.Errorf("missing attachment row: %s", stdout)
	}
	if !strings.Contains(stdout, `"type":"summary"`) {
		t.Errorf("missing summary: %s", stdout)
	}
	if !strings.Contains(stdout, `"extracted":1`) {
		t.Errorf("expected extracted=1: %s", stdout)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "report.pdf"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.HasPrefix(string(data), "payload-bytes-for-report.pdf") {
		t.Errorf("unexpected file contents: %q", string(data))
	}
}

func TestCmdAttachmentsDryRun(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: mkAttachmentMessage("A", "a@x.com", "x.pdf", "hi")},
	}))
	applyTestConfig(t, srv)

	stdout, err := runAttachments(t, "INBOX", "--uid", firstAppendedUID, "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(stdout, `"type":"pending"`) {
		t.Errorf("missing pending rows: %s", stdout)
	}
	if strings.Contains(stdout, `"type":"attachment"`) {
		t.Errorf("dry-run should not emit attachment rows: %s", stdout)
	}
}

func TestCmdAttachmentsFilenameGlob(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: mkAttachmentMessage("A", "a@x.com", "keep.pdf", "b")},
		{RFC822: mkAttachmentMessage("B", "b@x.com", "skip.zip", "b")},
	}))
	applyTestConfig(t, srv)

	outDir := t.TempDir()
	stdout, err := runAttachments(t, "INBOX", "--uid", "1,2", "--filename-glob", "*.pdf", "--out", outDir)
	if err != nil {
		t.Fatalf("attachments: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"filename":"keep.pdf"`) {
		t.Errorf("expected keep.pdf row: %s", stdout)
	}
	if strings.Contains(stdout, "skip.zip") {
		t.Errorf("skip.zip should not be extracted: %s", stdout)
	}
	if !strings.Contains(stdout, `"skipped":1`) {
		t.Errorf("expected skipped=1 in summary: %s", stdout)
	}
}

func TestCmdAttachmentsSizeBounds(t *testing.T) {
	// Payload length differs by filename length: "payload-bytes-for-small" = 23,
	// "payload-bytes-for-bigger" = 24.
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: mkAttachmentMessage("A", "a@x.com", "small", "x")},
		{RFC822: mkAttachmentMessage("B", "b@x.com", "bigger", "x")},
	}))
	applyTestConfig(t, srv)

	outDir := t.TempDir()
	stdout, err := runAttachments(t, "INBOX", "--uid", "1,2", "--min-size", "24", "--out", outDir)
	if err != nil {
		t.Fatalf("attachments: %v", err)
	}
	if !strings.Contains(stdout, "bigger") {
		t.Errorf("expected bigger in output: %s", stdout)
	}
	if strings.Contains(stdout, `"filename":"small"`) {
		t.Errorf("small should be size-filtered: %s", stdout)
	}
}

func TestCmdAttachmentsHostileFilename(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: mkAttachmentMessage("H", "a@x.com", "../../evil", "x")},
	}))
	applyTestConfig(t, srv)

	outDir := t.TempDir()
	stdout, err := runAttachments(t, "INBOX", "--uid", firstAppendedUID, "--out", outDir)
	if err != nil {
		t.Fatalf("attachments: %v (%s)", err, stdout)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no files created; stdout=%s", stdout)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "..") {
			t.Errorf("filename traversal leaked: %q", e.Name())
		}
	}
	// Sibling directory should not have been touched.
	parent := filepath.Dir(outDir)
	siblings, _ := os.ReadDir(parent)
	for _, s := range siblings {
		if s.Name() == "evil" {
			t.Errorf("traversal escaped to parent dir")
		}
	}
}

func TestCmdAttachmentsValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no uid", []string{"INBOX"}},
		{"bad uid", []string{"INBOX", "--uid", "not-a-uid"}},
		{"neg min", []string{"INBOX", "--uid", "1", "--min-size", "-1"}},
		{"min > max", []string{"INBOX", "--uid", "1", "--min-size", "10", "--max-size", "5"}},
		{"bad glob", []string{"INBOX", "--uid", "1", "--filename-glob", "[bad"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("PM_IMAP_USERNAME", "u")
			t.Setenv("PM_IMAP_PASSWORD", "p")
			_, err := runAttachments(t, c.args...)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if cerr.From(err).Kind != cerr.KindValidation {
				t.Errorf("kind = %v, want validation", cerr.From(err).Kind)
			}
		})
	}
}

func TestCmdAttachmentsAnnotations(t *testing.T) {
	cmd := newAttachmentsCmd()
	if cmd.Annotations["stdout_format"] != "ndjson" {
		t.Errorf("stdout_format = %q", cmd.Annotations["stdout_format"])
	}
	if cmd.Annotations["exit_codes"] != "0,3,4,5" {
		t.Errorf("exit_codes = %q", cmd.Annotations["exit_codes"])
	}
	if cmd.Name() != "attachments" {
		t.Errorf("name = %q", cmd.Name())
	}
}

func TestFetchRowWithAttachments(t *testing.T) {
	// Synthesize an RFC822 with one attachment and confirm the row carries
	// attachment metadata without losing the base email.Message fields.
	rfc := mkAttachmentMessage("A", "a@x.com", "doc.pdf", "hello")
	row := fetchRowWithAttachments(email.Message{UID: 42, Subject: "A"}, rfc)
	if row.UID != 42 {
		t.Errorf("embedded UID lost: %d", row.UID)
	}
	if row.Subject != "A" {
		t.Errorf("embedded Subject lost: %q", row.Subject)
	}
	if len(row.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(row.Attachments))
	}
	if row.Attachments[0].Filename != "doc.pdf" {
		t.Errorf("filename = %q", row.Attachments[0].Filename)
	}
	// Must JSON-encode with the "attachments" key present.
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"attachments"`) {
		t.Errorf("missing attachments key: %s", string(b))
	}
}

func TestFetchRowWithAttachmentsEmpty(t *testing.T) {
	// Plain-text only: attachments must be an empty array (not null).
	rfc := []byte("From: a\r\nTo: b\r\nSubject: x\r\nContent-Type: text/plain\r\n\r\nhi\r\n")
	row := fetchRowWithAttachments(email.Message{UID: 1}, rfc)
	if row.Attachments == nil {
		t.Fatal("attachments should be empty slice, not nil")
	}
	if len(row.Attachments) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(row.Attachments))
	}
	b, _ := json.Marshal(row)
	if !strings.Contains(string(b), `"attachments":[]`) {
		t.Errorf("expected empty array literal, got %s", string(b))
	}
}

func TestFetchRowWithAttachmentsWalkError(t *testing.T) {
	// Empty body yields an error inside WalkAttachments; the row should still
	// carry an empty attachments array.
	row := fetchRowWithAttachments(email.Message{UID: 3}, nil)
	if row.UID != 3 {
		t.Errorf("UID lost")
	}
	if row.Attachments == nil || len(row.Attachments) != 0 {
		t.Errorf("attachments should be empty, got %+v", row.Attachments)
	}
}

func TestSanitizeMailboxForPath(t *testing.T) {
	if got := sanitizeMailboxForPath("INBOX"); got != "INBOX" {
		t.Errorf("got %q", got)
	}
	if got := sanitizeMailboxForPath("Folders/Accounts"); got != "Folders_Accounts" {
		t.Errorf("got %q", got)
	}
	if got := sanitizeMailboxForPath(""); got != "mailbox" {
		t.Errorf("got %q", got)
	}
	if got := sanitizeMailboxForPath(".."); got != "mailbox" {
		t.Errorf("got %q", got)
	}
}
