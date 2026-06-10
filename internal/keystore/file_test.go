package keystore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withFileBackend sets up an isolated file backend rooted in t.TempDir() with
// a short passphrase suitable for tests.
func withFileBackend(t *testing.T, passphrase string) *fileBackend {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PM_KEYSTORE_PATH", filepath.Join(dir, "creds.enc"))
	t.Setenv("PM_KEYSTORE_PASSPHRASE", passphrase)
	return newFileBackend()
}

func TestFileBackend_RoundTrip(t *testing.T) {
	fb := withFileBackend(t, "hunter2")
	if !fb.Available() {
		t.Fatal("file backend should be available with passphrase set")
	}
	want := Credentials{Username: "alice@proton.me", Password: "bridge-pw"}
	if err := fb.Set(want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := fb.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestFileBackend_DeleteThenGet(t *testing.T) {
	fb := withFileBackend(t, "pass")
	if err := fb.Set(Credentials{Username: "u", Password: "p"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := fb.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := fb.Get(); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get err = %v, want ErrNotFound", err)
	}
	// Delete is idempotent.
	if err := fb.Delete(); err != nil {
		t.Errorf("Delete idempotent: %v", err)
	}
}

func TestFileBackend_TamperDetection(t *testing.T) {
	fb := withFileBackend(t, "pass")
	if err := fb.Set(Credentials{Username: "u", Password: "p"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	path := fb.Path()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Flip a byte in the ciphertext region (past version+salt+nonce).
	idx := 1 + saltLen + nonceLen
	if len(data) <= idx {
		t.Fatalf("encrypted file too small: %d bytes", len(data))
	}
	data[idx] ^= 0x55
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := fb.Get(); err == nil {
		t.Error("expected tamper to be detected")
	} else if !strings.Contains(err.Error(), "decrypt failed") {
		t.Errorf("unexpected err: %v", err)
	}
}

func TestFileBackend_WrongPassphrase(t *testing.T) {
	fb := withFileBackend(t, "right")
	if err := fb.Set(Credentials{Username: "u", Password: "p"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	t.Setenv("PM_KEYSTORE_PASSPHRASE", "wrong")
	if _, err := fb.Get(); err == nil {
		t.Fatal("expected decrypt error with wrong passphrase")
	}
}

func TestFileBackend_VersionByteMismatch(t *testing.T) {
	fb := withFileBackend(t, "pass")
	if err := fb.Set(Credentials{Username: "u", Password: "p"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	path := fb.Path()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	data[0] = 0xFE
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := fb.Get(); err == nil {
		t.Error("expected version mismatch error")
	} else if !strings.Contains(err.Error(), "unsupported file version") {
		t.Errorf("unexpected err: %v", err)
	}
}

func TestFileBackend_NoPassphrase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PM_KEYSTORE_PATH", filepath.Join(dir, "creds.enc"))
	t.Setenv("PM_KEYSTORE_PASSPHRASE", "")
	os.Unsetenv("PM_KEYSTORE_PASSPHRASE")
	fb := newFileBackend()
	if fb.Available() {
		t.Error("Available should be false when passphrase is unset and no file exists")
	}
	if err := fb.Set(Credentials{Username: "u", Password: "p"}); err == nil {
		t.Error("expected Set to require passphrase")
	}
	if _, err := fb.Get(); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get err = %v, want ErrNotFound (file does not exist)", err)
	}
}

func TestFileBackend_AvailableWhenFileExistsWithoutPassphrase(t *testing.T) {
	fb := withFileBackend(t, "pass")
	if err := fb.Set(Credentials{Username: "u", Password: "p"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Drop the passphrase — the file backend should still report Available so
	// logout can delete the stored file.
	t.Setenv("PM_KEYSTORE_PASSPHRASE", "")
	os.Unsetenv("PM_KEYSTORE_PASSPHRASE")
	if !fb.Available() {
		t.Error("Available should be true when file exists even without passphrase")
	}
	if err := fb.Delete(); err != nil {
		t.Errorf("Delete without passphrase: %v", err)
	}
}

func TestFileBackend_TruncatedFile(t *testing.T) {
	fb := withFileBackend(t, "pass")
	if err := fb.Set(Credentials{Username: "u", Password: "p"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	path := fb.Path()
	if err := os.WriteFile(path, []byte{0x01, 0x02}, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := fb.Get(); err == nil {
		t.Error("expected truncated-file error")
	}
}

func TestFileBackend_GetReadErrorNotEnoent(t *testing.T) {
	// Make the path point at a directory so os.ReadFile returns a non-ENOENT error.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PM_KEYSTORE_PATH", dir) // path IS the directory
	t.Setenv("PM_KEYSTORE_PASSPHRASE", "pass")
	fb := newFileBackend()
	if _, err := fb.Get(); err == nil {
		t.Error("expected error reading a directory as file")
	}
}

func TestFileBackend_DeleteGenericErrorIsWrapped(t *testing.T) {
	// On POSIX systems, removing a non-empty directory returns ENOTEMPTY, not
	// ENOENT. Point PM_KEYSTORE_PATH at a non-empty directory to force that.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Put a file inside so `os.Remove(nested)` fails.
	if err := os.WriteFile(filepath.Join(nested, "f"), []byte{0}, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Setenv("PM_KEYSTORE_PATH", nested)
	fb := newFileBackend()
	if err := fb.Delete(); err == nil {
		t.Error("expected Delete to fail on non-empty directory")
	}
}

func TestFileBackend_Names(t *testing.T) {
	fb := newFileBackend()
	if fb.Name() != "encrypted file" {
		t.Errorf("name = %q", fb.Name())
	}
}

// TestFileBackend_NoHomeNoOverride simulates the no-home case by pointing
// PM_KEYSTORE_PATH at empty and unsetting HOME. Path() should return "".
func TestFileBackend_NoHomeNoOverride(t *testing.T) {
	t.Setenv("PM_KEYSTORE_PATH", "")
	os.Unsetenv("PM_KEYSTORE_PATH")
	// Force UserHomeDir to fail by unsetting HOME on unix and USERPROFILE on
	// windows. A best-effort approach — if the runtime still resolves a home,
	// the test becomes a no-op rather than a false negative.
	t.Setenv("HOME", "")
	os.Unsetenv("HOME")
	t.Setenv("USERPROFILE", "")
	os.Unsetenv("USERPROFILE")
	fb := newFileBackend()
	if p := fb.Path(); p != "" {
		// Not a failure — just skip the downstream assertions.
		t.Skipf("runtime still resolved a home: %q", p)
	}
	if fb.Available() {
		t.Error("Available should be false without a resolvable path")
	}
	if _, err := fb.Get(); err == nil {
		t.Error("Get should error without a path")
	}
	if err := fb.Set(Credentials{}); err == nil {
		t.Error("Set should error without a path")
	}
	if err := fb.Delete(); err != nil {
		t.Errorf("Delete with empty path should be a no-op, got %v", err)
	}
}

func TestFileBackend_SetMkdirFailure(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file at the spot where Set wants to create a directory.
	block := filepath.Join(dir, "block")
	if err := os.WriteFile(block, []byte("no"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Point PM_KEYSTORE_PATH at <block>/creds.enc so mkdir should fail.
	t.Setenv("HOME", dir)
	t.Setenv("PM_KEYSTORE_PATH", filepath.Join(block, "creds.enc"))
	t.Setenv("PM_KEYSTORE_PASSPHRASE", "pass")
	fb := newFileBackend()
	if err := fb.Set(Credentials{Username: "u", Password: "p"}); err == nil {
		t.Error("expected Set to fail when parent dir is a file")
	}
}

func TestFileBackend_SetCreatesDirectoryWith0700(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// Point into a nested path that does not yet exist.
	nested := filepath.Join(dir, "deeply", "nested", "creds.enc")
	t.Setenv("PM_KEYSTORE_PATH", nested)
	t.Setenv("PM_KEYSTORE_PASSPHRASE", "pass")
	fb := newFileBackend()
	if err := fb.Set(Credentials{Username: "u", Password: "p"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	info, err := os.Stat(filepath.Dir(nested))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	finfo, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	// Windows does not enforce POSIX file modes, so the 0700/0600 bits are
	// not observable there.
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o700 {
			t.Errorf("dir perm = %o, want 0700", info.Mode().Perm())
		}
		if finfo.Mode().Perm() != 0o600 {
			t.Errorf("file perm = %o, want 0600", finfo.Mode().Perm())
		}
	}
}

func TestFileBackend_DeleteMissingIsNoOp(t *testing.T) {
	fb := withFileBackend(t, "pass")
	// Ensure nothing exists yet.
	if err := fb.Delete(); err != nil {
		t.Errorf("delete of missing file: %v", err)
	}
}

func TestFileBackend_PathOverrideAndHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// os.UserHomeDir resolves %USERPROFILE% on Windows, not $HOME.
	t.Setenv("USERPROFILE", dir)
	t.Setenv("PM_KEYSTORE_PATH", "")
	os.Unsetenv("PM_KEYSTORE_PATH")
	fb := newFileBackend()
	want := filepath.Join(dir, ".protoncli", "credentials.enc")
	if got := fb.Path(); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}

	t.Setenv("PM_KEYSTORE_PATH", "/custom/path.enc")
	if got := fb.Path(); got != "/custom/path.enc" {
		t.Errorf("override path = %q", got)
	}
}
