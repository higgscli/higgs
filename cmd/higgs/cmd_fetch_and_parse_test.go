package main

import (
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
)

func TestFetchAndParseCmdShape(t *testing.T) {
	cmd := newFetchAndParseCmd()
	if cmd.Annotations["stdout_format"] != "ndjson" {
		t.Errorf("stdout_format = %q", cmd.Annotations["stdout_format"])
	}
	if cmd.Use != "fetch-and-parse [mailbox]" {
		t.Errorf("use = %q", cmd.Use)
	}
}

func TestFetchAndParseMissingConfig(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")

	err := cmdFetchAndParse("INBOX")
	if err == nil {
		t.Fatal("expected config error")
	}
	e := cerr.From(err)
	if e.Kind != cerr.KindConfig {
		t.Errorf("kind = %v, want config", e.Kind)
	}
}
