package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaptest"
)

// appendViaCfg dials the test server with a fresh client, appends rfc822 to
// mailbox, and closes the connection. Intended for tests that need to simulate
// a new message arriving mid-watch.
func appendViaCfg(t *testing.T, srv *imaptest.Server, mailbox string, rfc822 []byte) {
	t.Helper()
	c, err := imapclient.Dial(imaptest.Config(srv))
	if err != nil {
		t.Fatalf("append dial: %v", err)
	}
	defer imapclient.CloseAndLogout(c)
	if err := c.Append(mailbox, nil, time.Now(), bytes.NewReader(rfc822)); err != nil {
		t.Fatalf("append: %v", err)
	}
}

// rootWithCmd returns a minimal root cobra command with the given command
// registered — used by tests so that we can exercise these commands without
// mutating cmd/higgs/main.go (which is out of scope for this phase).
func rootWithCmd(sub *cobra.Command) *cobra.Command {
	root := &cobra.Command{Use: "higgs", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(sub)
	return root
}

func TestCmdWatchTimeout(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)
	root := rootWithCmd(newWatchCmd())
	root.SetArgs([]string{
		"watch", "INBOX",
		"--poll-interval", "50ms",
		"--timeout", "250ms",
	})
	start := time.Now()
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("watch: %v (stdout=%s)", err, stdout)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("watch took too long: %v", elapsed)
	}
	if !strings.Contains(stdout, `"type":"summary"`) {
		t.Errorf("missing summary row: %s", stdout)
	}
	if !strings.Contains(stdout, `"events_emitted"`) {
		t.Errorf("missing events_emitted field: %s", stdout)
	}
}

func TestCmdWatchMaxEvents(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("x", "a@x.com")},
	}))
	applyTestConfig(t, srv)

	root := rootWithCmd(newWatchCmd())
	root.SetArgs([]string{
		"watch", "INBOX",
		"--poll-interval", "30ms",
		"--max-events", "1",
		"--timeout", "3s",
	})

	done := make(chan error, 1)
	var stdout string
	go func() {
		var err error
		stdout, err = captureStdout(t, func() error { return root.Execute() })
		done <- err
	}()

	time.Sleep(120 * time.Millisecond)
	appendViaCfg(t, srv, "INBOX", testMsg("new", "b@x.com"))

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not exit within 5s")
	}
	if !strings.Contains(stdout, `"type":"event"`) {
		t.Errorf("expected event row in stdout: %s", stdout)
	}
	if !strings.Contains(stdout, `"kind":"new"`) {
		t.Errorf("expected kind=new: %s", stdout)
	}
	if !strings.Contains(stdout, `"events_emitted":1`) {
		t.Errorf("expected events_emitted=1: %s", stdout)
	}
}

func TestCmdWatchNegativeInterval(t *testing.T) {
	// Use the command function directly to cover the negative-interval branch.
	err := cmdWatch("INBOX", &watchFlags{pollInterval: -1})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("expected validation kind, got %v", cerr.From(err).Kind)
	}
	if err := cmdWatch("INBOX", &watchFlags{maxEvents: -1}); err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("expected validation error for max-events<0, got %v", err)
	}
	if err := cmdWatch("INBOX", &watchFlags{timeout: -1}); err == nil || cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("expected validation error for timeout<0, got %v", err)
	}
}

func TestCmdWatchBadMailbox(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", nil))
	applyTestConfig(t, srv)
	root := rootWithCmd(newWatchCmd())
	root.SetArgs([]string{
		"watch", "NoSuchMailbox",
		"--poll-interval", "50ms",
		"--timeout", "100ms",
	})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error for unknown mailbox")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("got kind %v, want validation", cerr.From(err).Kind)
	}
}
