package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/termio"
)

// syncWriter serializes Write calls so multiple goroutines may share a
// single writer without triggering the race detector.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// captureStdout swaps termio.Default() writer for a buffer, runs fn, and
// returns captured stdout. Stderr is discarded. The wrapping syncWriter
// protects the buffer from concurrent termio writers (e.g. classify workers).
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	var out bytes.Buffer
	prev := termio.Default()
	termio.SetDefault(&termio.Writer{
		Out: &syncWriter{w: &out},
		Err: &syncWriter{w: &bytes.Buffer{}},
	})
	t.Cleanup(func() { termio.SetDefault(prev) })
	err := fn()
	return out.String(), err
}

func TestSchemaRootOutput(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"schema"})

	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("schema exec: %v", err)
	}

	var sr schemaRoot
	if err := json.Unmarshal([]byte(stdout), &sr); err != nil {
		t.Fatalf("unmarshal schema: %v\nstdout: %s", err, stdout)
	}

	if len(sr.Commands) < 7 {
		t.Errorf("expected >=7 commands, got %d: %+v", len(sr.Commands), sr.Commands)
	}

	// Must include every major subcommand.
	wantCmds := []string{
		"scan-folders", "fetch-and-parse", "classify", "cleanup-labels",
		"state", "backfill", "apply-labels", "schema",
	}
	have := make(map[string]bool)
	for _, c := range sr.Commands {
		have[c.Name] = true
	}
	for _, w := range wantCmds {
		if !have[w] {
			t.Errorf("missing command %q in schema output", w)
		}
	}

	if len(sr.ExitCodeDocs) != len(cerr.ExitCodeDocs) {
		t.Errorf("exit_code_docs length = %d, want %d", len(sr.ExitCodeDocs), len(cerr.ExitCodeDocs))
	}
}

func TestSchemaSubcommandFiltered(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"schema", "classify"})

	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("schema classify exec: %v", err)
	}

	var sc schemaCommand
	if err := json.Unmarshal([]byte(stdout), &sc); err != nil {
		t.Fatalf("unmarshal schema classify: %v", err)
	}
	if sc.Name != "classify" {
		t.Errorf("name = %q, want classify", sc.Name)
	}
	if len(sc.Flags) == 0 {
		t.Errorf("classify has no flags")
	}

	// classify has one optional argument "mailbox" with default Folders/Accounts.
	if len(sc.Args) != 1 || sc.Args[0].Name != "mailbox" {
		t.Errorf("classify args = %+v", sc.Args)
	} else if sc.Args[0].Required {
		t.Errorf("mailbox arg should be optional")
	} else if sc.Args[0].Default != "Folders/Accounts" {
		t.Errorf("mailbox default = %q, want Folders/Accounts", sc.Args[0].Default)
	}
}

func TestSchemaUnknownSubcommand(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"schema", "does-not-exist"})

	_, err := captureStdout(t, func() error { return root.Execute() })
	if err == nil {
		t.Fatalf("expected validation error")
	}
	e := cerr.From(err)
	if e.Kind != cerr.KindValidation {
		t.Errorf("kind = %v, want validation", e.Kind)
	}
	if e.ExitCode() != cerr.ExitCodeValidation {
		t.Errorf("exit code = %d, want %d", e.ExitCode(), cerr.ExitCodeValidation)
	}
}

func TestDeriveArgsForms(t *testing.T) {
	// Arg default empty for non-special commands.
	if d := commandArgDefault("other", "x"); d != "" {
		t.Errorf("default empty expected, got %q", d)
	}
	if d := commandArgDefault("fetch-and-parse", "mailbox"); d != "INBOX" {
		t.Errorf("fetch-and-parse mailbox default = %q, want INBOX", d)
	}
	if d := commandArgDefault("apply-labels", "mailbox"); d != "Folders/Accounts" {
		t.Errorf("apply-labels mailbox default = %q, want Folders/Accounts", d)
	}
}

func TestParseExitCodes(t *testing.T) {
	// default when empty
	got := parseExitCodes("")
	if len(got) != 2 || got[0] != cerr.ExitCodeOK || got[1] != cerr.ExitCodeInternal {
		t.Errorf("empty parse = %v", got)
	}
	got = parseExitCodes("0,2,5,not-a-num,7")
	want := []int{0, 2, 5, 7}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestFindCommandByName(t *testing.T) {
	root := newRootCmd()
	if c := findCommandByName(root, "classify"); c == nil || c.Name() != "classify" {
		t.Errorf("classify not found")
	}
	if c := findCommandByName(root, "nope"); c != nil {
		t.Errorf("found unexpected command: %v", c)
	}
}

func TestDeriveArgsRequired(t *testing.T) {
	// Use the state subcommand's clear form: Use: "clear <mailbox>".
	root := newRootCmd()
	stateCmd := findCommandByName(root, "state")
	if stateCmd == nil {
		t.Fatal("state cmd missing")
	}
	var clear *schemaCommand
	for _, sub := range stateCmd.Commands() {
		if sub.Name() == "clear" {
			d := describeCommand(sub)
			clear = &d
			break
		}
	}
	if clear == nil {
		t.Fatal("state clear missing")
	}
	if len(clear.Args) != 1 || clear.Args[0].Name != "mailbox" || !clear.Args[0].Required {
		t.Errorf("state clear args = %+v", clear.Args)
	}
}

func TestSchemaExecSmoke(t *testing.T) {
	// Smoke: verify stdout contains balanced JSON with a "commands" key.
	root := newRootCmd()
	root.SetArgs([]string{"schema"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"commands"`) {
		t.Error("missing commands key")
	}
	if !strings.Contains(stdout, `"exit_code_docs"`) {
		t.Error("missing exit_code_docs key")
	}
}
