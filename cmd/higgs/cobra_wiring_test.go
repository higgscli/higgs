package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
)

// These tests run each subcommand through the cobra root so the RunE
// wrappers are exercised. Each command is invoked with an environment that
// causes an early exit (missing PM_IMAP_USERNAME yields a config error
// before any network call). Subcommands that only talk to the state DB
// are pointed at a temp directory.

func runRootExpectKind(t *testing.T, args []string, wantKind cerr.Kind) {
	t.Helper()
	root := newRootCmd()
	root.SetArgs(args)
	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatalf("args %v: expected error kind=%v, got nil", args, wantKind)
	}
	e := cerr.From(err)
	if e.Kind != wantKind {
		t.Errorf("args %v: kind = %v, want %v (err=%v)", args, e.Kind, wantKind, err)
	}
}

func TestRootScanFoldersConfigError(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")
	runRootExpectKind(t, []string{"scan-folders"}, cerr.KindConfig)
}

func TestRootFetchAndParseConfigError(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")
	runRootExpectKind(t, []string{"fetch-and-parse", "INBOX"}, cerr.KindConfig)
}

func TestRootClassifyConfigError(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")
	runRootExpectKind(t, []string{"classify", "--no-state"}, cerr.KindConfig)
}

func TestRootCleanupLabelsConfigError(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")
	runRootExpectKind(t, []string{"cleanup-labels"}, cerr.KindConfig)
}

func TestRootCleanupLabelsDryRunConfigError(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")
	runRootExpectKind(t, []string{"cleanup-labels", "--dry-run"}, cerr.KindConfig)
}

func TestRootApplyLabelsConfigError(t *testing.T) {
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")
	runRootExpectKind(t, []string{"apply-labels"}, cerr.KindConfig)
}

func TestRootApplyLabelsDryRunConfigError(t *testing.T) {
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")
	runRootExpectKind(t, []string{"apply-labels", "--dry-run"}, cerr.KindConfig)
}

func TestRootBackfillMissingFile(t *testing.T) {
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	runRootExpectKind(t, []string{"backfill", filepath.Join(t.TempDir(), "no.log")}, cerr.KindInternal)
}

func TestRootStateStats(t *testing.T) {
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	root := newRootCmd()
	root.SetArgs([]string{"state", "stats"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"db"`) {
		t.Errorf("missing db key in stats: %s", stdout)
	}
}

func TestRootStateStatsWithMailbox(t *testing.T) {
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	root := newRootCmd()
	root.SetArgs([]string{"state", "stats", "INBOX"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"mailbox"`) {
		t.Errorf("missing mailbox key: %s", stdout)
	}
}

func TestRootStateClear(t *testing.T) {
	t.Setenv("PM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
	root := newRootCmd()
	root.SetArgs([]string{"state", "clear", "INBOX"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"cleared": true`) {
		t.Errorf("missing cleared: %s", stdout)
	}
}
