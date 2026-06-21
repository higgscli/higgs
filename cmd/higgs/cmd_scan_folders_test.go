package main

import (
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
)

func TestScanFoldersCmdShape(t *testing.T) {
	cmd := newScanFoldersCmd()
	if cmd.Annotations["stdout_format"] != "json" {
		t.Errorf("stdout_format = %q", cmd.Annotations["stdout_format"])
	}
	if cmd.Use != "scan-folders" {
		t.Errorf("use = %q", cmd.Use)
	}
}

func TestScanFoldersMissingConfig(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")

	err := cmdScanFolders()
	if err == nil {
		t.Fatal("expected config error")
	}
	e := cerr.From(err)
	if e.Kind != cerr.KindConfig {
		t.Errorf("kind = %v, want config", e.Kind)
	}
}
