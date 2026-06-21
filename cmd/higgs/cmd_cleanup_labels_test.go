package main

import (
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/labels"
)

func TestCleanupLabelsCmdFlags(t *testing.T) {
	cmd := newCleanupLabelsCmd()
	f := cmd.Flags().Lookup("dry-run")
	if f == nil {
		t.Fatal("missing dry-run flag")
	}
	if f.DefValue != "false" {
		t.Errorf("dry-run default = %q, want false", f.DefValue)
	}
	if cmd.Annotations["stdout_format"] != "ndjson" {
		t.Errorf("stdout_format = %q", cmd.Annotations["stdout_format"])
	}
}

func TestCleanupLabelsMissingConfig(t *testing.T) {
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	t.Setenv("PM_DISABLE_KEYSTORE", "1")

	err := cmdCleanupLabels(false)
	if err == nil {
		t.Fatal("expected config error")
	}
	e := cerr.From(err)
	if e.Kind != cerr.KindConfig {
		t.Errorf("kind = %v, want config", e.Kind)
	}
}

// TestCleanupLabelsTaxonomyWired verifies the cleanup command uses the
// shared taxonomy. This is a proxy check: we confirm the canonical set
// exposed by labels.Default is the one the command would iterate.
func TestCleanupLabelsTaxonomyWired(t *testing.T) {
	got := labels.Default.Canonical()
	if len(got) == 0 {
		t.Fatal("labels.Default has no canonical labels")
	}
	// Historically the cleanup command used these 11 canonical names; confirm
	// they remain present so external behavior is preserved.
	want := []string{
		"Orders", "Finance", "Newsletters", "Promotions", "Jobs",
		"Social", "Services", "Health", "Travel", "Security", "Signups",
	}
	have := make(map[string]bool, len(got))
	for _, n := range got {
		have[n] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("canonical label %q missing from taxonomy", w)
		}
	}

	// Spot-check a few aliases that used to live in the local map.
	cases := map[string]string{
		"shipping":  "Orders",
		"invoice":   "Finance",
		"promo":     "Promotions",
		"linkedin":  "Jobs",
		"nextdoor":  "Social",
		"outage":    "Services",
		"wellness":  "Health",
		"flight":    "Travel",
		"2fa":       "Security",
		"domain":    "Signups",
	}
	for alias, wantName := range cases {
		got, ok := labels.Default.CanonicalFor(alias)
		if !ok {
			t.Errorf("alias %q not recognized", alias)
			continue
		}
		if got != wantName {
			t.Errorf("alias %q: got %q, want %q", alias, got, wantName)
		}
	}
}
