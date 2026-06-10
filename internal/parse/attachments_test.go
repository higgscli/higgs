package parse

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- fixtures -------------------------------------------------------------

const plainOnly = "From: a@x.com\r\n" +
	"To: b@x.com\r\n" +
	"Subject: hi\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"just a body\r\n"

const altTextHTML = "From: a@x.com\r\n" +
	"To: b@x.com\r\n" +
	"Subject: alt\r\n" +
	"Content-Type: multipart/alternative; boundary=BOUND\r\n" +
	"\r\n" +
	"--BOUND\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"plain body\r\n" +
	"--BOUND\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<p>html body</p>\r\n" +
	"--BOUND--\r\n"

const mixedPDF = "From: a@x.com\r\n" +
	"To: b@x.com\r\n" +
	"Subject: mixed\r\n" +
	"Content-Type: multipart/mixed; boundary=MIX\r\n" +
	"\r\n" +
	"--MIX\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"see attached\r\n" +
	"--MIX\r\n" +
	"Content-Type: application/pdf; name=\"report.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"report.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"aGVsbG8gcGRm\r\n" + // "hello pdf"
	"--MIX--\r\n"

const inlineImageRelated = "From: a@x.com\r\n" +
	"To: b@x.com\r\n" +
	"Subject: related\r\n" +
	"Content-Type: multipart/related; boundary=REL\r\n" +
	"\r\n" +
	"--REL\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<img src=cid:pic>\r\n" +
	"--REL\r\n" +
	"Content-Type: image/png\r\n" +
	"Content-Disposition: inline; filename=\"pic.png\"\r\n" +
	"Content-ID: <pic>\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"iVBORw0KGgo=\r\n" +
	"--REL--\r\n"

const nestedMixed = "From: a@x.com\r\n" +
	"To: b@x.com\r\n" +
	"Subject: nested\r\n" +
	"Content-Type: multipart/mixed; boundary=OUT\r\n" +
	"\r\n" +
	"--OUT\r\n" +
	"Content-Type: multipart/alternative; boundary=INN\r\n" +
	"\r\n" +
	"--INN\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"t\r\n" +
	"--INN\r\n" +
	"Content-Type: text/html\r\n" +
	"\r\n" +
	"<p>h</p>\r\n" +
	"--INN--\r\n" +
	"--OUT\r\n" +
	"Content-Type: application/octet-stream\r\n" +
	"Content-Disposition: attachment; filename=\"blob.bin\"\r\n" +
	"\r\n" +
	"raw blob bytes\r\n" +
	"--OUT--\r\n"

const encodingsMixed = "From: a@x.com\r\n" +
	"To: b@x.com\r\n" +
	"Subject: enc\r\n" +
	"Content-Type: multipart/mixed; boundary=E\r\n" +
	"\r\n" +
	"--E\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"body\r\n" +
	"--E\r\n" +
	"Content-Type: text/csv; name=\"a.csv\"\r\n" +
	"Content-Disposition: attachment; filename=\"a.csv\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"YSxiLGMK\r\n" +
	"--E\r\n" +
	"Content-Type: text/plain; name=\"b.txt\"\r\n" +
	"Content-Disposition: attachment; filename=\"b.txt\"\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"hi=3Dthere\r\n" +
	"--E\r\n" +
	"Content-Type: text/plain; name=\"c.txt\"\r\n" +
	"Content-Disposition: attachment; filename=\"c.txt\"\r\n" +
	"Content-Transfer-Encoding: 7bit\r\n" +
	"\r\n" +
	"seven\r\n" +
	"--E--\r\n"

const hostileAttachment = "From: a@x.com\r\n" +
	"To: b@x.com\r\n" +
	"Subject: hostile\r\n" +
	"Content-Type: multipart/mixed; boundary=H\r\n" +
	"\r\n" +
	"--H\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"body\r\n" +
	"--H\r\n" +
	"Content-Type: application/octet-stream; name=\"..\\\\..\\\\evil\"\r\n" +
	"Content-Disposition: attachment; filename=\"..\\\\..\\\\evil\"\r\n" +
	"\r\n" +
	"danger\r\n" +
	"--H--\r\n"

const emptyFilenameZeroByte = "From: a@x.com\r\n" +
	"To: b@x.com\r\n" +
	"Subject: empty\r\n" +
	"Content-Type: multipart/mixed; boundary=Z\r\n" +
	"\r\n" +
	"--Z\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"b\r\n" +
	"--Z\r\n" +
	"Content-Type: application/pdf\r\n" +
	"Content-Disposition: attachment\r\n" +
	"\r\n" +
	"--Z\r\n" +
	"Content-Type: application/octet-stream; name=\"\"\r\n" +
	"Content-Disposition: attachment; filename=\"\"\r\n" +
	"\r\n" +
	"\r\n" +
	"--Z--\r\n"

// --- tests ----------------------------------------------------------------

func TestWalkAttachmentsPlainOnly(t *testing.T) {
	got, err := WalkAttachments([]byte(plainOnly))
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 attachments, got %+v", got)
	}
}

func TestWalkAttachmentsAltTextHTML(t *testing.T) {
	got, err := WalkAttachments([]byte(altTextHTML))
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("text+html should yield 0 attachments, got %+v", got)
	}
}

func TestWalkAttachmentsMixedPDF(t *testing.T) {
	got, err := WalkAttachments([]byte(mixedPDF))
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 attachment, got %d: %+v", len(got), got)
	}
	a := got[0]
	if a.Filename != "report.pdf" {
		t.Errorf("filename = %q", a.Filename)
	}
	if a.ContentType != "application/pdf" {
		t.Errorf("content type = %q", a.ContentType)
	}
	if a.Disposition != "attachment" {
		t.Errorf("disposition = %q", a.Disposition)
	}
	if a.Size != int64(len("hello pdf")) {
		t.Errorf("size = %d", a.Size)
	}
	if len(a.SHA256) != 64 {
		t.Errorf("sha256 length = %d", len(a.SHA256))
	}
	if a.SHA256 != strings.ToLower(a.SHA256) {
		t.Errorf("sha256 not lowercase: %q", a.SHA256)
	}
}

func TestWalkAttachmentsInlineImage(t *testing.T) {
	got, err := WalkAttachments([]byte(inlineImageRelated))
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 inline non-text part, got %d", len(got))
	}
	a := got[0]
	if a.ContentType != "image/png" {
		t.Errorf("content type = %q", a.ContentType)
	}
	if a.Disposition != "inline" {
		t.Errorf("disposition = %q", a.Disposition)
	}
	if a.ContentID != "pic" {
		t.Errorf("content id = %q", a.ContentID)
	}
	if a.Size == 0 {
		t.Errorf("size should be > 0")
	}
}

func TestWalkAttachmentsNested(t *testing.T) {
	got, err := WalkAttachments([]byte(nestedMixed))
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 attachment, got %d: %+v", len(got), got)
	}
	if got[0].Filename != "blob.bin" {
		t.Errorf("filename = %q", got[0].Filename)
	}
}

func TestWalkAttachmentsEncodings(t *testing.T) {
	got, err := WalkAttachments([]byte(encodingsMixed))
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 attachments, got %d", len(got))
	}
	// Base64 a.csv decodes to "a,b,c\n"
	if got[0].Size != int64(len("a,b,c\n")) {
		t.Errorf("csv size = %d", got[0].Size)
	}
	// QP b.txt: "hi=3Dthere" decodes to "hi=there"
	if got[1].Size != int64(len("hi=there")) {
		t.Errorf("qp size = %d", got[1].Size)
	}
	// 7bit c.txt: "seven"
	if got[2].Size != int64(len("seven")) {
		t.Errorf("7bit size = %d", got[2].Size)
	}
}

func TestWalkAttachmentsHostileFilename(t *testing.T) {
	got, err := WalkAttachments([]byte(hostileAttachment))
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(got))
	}
	// The metadata itself keeps the raw filename; sanitisation happens at
	// extract time.
	if got[0].Filename == "" {
		t.Errorf("expected filename to be parsed; got empty")
	}
}

func TestWalkAttachmentsEmptyFilenameZeroByte(t *testing.T) {
	got, err := WalkAttachments([]byte(emptyFilenameZeroByte))
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	// Two attachments: one PDF with empty filename, one zero-byte octet.
	if len(got) != 2 {
		t.Fatalf("expected 2 attachments, got %d: %+v", len(got), got)
	}
	for _, a := range got {
		if a.Size > 16 {
			t.Errorf("unexpectedly large attachment: %+v", a)
		}
	}
}

func TestWalkAttachmentsEmptyInput(t *testing.T) {
	_, err := WalkAttachments(nil)
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestExtractAttachmentMatch(t *testing.T) {
	var buf bytes.Buffer
	a, err := ExtractAttachment([]byte(mixedPDF), func(att Attachment) bool {
		return att.Filename == "report.pdf"
	}, &buf)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if a.Filename != "report.pdf" {
		t.Errorf("filename = %q", a.Filename)
	}
	if buf.String() != "hello pdf" {
		t.Errorf("body = %q", buf.String())
	}
}

func TestExtractAttachmentNil(t *testing.T) {
	var buf bytes.Buffer
	a, err := ExtractAttachment([]byte(mixedPDF), nil, &buf)
	if err != nil {
		t.Fatalf("extract nil match: %v", err)
	}
	if a.Filename != "report.pdf" {
		t.Errorf("filename = %q", a.Filename)
	}
}

func TestExtractAttachmentNotFound(t *testing.T) {
	var buf bytes.Buffer
	_, err := ExtractAttachment([]byte(plainOnly), func(Attachment) bool { return true }, &buf)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want ErrNotExist", err)
	}
}

func TestExtractAttachmentEmptyInput(t *testing.T) {
	var buf bytes.Buffer
	_, err := ExtractAttachment(nil, nil, &buf)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractAllHappy(t *testing.T) {
	dir := t.TempDir()
	got, err := ExtractAll([]byte(mixedPDF), filepath.Join(dir, "new"), nil)
	if err != nil {
		t.Fatalf("extract all: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	data, err := os.ReadFile(filepath.Join(dir, "new", got[0].Filename))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello pdf" {
		t.Errorf("contents = %q", string(data))
	}
	// Confirm 0700 directory perms on new dir (POSIX modes only; Windows does
	// not enforce them).
	info, err := os.Stat(filepath.Join(dir, "new"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("dir perms = %v, want 0700", info.Mode().Perm())
	}
}

func TestExtractAllHostile(t *testing.T) {
	dir := t.TempDir()
	got, err := ExtractAll([]byte(hostileAttachment), dir, nil)
	if err != nil {
		t.Fatalf("extract all: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	name := got[0].Filename
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		t.Errorf("sanitised name still contains separator: %q", name)
	}
	if strings.Contains(name, "..") {
		t.Errorf("sanitised name still contains '..': %q", name)
	}
	// File must exist inside dir.
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Errorf("file missing: %v", err)
	}
}

func TestExtractAllCollision(t *testing.T) {
	// Craft a message with two attachments that share the same filename so
	// collision suffixing is exercised.
	const dup = "From: a@x.com\r\n" +
		"To: b@x.com\r\n" +
		"Subject: dup\r\n" +
		"Content-Type: multipart/mixed; boundary=D\r\n" +
		"\r\n" +
		"--D\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"same.bin\"\r\n" +
		"\r\n" +
		"one\r\n" +
		"--D\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"same.bin\"\r\n" +
		"\r\n" +
		"two\r\n" +
		"--D--\r\n"

	dir := t.TempDir()
	got, err := ExtractAll([]byte(dup), dir, nil)
	if err != nil {
		t.Fatalf("extract all: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Filename == got[1].Filename {
		t.Errorf("expected collision rename, got %q and %q", got[0].Filename, got[1].Filename)
	}
}

func TestExtractAllFilter(t *testing.T) {
	dir := t.TempDir()
	got, err := ExtractAll([]byte(encodingsMixed), dir, func(a Attachment) bool {
		return a.Filename == "a.csv"
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 filtered, got %d: %+v", len(got), got)
	}
}

func TestExtractAllEmptyFilename(t *testing.T) {
	dir := t.TempDir()
	got, err := ExtractAll([]byte(emptyFilenameZeroByte), dir, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d: %+v", len(got), got)
	}
	for _, a := range got {
		if a.Filename == "" {
			t.Errorf("filename should have fallback")
		}
	}
}

func TestSafeFilename(t *testing.T) {
	cases := []struct {
		in, mt string
	}{
		{"nice.txt", "text/plain"},
		{"../evil.sh", "application/octet-stream"},
		{"..\\..\\evil", "application/octet-stream"},
		{"", "application/pdf"},
		{".", ""},
		{"..", ""},
		{"null\x00byte", ""},
		{"/abs/path/foo.pdf", "application/pdf"},
	}
	for _, c := range cases {
		got := safeFilename(c.in, c.mt)
		if got == "" {
			t.Errorf("safeFilename(%q,%q) returned empty", c.in, c.mt)
		}
		if strings.Contains(got, "/") || strings.Contains(got, "\\") {
			t.Errorf("safeFilename(%q,%q)=%q contains separator", c.in, c.mt, got)
		}
		if strings.HasPrefix(got, ".") {
			t.Errorf("safeFilename(%q,%q)=%q begins with dot", c.in, c.mt, got)
		}
		if strings.Contains(got, "\x00") {
			t.Errorf("safeFilename(%q,%q)=%q contains NUL", c.in, c.mt, got)
		}
	}
	// Specific checks for explicit fallbacks.
	if got := safeFilename("", "application/pdf"); !strings.HasSuffix(got, ".pdf") {
		t.Errorf("empty+pdf should fall back to .pdf, got %q", got)
	}
	if got := safeFilename("", ""); got != "attachment.bin" {
		t.Errorf("empty+empty should be attachment.bin, got %q", got)
	}
}

func TestSafeFilenameLong(t *testing.T) {
	long := strings.Repeat("a", 300) + ".ext"
	got := safeFilename(long, "")
	if len(got) > 200 {
		t.Errorf("length %d exceeds cap", len(got))
	}
	if !strings.HasSuffix(got, ".ext") {
		t.Errorf("ext not preserved: %q", got)
	}
}

func TestAvoidCollision(t *testing.T) {
	used := map[string]struct{}{"a.txt": {}, "a.1.txt": {}}
	got := avoidCollision("a.txt", used)
	if got != "a.2.txt" {
		t.Errorf("got %q", got)
	}
	if avoidCollision("b.txt", used) != "b.txt" {
		t.Error("non-clash should return input")
	}
}

func TestPathToString(t *testing.T) {
	if pathToString(nil) != "" {
		t.Error("nil should be empty")
	}
	if got := pathToString([]int{0, 1}); got != "1.2" {
		t.Errorf("got %q", got)
	}
}
