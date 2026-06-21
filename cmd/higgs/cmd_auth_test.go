package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/keystore"
)

// authTestSetup installs a file-backed keystore in t.TempDir() and neutralizes
// the OS keyring via MockInit(). It also clears any stray IMAP env vars so
// status output is deterministic.
func authTestSetup(t *testing.T) (backendName string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PM_KEYSTORE_PATH", filepath.Join(dir, "creds.enc"))
	t.Setenv("PM_KEYSTORE_PASSPHRASE", "test-pass")
	t.Setenv("PM_IMAP_USERNAME", "")
	t.Setenv("PM_IMAP_PASSWORD", "")
	os.Unsetenv("PM_IMAP_USERNAME")
	os.Unsetenv("PM_IMAP_PASSWORD")
	keyring.MockInit()
	return "file"
}

func runAuthCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error {
		root := newRootCmd()
		root.SetArgs(args)
		return root.Execute()
	})
}

func TestAuthLogin_PasswordStdinStoresCreds(t *testing.T) {
	authTestSetup(t)
	// Inject stdin.
	authStdin = strings.NewReader("pw\n")
	authIsTerminal = func(int) bool { return true }
	t.Cleanup(func() {
		authStdin = os.Stdin
		authIsTerminal = func(fd int) bool { return false }
	})

	stdout, err := runAuthCmd(t, "auth", "login", "--username", "foo@example.com", "--password-stdin", "--backend", "file")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("unmarshal login: %v: %s", err, stdout)
	}
	if out["stored"] != true {
		t.Errorf("stored = %v", out["stored"])
	}
	if out["backend"] != "encrypted file" {
		t.Errorf("backend = %v", out["backend"])
	}
	if out["username"] != "foo@example.com" {
		t.Errorf("username = %v", out["username"])
	}

	// auth status should now show has_credentials=true for the file backend.
	stdout, err = runAuthCmd(t, "auth", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var st struct {
		Backends []struct {
			Name           string `json:"name"`
			Available      bool   `json:"available"`
			HasCredentials bool   `json:"has_credentials"`
			Username       string `json:"username"`
		} `json:"backends"`
		Active      string `json:"active"`
		EnvOverride bool   `json:"env_override"`
	}
	if err := json.Unmarshal([]byte(stdout), &st); err != nil {
		t.Fatalf("unmarshal status: %v: %s", err, stdout)
	}
	var file struct {
		ok bool
	}
	for _, b := range st.Backends {
		if b.Name == "encrypted file" {
			file.ok = b.HasCredentials && b.Username == "foo@example.com"
		}
	}
	if !file.ok {
		t.Errorf("encrypted-file backend status wrong: %+v", st.Backends)
	}
	if st.EnvOverride {
		t.Error("env_override should be false")
	}
}

func TestAuthLogin_NoTTYNoPasswordStdinFails(t *testing.T) {
	authTestSetup(t)
	authStdin = strings.NewReader("")
	authIsTerminal = func(int) bool { return false }
	t.Cleanup(func() {
		authStdin = os.Stdin
		authIsTerminal = func(fd int) bool { return false }
	})

	_, err := runAuthCmd(t, "auth", "login", "--username", "foo", "--backend", "file")
	if err == nil {
		t.Fatal("expected error when stdin is not a TTY and --password-stdin not set")
	}
	e := cerr.From(err)
	if e.ExitCode() != cerr.ExitCodeValidation {
		t.Errorf("exit code = %d, want %d (validation)", e.ExitCode(), cerr.ExitCodeValidation)
	}
}

func TestAuthLogin_EmptyPasswordFails(t *testing.T) {
	authTestSetup(t)
	authStdin = strings.NewReader("\n") // just newline
	authIsTerminal = func(int) bool { return true }
	t.Cleanup(func() {
		authStdin = os.Stdin
		authIsTerminal = func(fd int) bool { return false }
	})

	_, err := runAuthCmd(t, "auth", "login", "--username", "foo", "--password-stdin", "--backend", "file")
	if err == nil {
		t.Fatal("expected error on empty password")
	}
	if cerr.From(err).ExitCode() != cerr.ExitCodeValidation {
		t.Errorf("exit = %d, want validation", cerr.From(err).ExitCode())
	}
}

func TestAuthLogin_UnknownBackend(t *testing.T) {
	authTestSetup(t)
	_, err := runAuthCmd(t, "auth", "login", "--username", "foo", "--password-stdin", "--backend", "bogus")
	if err == nil {
		t.Fatal("expected validation error for unknown backend")
	}
	if cerr.From(err).ExitCode() != cerr.ExitCodeValidation {
		t.Errorf("exit = %d", cerr.From(err).ExitCode())
	}
}

func TestAuthLogout_IdempotentWhenEmpty(t *testing.T) {
	authTestSetup(t)
	stdout, err := runAuthCmd(t, "auth", "logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, stdout)
	}
	r, ok := out["removed"].([]any)
	if !ok {
		t.Fatalf("removed missing or wrong type: %v", out)
	}
	if len(r) != 0 {
		t.Errorf("removed = %v, want []", r)
	}
}

func TestAuthLoginThenLogout(t *testing.T) {
	authTestSetup(t)
	authStdin = strings.NewReader("pw\n")
	authIsTerminal = func(int) bool { return true }
	t.Cleanup(func() {
		authStdin = os.Stdin
		authIsTerminal = func(fd int) bool { return false }
	})

	if _, err := runAuthCmd(t, "auth", "login", "--username", "foo", "--password-stdin", "--backend", "file"); err != nil {
		t.Fatalf("login: %v", err)
	}

	// Reset stdin/TTY since it was consumed.
	authStdin = os.Stdin

	stdout, err := runAuthCmd(t, "auth", "logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	var out struct {
		Removed []string `json:"removed"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, stdout)
	}
	found := false
	for _, r := range out.Removed {
		if r == "encrypted file" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'encrypted file' in removed, got %+v", out.Removed)
	}
}

func TestAuthStatus_EnvOverride(t *testing.T) {
	authTestSetup(t)
	t.Setenv("PM_IMAP_USERNAME", "envuser")
	stdout, err := runAuthCmd(t, "auth", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var st struct {
		EnvOverride bool `json:"env_override"`
	}
	if err := json.Unmarshal([]byte(stdout), &st); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !st.EnvOverride {
		t.Errorf("env_override should be true")
	}
}

// TestAuthLogin_IntegrationWithConfigResolver writes creds via auth login and
// then verifies config.LoadFromEnv picks them up. Full classify would need IMAP;
// we stop at the config-resolution step per the spec.
func TestAuthLogin_IntegrationWithConfigResolver(t *testing.T) {
	authTestSetup(t)
	authStdin = strings.NewReader("integration-pw\n")
	authIsTerminal = func(int) bool { return true }
	t.Cleanup(func() {
		authStdin = os.Stdin
		authIsTerminal = func(fd int) bool { return false }
	})

	if _, err := runAuthCmd(t, "auth", "login", "--username", "integration@example.com", "--password-stdin", "--backend", "file"); err != nil {
		t.Fatalf("login: %v", err)
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.IMAP.Username != "integration@example.com" {
		t.Errorf("username = %q", cfg.IMAP.Username)
	}
	if cfg.IMAP.Password != "integration-pw" {
		t.Errorf("password mismatch: %q", cfg.IMAP.Password)
	}
}

func TestAuthCmd_SchemaVisible(t *testing.T) {
	// Verify the schema walker picks up the new `auth` command and its three
	// children. Uses the existing schemaRoot type from cmd_schema.go.
	root := newRootCmd()
	root.SetArgs([]string{"schema"})
	stdout, err := captureStdout(t, func() error { return root.Execute() })
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	var sr schemaRoot
	if err := json.Unmarshal([]byte(stdout), &sr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var auth *schemaCommand
	for i, c := range sr.Commands {
		if c.Name == "auth" {
			auth = &sr.Commands[i]
			break
		}
	}
	if auth == nil {
		t.Fatal("auth not found in schema commands")
	}
	names := map[string]bool{}
	for _, sub := range auth.Commands {
		names[sub.Name] = true
	}
	for _, want := range []string{"login", "logout", "status"} {
		if !names[want] {
			t.Errorf("auth missing subcommand %q: %+v", want, names)
		}
	}
}

// Smoke test: ensure backend listing via keystore package is non-empty and our
// selector accepts short names.
func TestSelectBackend_Names(t *testing.T) {
	if _, err := selectBackend("keyring"); err != nil {
		t.Errorf("keyring select: %v", err)
	}
	if _, err := selectBackend("file"); err != nil {
		t.Errorf("file select: %v", err)
	}
	if _, err := selectBackend(""); err != nil {
		t.Errorf("auto select: %v", err)
	}
	if _, err := selectBackend("invalid"); err == nil {
		t.Error("invalid selector should fail")
	}
}

// assertions about the keystore package wiring — mostly compile-time guards.
var (
	_ keystore.Backend = (*keyringBackendHandle)(nil)
	_ io.Reader        = strings.NewReader("")
)

type keyringBackendHandle struct{ keystore.Backend }
