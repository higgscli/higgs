package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imaptest"
	"github.com/higgscli/higgs/internal/smtp"
)

// useTLSTestClient swaps unsubscribeHTTPClient for one that trusts the test
// server's self-signed certificate, while preserving the no-redirect policy.
func useTLSTestClient(t *testing.T, ts *httptest.Server) {
	t.Helper()
	c := ts.Client()
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	prev := unsubscribeHTTPClient
	unsubscribeHTTPClient = c
	t.Cleanup(func() { unsubscribeHTTPClient = prev })
}

// msgWithHeaders constructs a test RFC5322 message with additional headers.
func msgWithHeaders(subject, from string, headers map[string]string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: u@x.com\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("Date: Wed, 01 Jan 2026 00:00:00 +0000\r\n")
	b.WriteString("Message-ID: <" + subject + "@t>\r\n")
	for k, v := range headers {
		b.WriteString(k + ": " + v + "\r\n")
	}
	b.WriteString("\r\nhi\r\n")
	return []byte(b.String())
}

// runUnsub creates a fresh unsubscribe command with args and runs it via captureStdout.
func runUnsub(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newUnsubscribeCmd()
	cmd.SetArgs(args)
	return captureStdout(t, func() error { return cmd.Execute() })
}

func TestUnsubscribeCmdAnnotations(t *testing.T) {
	cmd := newUnsubscribeCmd()
	if cmd.Annotations["stdout_format"] != "ndjson" {
		t.Errorf("stdout_format: %q", cmd.Annotations["stdout_format"])
	}
	if cmd.Annotations["exit_codes"] != "0,3,4,5" {
		t.Errorf("exit_codes: %q", cmd.Annotations["exit_codes"])
	}
	for _, name := range []string{"uid", "http-only", "mailto-only", "dry-run"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag %q", name)
		}
	}
}

func TestUnsubscribeValidationBothFilters(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	_, err := runUnsub(t, "INBOX", "--uid", "1", "--http-only", "--mailto-only")
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind: %v", cerr.From(err).Kind)
	}
}

func TestUnsubscribeValidationNoUIDs(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	_, err := runUnsub(t, "INBOX")
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestUnsubscribeDryRunHTTP(t *testing.T) {
	msg := msgWithHeaders("Hi", "sender@x.com", map[string]string{
		"List-Unsubscribe":      "<https://example.com/unsub>, <mailto:unsub@example.com>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	})
	srv := imaptest.Start(t, imaptest.WithMailbox("Newsletters", []imaptest.Message{
		{RFC822: msg},
	}))
	applyTestConfig(t, srv)
	stdout, err := runUnsub(t, "Newsletters", "--uid", "1", "--dry-run")
	if err != nil {
		t.Fatalf("unsubscribe: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"pending"`) {
		t.Errorf("expected pending: %s", stdout)
	}
	if !strings.Contains(stdout, `"method":"http"`) {
		t.Errorf("expected http method: %s", stdout)
	}
	if !strings.Contains(stdout, `"type":"summary"`) {
		t.Errorf("missing summary: %s", stdout)
	}
}

func TestUnsubscribeHTTPOneClick(t *testing.T) {
	var posts int32
	var sawOneClick int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt32(&posts, 1)
		}
		if err := r.ParseForm(); err == nil {
			if r.Form.Get("List-Unsubscribe") == "One-Click" {
				atomic.AddInt32(&sawOneClick, 1)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	useTLSTestClient(t, ts)

	msg := msgWithHeaders("x", "a@x.com", map[string]string{
		"List-Unsubscribe":      "<" + ts.URL + "/unsub>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	})
	srv := imaptest.Start(t, imaptest.WithMailbox("Newsletters", []imaptest.Message{
		{RFC822: msg},
	}))
	applyTestConfig(t, srv)
	stdout, err := runUnsub(t, "Newsletters", "--uid", "1")
	if err != nil {
		t.Fatalf("unsubscribe: %v (%s)", err, stdout)
	}
	if atomic.LoadInt32(&posts) != 1 {
		t.Errorf("expected 1 POST, got %d", posts)
	}
	if atomic.LoadInt32(&sawOneClick) != 1 {
		t.Errorf("expected one-click form, got %d", sawOneClick)
	}
	if !strings.Contains(stdout, `"type":"unsubscribed"`) {
		t.Errorf("missing unsubscribed: %s", stdout)
	}
	if !strings.Contains(stdout, `"succeeded":1`) {
		t.Errorf("missing succeeded count: %s", stdout)
	}
}

func TestUnsubscribeHTTPGetWithoutOneClick(t *testing.T) {
	var method string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	useTLSTestClient(t, ts)

	msg := msgWithHeaders("x", "a@x.com", map[string]string{
		"List-Unsubscribe": "<" + ts.URL + "/unsub>",
	})
	srv := imaptest.Start(t, imaptest.WithMailbox("Newsletters", []imaptest.Message{
		{RFC822: msg},
	}))
	applyTestConfig(t, srv)
	stdout, err := runUnsub(t, "Newsletters", "--uid", "1")
	if err != nil {
		t.Fatalf("unsubscribe: %v (%s)", err, stdout)
	}
	if method != http.MethodGet {
		t.Errorf("expected GET, got %q", method)
	}
	if !strings.Contains(stdout, `"status":204`) {
		t.Errorf("status missing: %s", stdout)
	}
}

func TestUnsubscribeHTTPFailure(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	useTLSTestClient(t, ts)

	msg := msgWithHeaders("x", "a@x.com", map[string]string{
		"List-Unsubscribe":      "<" + ts.URL + "/unsub>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	})
	srv := imaptest.Start(t, imaptest.WithMailbox("Newsletters", []imaptest.Message{
		{RFC822: msg},
	}))
	applyTestConfig(t, srv)
	stdout, err := runUnsub(t, "Newsletters", "--uid", "1")
	if err != nil {
		t.Fatalf("unsubscribe: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"failed"`) {
		t.Errorf("expected failure: %s", stdout)
	}
	if !strings.Contains(stdout, `"failed":1`) {
		t.Errorf("summary: %s", stdout)
	}
}

func TestUnsubscribeMailtoNoSMTP(t *testing.T) {
	msg := msgWithHeaders("x", "a@x.com", map[string]string{
		"List-Unsubscribe": "<mailto:unsub@example.com>",
	})
	srv := imaptest.Start(t, imaptest.WithMailbox("Newsletters", []imaptest.Message{
		{RFC822: msg},
	}))
	applyTestConfig(t, srv)
	// Make sure SMTP env is empty.
	t.Setenv("PM_SMTP_HOST", "")
	t.Setenv("PM_SMTP_PORT", "")
	stdout, err := runUnsub(t, "Newsletters", "--uid", "1")
	if err != nil {
		t.Fatalf("unsubscribe: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"reason":"smtp not configured"`) {
		t.Errorf("skip reason missing: %s", stdout)
	}
}

func TestUnsubscribeMailtoSuccess(t *testing.T) {
	var captured struct {
		from string
		to   []string
		msg  []byte
	}
	prevSend := unsubscribeSend
	unsubscribeSend = func(cfg smtp.Config, from string, to []string, raw []byte) error {
		captured.from = from
		captured.to = to
		captured.msg = raw
		return nil
	}
	t.Cleanup(func() { unsubscribeSend = prevSend })

	prevLookup := unsubscribeSMTPLookup
	unsubscribeSMTPLookup = func() (smtp.Config, bool) {
		return smtp.Config{Host: "127.0.0.1", Port: 25}, true
	}
	t.Cleanup(func() { unsubscribeSMTPLookup = prevLookup })

	msg := msgWithHeaders("x", "a@x.com", map[string]string{
		"List-Unsubscribe": "<mailto:unsub@example.com?subject=goodbye>",
	})
	srv := imaptest.Start(t, imaptest.WithMailbox("Newsletters", []imaptest.Message{
		{RFC822: msg},
	}))
	applyTestConfig(t, srv)
	stdout, err := runUnsub(t, "Newsletters", "--uid", "1")
	if err != nil {
		t.Fatalf("unsubscribe: %v (%s)", err, stdout)
	}
	if len(captured.to) != 1 || captured.to[0] != "unsub@example.com" {
		t.Errorf("bad recipient: %+v", captured.to)
	}
	if !strings.Contains(string(captured.msg), "Subject: goodbye") {
		t.Errorf("custom subject missing: %s", string(captured.msg))
	}
	if !strings.Contains(stdout, `"method":"mailto"`) || !strings.Contains(stdout, `"status":"sent"`) {
		t.Errorf("mailto output: %s", stdout)
	}
}

func TestUnsubscribeHTTPOnly(t *testing.T) {
	msg := msgWithHeaders("x", "a@x.com", map[string]string{
		"List-Unsubscribe": "<mailto:unsub@example.com>",
	})
	srv := imaptest.Start(t, imaptest.WithMailbox("Newsletters", []imaptest.Message{
		{RFC822: msg},
	}))
	applyTestConfig(t, srv)
	stdout, err := runUnsub(t, "Newsletters", "--uid", "1", "--http-only")
	if err != nil {
		t.Fatalf("unsubscribe: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"skipped"`) {
		t.Errorf("expected skipped (no http target): %s", stdout)
	}
}

func TestUnsubscribeMailtoOnly(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	useTLSTestClient(t, ts)

	msg := msgWithHeaders("x", "a@x.com", map[string]string{
		"List-Unsubscribe": "<" + ts.URL + "/u>",
	})
	srv := imaptest.Start(t, imaptest.WithMailbox("Newsletters", []imaptest.Message{
		{RFC822: msg},
	}))
	applyTestConfig(t, srv)
	stdout, err := runUnsub(t, "Newsletters", "--uid", "1", "--mailto-only")
	if err != nil {
		t.Fatalf("unsubscribe: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"skipped"`) {
		t.Errorf("expected skipped (no mailto target): %s", stdout)
	}
}

func TestUnsubscribeNoHeader(t *testing.T) {
	msg := msgWithHeaders("x", "a@x.com", nil)
	srv := imaptest.Start(t, imaptest.WithMailbox("Newsletters", []imaptest.Message{
		{RFC822: msg},
	}))
	applyTestConfig(t, srv)
	stdout, err := runUnsub(t, "Newsletters", "--uid", "1")
	if err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if !strings.Contains(stdout, `"reason":"no List-Unsubscribe header"`) {
		t.Errorf("skip reason: %s", stdout)
	}
}

func TestUnsubscribeHTTPPlaintextSkipped(t *testing.T) {
	msg := msgWithHeaders("x", "a@x.com", map[string]string{
		"List-Unsubscribe": "<http://example.com/unsub>",
	})
	srv := imaptest.Start(t, imaptest.WithMailbox("Newsletters", []imaptest.Message{
		{RFC822: msg},
	}))
	applyTestConfig(t, srv)
	stdout, err := runUnsub(t, "Newsletters", "--uid", "1")
	if err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if !strings.Contains(stdout, `"type":"skipped"`) {
		t.Errorf("expected skipped for http:// URL, got: %s", stdout)
	}
}

func TestUnsubscribePrivateIPBlocked(t *testing.T) {
	// Dial directly to confirm the guard rejects loopback addresses when the
	// URL would otherwise pass HTTPS validation.
	_, err := guardedDialContext(t.Context(), "tcp", "127.0.0.1:443")
	if err == nil {
		t.Fatal("expected error dialing loopback, got nil")
	}
	if !strings.Contains(err.Error(), "private/internal") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseListUnsubscribe(t *testing.T) {
	got := parseListUnsubscribe("<https://x/u>, <mailto:a@x>")
	if len(got) != 2 || got[0] != "https://x/u" || got[1] != "mailto:a@x" {
		t.Errorf("got: %v", got)
	}
	got = parseListUnsubscribe("")
	if len(got) != 0 {
		t.Errorf("empty: %v", got)
	}
	got = parseListUnsubscribe("  ,  <https://x>")
	if len(got) != 1 || got[0] != "https://x" {
		t.Errorf("with empty: %v", got)
	}
}

func TestIsOneClick(t *testing.T) {
	if !isOneClick("List-Unsubscribe=One-Click") {
		t.Error("exact")
	}
	if !isOneClick("  list-unsubscribe=one-click ") {
		t.Error("case+ws")
	}
	if isOneClick("") || isOneClick("something-else") {
		t.Error("false cases")
	}
}

func TestPickUnsubscribeTarget(t *testing.T) {
	targets := []string{"mailto:a@x", "https://x/u"}
	// default prefers http
	if got, m := pickUnsubscribeTarget(targets, false, false); got != "https://x/u" || m != "http" {
		t.Errorf("default: %q %q", got, m)
	}
	if got, m := pickUnsubscribeTarget(targets, true, false); got != "https://x/u" || m != "http" {
		t.Errorf("http-only: %q %q", got, m)
	}
	if got, m := pickUnsubscribeTarget(targets, false, true); got != "mailto:a@x" || m != "mailto" {
		t.Errorf("mailto-only: %q %q", got, m)
	}
	if got, m := pickUnsubscribeTarget([]string{"ftp://x"}, false, false); got != "" || m != "" {
		t.Errorf("unknown: %q %q", got, m)
	}
	if got, _ := pickUnsubscribeTarget([]string{"mailto:a@x"}, true, false); got != "" {
		t.Errorf("http-only no http: %q", got)
	}
}

func TestParseMailto(t *testing.T) {
	to, subj, body, err := parseMailto("mailto:a@x.com?subject=S&body=B")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if to != "a@x.com" || subj != "S" || body != "B" {
		t.Errorf("got: %q %q %q", to, subj, body)
	}
	if _, _, _, err := parseMailto("mailto:"); err == nil {
		t.Error("expected error for empty recipient")
	}
	if _, _, _, err := parseMailto("http://not-mailto"); err == nil {
		t.Error("expected not-mailto error")
	}
}

func TestCmdUnsubscribeReportsMissingUIDs(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := newRootCmd()
	root.SetArgs([]string{"unsubscribe", "INBOX", "--uid", "999", "--dry-run"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("unsubscribe: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"type":"error"`) || !strings.Contains(stdout, `"uid":999`) {
		t.Errorf("missing error row for absent uid 999: %s", stdout)
	}
	if !strings.Contains(stdout, `"failed":1`) {
		t.Errorf("summary should count the absent uid as failed: %s", stdout)
	}
}
