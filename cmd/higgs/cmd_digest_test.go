package main

import (
	"strings"
	"testing"
	"time"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/imaptest"
)

func TestDigestCmdFlags(t *testing.T) {
	cmd := newDigestCmd()
	if cmd.Annotations["stdout_format"] != "json" {
		t.Errorf("stdout_format = %q", cmd.Annotations["stdout_format"])
	}
	for _, name := range []string{"since", "max-messages", "user-context", "model"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag %q", name)
		}
	}
}

func TestParseWindow(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"7d", 7 * 24 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"90m", 90 * time.Minute, false},
		{"", 0, true},
		{"0d", 0, true},
		{"abd", 0, true},
		{"garbage", 0, true},
	}
	for _, tc := range cases {
		got, err := parseWindow(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseWindow(%q) expected err", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseWindow(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseWindow(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestDigestValidation_BadSince(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	root := newDigestCmd()
	root.SetArgs([]string{"INBOX", "--since", "not-a-duration"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v", cerr.From(err).Kind)
	}
}

func TestDigestValidation_BadMaxMessages(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "u")
	t.Setenv("PM_IMAP_PASSWORD", "p")
	root := newDigestCmd()
	root.SetArgs([]string{"INBOX", "--max-messages", "0"})
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatal("expected validation error")
	}
	if cerr.From(err).Kind != cerr.KindValidation {
		t.Errorf("kind = %v", cerr.From(err).Kind)
	}
}

func TestDigestHappy_Empty(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", nil))
	applyTestConfig(t, srv)
	// No ollama should be hit (empty mailbox skips call).
	root := newDigestCmd()
	root.SetArgs([]string{"INBOX", "--since", "7d"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("digest: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"window": "7d"`) {
		t.Errorf("expected window in output: %s", stdout)
	}
}

func TestDigestHappy_WithMessages(t *testing.T) {
	srv := imaptest.Start(t, imaptest.WithMailbox("INBOX", []imaptest.Message{
		{RFC822: testMsg("A", "a@x.com"), Date: time.Now().Add(-1 * time.Hour)},
	}))
	applyTestConfig(t, srv)
	ollama := fakeOllamaJSON(t, `{"window":"7d","highlights":[{"uid":1,"title":"t","summary":"s","is_actionable":false}],"by_category":{},"counts":{}}`)
	t.Setenv("PM_OLLAMA_BASE_URL", ollama.URL)
	t.Setenv("PM_OLLAMA_MODEL", "m")

	root := newDigestCmd()
	root.SetArgs([]string{"INBOX", "--since", "7d"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("digest: %v (%s)", err, stdout)
	}
	if !strings.Contains(stdout, `"highlights"`) {
		t.Errorf("expected highlights in output: %s", stdout)
	}
}
