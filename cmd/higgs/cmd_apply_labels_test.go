package main

import (
	"path/filepath"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
)

func TestApplyLabelsCmdFlags(t *testing.T) {
	cmd := newApplyLabelsCmd()
	expected := map[string]string{
		"limit":   "0",
		"dry-run": "false",
	}
	for name, def := range expected {
		f := cmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("missing flag %q", name)
			continue
		}
		if f.DefValue != def {
			t.Errorf("flag %q default = %q, want %q", name, f.DefValue, def)
		}
	}
	if cmd.Annotations["stdout_format"] != "ndjson" {
		t.Errorf("stdout_format = %q", cmd.Annotations["stdout_format"])
	}
}

func TestApplyLabelsMissingConfig(t *testing.T) {
	// Point state DB at a tmp path so we don't touch home.
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	// Skip keystore so a developer's OS keychain creds don't slip through.
	t.Setenv("PM_DISABLE_KEYSTORE", "1")

	err := cmdApplyLabels("Folders/Accounts", 0, false)
	if err == nil {
		t.Fatal("expected config error")
	}
	e := cerr.From(err)
	if e.Kind != cerr.KindConfig {
		t.Errorf("kind = %v, want config", e.Kind)
	}
}
