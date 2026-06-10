package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akeemjenkins/protoncli/internal/cerr"
	"github.com/akeemjenkins/protoncli/internal/imapclient"
	"github.com/akeemjenkins/protoncli/internal/imaptest"
)

// fakeSMTP is a minimal SMTP server that speaks the full net/smtp client
// handshake (EHLO/MAIL/RCPT/DATA/QUIT) over plaintext on loopback. It captures
// the DATA payload so tests can assert on the delivered message.
type fakeSMTP struct {
	mu   sync.Mutex
	data bytes.Buffer
	host string
	port int
	done chan struct{}
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	f := &fakeSMTP{host: addr.IP.String(), port: addr.Port, done: make(chan struct{})}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		defer close(f.done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 fake ESMTP ready\r\n")
		inData := false
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					fmt.Fprint(conn, "250 2.0.0 OK queued\r\n")
					continue
				}
				f.mu.Lock()
				f.data.WriteString(line)
				f.mu.Unlock()
				continue
			}
			switch verb := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(verb, "EHLO"), strings.HasPrefix(verb, "HELO"):
				fmt.Fprint(conn, "250-fake greets you\r\n250 HELP\r\n")
			case strings.HasPrefix(verb, "MAIL"):
				fmt.Fprint(conn, "250 2.1.0 OK\r\n")
			case strings.HasPrefix(verb, "RCPT"):
				fmt.Fprint(conn, "250 2.1.5 OK\r\n")
			case strings.HasPrefix(verb, "DATA"):
				fmt.Fprint(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
				inData = true
			case strings.HasPrefix(verb, "QUIT"):
				fmt.Fprint(conn, "221 2.0.0 Bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 2.0.0 OK\r\n")
			}
		}
	}()
	return f
}

func (f *fakeSMTP) apply(t *testing.T) {
	t.Helper()
	t.Setenv("PM_SMTP_HOST", f.host)
	t.Setenv("PM_SMTP_PORT", strconv.Itoa(f.port))
	t.Setenv("PM_SMTP_USERNAME", "")
	t.Setenv("PM_SMTP_PASSWORD", "")
}

func (f *fakeSMTP) waitDone(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(3 * time.Second):
		t.Fatal("fake SMTP server did not finish handshake")
	}
}

func (f *fakeSMTP) body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data.String()
}

func TestSendCmdFlagsAndAnnotations(t *testing.T) {
	cmd := newSendCmd()
	if cmd.Annotations["stdout_format"] != "json" {
		t.Errorf("stdout_format: %q", cmd.Annotations["stdout_format"])
	}
	if cmd.Annotations["exit_codes"] != "0,1,2,3,4,5,9" {
		t.Errorf("exit_codes: %q", cmd.Annotations["exit_codes"])
	}
	if !strings.Contains(strings.ToLower(cmd.Short), "send") {
		t.Errorf("Short must mention send: %q", cmd.Short)
	}
	for _, name := range []string{"to", "cc", "bcc", "subject", "body-file", "body-html-file", "from", "in-reply-to", "source-mailbox", "dry-run", "save-to-sent", "sent-mailbox"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag %q", name)
		}
	}
	if cmd.Flags().Lookup("drafts-mailbox") != nil {
		t.Errorf("send must not expose --drafts-mailbox")
	}
	if def := cmd.Flags().Lookup("save-to-sent"); def == nil || def.DefValue != "false" {
		t.Errorf("save-to-sent must default to false: %+v", def)
	}
}

func TestSendValidationNoRecipient(t *testing.T) {
	body := writeBody(t, "hi")
	cmd := newSendCmd()
	cmd.SetArgs([]string{"--from", "me@x.com", "--subject", "Hi", "--body-file", body})
	_, err := captureStdout(t, func() error { return cmd.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind: %v", cerr.From(err).Kind)
	}
}

func TestSendValidationNoBody(t *testing.T) {
	cmd := newSendCmd()
	cmd.SetArgs([]string{"--from", "me@x.com", "--to", "u@x.com"})
	_, err := captureStdout(t, func() error { return cmd.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind: %v", cerr.From(err).Kind)
	}
}

func TestSendMissingSMTPConfig(t *testing.T) {
	t.Setenv("PM_SMTP_HOST", "")
	t.Setenv("PM_SMTP_PORT", "")
	body := writeBody(t, "hi")
	cmd := newSendCmd()
	cmd.SetArgs([]string{"--from", "me@x.com", "--to", "u@x.com", "--body-file", body})
	_, err := captureStdout(t, func() error { return cmd.Execute() })
	if err == nil {
		t.Fatal("expected config error")
	}
	if cerr.From(err).Kind != cerr.KindConfig {
		t.Errorf("kind: %v", cerr.From(err).Kind)
	}
}

func TestSendDryRun(t *testing.T) {
	body := writeBody(t, "hello world")
	cmd := newSendCmd()
	cmd.SetArgs([]string{
		"--to", "user@example.com",
		"--subject", "Hi",
		"--body-file", body,
		"--from", "me@example.com",
		"--dry-run",
	})
	stdout, err := captureStdout(t, func() error { return cmd.Execute() })
	if err != nil {
		t.Fatalf("send dry-run: %v (%s)", err, stdout)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if out["type"] != "send_preview" {
		t.Errorf("wrong type: %v", out["type"])
	}
	raw, _ := out["rfc822"].(string)
	if !strings.Contains(raw, "Subject: Hi") || !strings.Contains(raw, "hello world") {
		t.Errorf("preview missing content: %s", raw)
	}
}

func TestSendToFakeSMTPServer(t *testing.T) {
	srv := startFakeSMTP(t)
	srv.apply(t)
	body := writeBody(t, "the body text")
	cmd := newSendCmd()
	cmd.SetArgs([]string{
		"--from", "me@example.com",
		"--to", "user@example.com",
		"--subject", "Greetings",
		"--body-file", body,
	})
	stdout, err := captureStdout(t, func() error { return cmd.Execute() })
	if err != nil {
		t.Fatalf("send: %v (%s)", err, stdout)
	}
	srv.waitDone(t)

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout)
	}
	if out["sent"] != true {
		t.Errorf("sent flag missing: %v", out)
	}
	if msgID, ok := out["message_id"].(string); !ok || msgID == "" {
		t.Errorf("message_id missing: %v", out)
	}
	delivered := srv.body()
	if !strings.Contains(delivered, "Subject: Greetings") {
		t.Errorf("subject not delivered: %s", delivered)
	}
	if !strings.Contains(delivered, "the body text") {
		t.Errorf("body not delivered: %s", delivered)
	}
}

func TestSendSmtpFailureIsExitCode1(t *testing.T) {
	// A listener we immediately close yields a refused port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	t.Setenv("PM_SMTP_HOST", "127.0.0.1")
	t.Setenv("PM_SMTP_PORT", strconv.Itoa(port))
	t.Setenv("PM_SMTP_USERNAME", "")
	t.Setenv("PM_SMTP_PASSWORD", "")

	body := writeBody(t, "x")
	cmd := newSendCmd()
	cmd.SetArgs([]string{"--from", "me@x.com", "--to", "u@x.com", "--body-file", body})
	_, err = captureStdout(t, func() error { return cmd.Execute() })
	if err == nil {
		t.Fatal("expected SMTP delivery error")
	}
	ce := cerr.From(err)
	if ce.Kind != cerr.KindAPI {
		t.Errorf("kind: got %v, want api", ce.Kind)
	}
	if ce.ExitCode() != cerr.ExitCodeAPI {
		t.Errorf("exit code: got %d, want %d", ce.ExitCode(), cerr.ExitCodeAPI)
	}
	if ce.Reason != "smtpError" {
		t.Errorf("reason: got %q, want smtpError", ce.Reason)
	}
}

func TestSendInReplyTo(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("Conv", []imaptest.Message{
		{RFC822: testMsg("Original", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	body := writeBody(t, "my reply")
	cmd := newSendCmd()
	cmd.SetArgs([]string{
		"--to", "a@x.com",
		"--body-file", body,
		"--from", "me@x.com",
		"--in-reply-to", "1",
		"--source-mailbox", "Conv",
		"--dry-run",
	})
	stdout, err := captureStdout(t, func() error { return cmd.Execute() })
	if err != nil {
		t.Fatalf("reply: %v (%s)", err, stdout)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, _ := out["rfc822"].(string)
	if !strings.Contains(raw, "Subject: Re: Original") {
		t.Errorf("subject missing Re prefix: %s", raw)
	}
	if !strings.Contains(raw, "In-Reply-To:") {
		t.Errorf("In-Reply-To missing: %s", raw)
	}
}

func TestSendSavesToSent(t *testing.T) {
	srv := imaptest.Start(t,
		imaptest.WithMailbox("INBOX", nil),
		imaptest.WithMailbox("Sent", nil),
	)
	applyTestConfig(t, srv)
	smtpSrv := startFakeSMTP(t)
	smtpSrv.apply(t)

	body := writeBody(t, "saved body")
	cmd := newSendCmd()
	cmd.SetArgs([]string{
		"--from", "me@example.com",
		"--to", "user@example.com",
		"--subject", "Keep me",
		"--body-file", body,
		"--save-to-sent",
	})
	stdout, err := captureStdout(t, func() error { return cmd.Execute() })
	if err != nil {
		t.Fatalf("send: %v (%s)", err, stdout)
	}
	smtpSrv.waitDone(t)

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parse: %v\n%s", err, stdout)
	}
	if out["saved_to"] != "Sent" {
		t.Errorf("saved_to: %v", out["saved_to"])
	}

	// Verify the copy actually landed in Sent.
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("dial verify: %v", err)
	}
	defer imapclient.CloseAndLogout(c)
	status, err := c.Select("Sent", true)
	if err != nil {
		t.Fatalf("select Sent: %v", err)
	}
	if status.Messages < 1 {
		t.Errorf("Sent empty: %d", status.Messages)
	}
}
