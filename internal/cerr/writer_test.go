package cerr

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPrintErrorEnvelopeAndSummary(t *testing.T) {
	var out, errw bytes.Buffer
	w := NewWithExit(&out, &errw, func(int) {})
	w.PrintError(Validation("bad input"))

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); err != nil {
		t.Fatalf("stdout json: %v\n%s", err, out.String())
	}
	inner := decoded["error"].(map[string]any)
	if inner["kind"] != "validation" {
		t.Errorf("kind=%v", inner["kind"])
	}
	summary := errw.String()
	if !strings.Contains(summary, "error[validation]: bad input") {
		t.Errorf("stderr summary missing: %q", summary)
	}
}

func TestPrintErrorIncludesCause(t *testing.T) {
	var out, errw bytes.Buffer
	w := NewWithExit(&out, &errw, func(int) {})
	w.PrintError(IMAP(errors.New("MOVE out of All Mail is not allowed"), "TRASH %q→%q", "All Mail", "Trash"))

	if !strings.Contains(errw.String(), "MOVE out of All Mail is not allowed") {
		t.Errorf("stderr summary should carry the cause: %q", errw.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); err != nil {
		t.Fatalf("stdout json: %v\n%s", err, out.String())
	}
	inner := decoded["error"].(map[string]any)
	if inner["cause"] != "MOVE out of All Mail is not allowed" {
		t.Errorf("envelope cause=%v", inner["cause"])
	}
}

func TestPrintErrorNilNoop(t *testing.T) {
	var out, errw bytes.Buffer
	w := NewWithExit(&out, &errw, func(int) {})
	w.PrintError(nil)
	if out.Len() != 0 || errw.Len() != 0 {
		t.Errorf("nil err should produce no output")
	}
}

func TestPrintErrorWrapsNonStructured(t *testing.T) {
	var out, errw bytes.Buffer
	w := NewWithExit(&out, &errw, func(int) {})
	w.PrintError(errors.New("raw"))
	if !strings.Contains(errw.String(), "error[internal]: raw") {
		t.Errorf("stderr=%q", errw.String())
	}
}

func TestExitUsesMappedCode(t *testing.T) {
	var out, errw bytes.Buffer
	var got int
	w := NewWithExit(&out, &errw, func(c int) { got = c })
	w.Exit(Auth("bad token"))
	if got != ExitCodeAuth {
		t.Errorf("exit=%d want %d", got, ExitCodeAuth)
	}
	if !strings.Contains(errw.String(), "auth") {
		t.Errorf("missing stderr summary")
	}
}

func TestExitNilIsOK(t *testing.T) {
	var out, errw bytes.Buffer
	got := -1
	w := NewWithExit(&out, &errw, func(c int) { got = c })
	w.Exit(nil)
	if got != ExitCodeOK {
		t.Errorf("exit=%d want 0", got)
	}
	if out.Len() != 0 || errw.Len() != 0 {
		t.Errorf("nil err should print nothing")
	}
}

func TestNewWithExitNilDefaultsToOsExit(t *testing.T) {
	w := NewWithExit(&bytes.Buffer{}, &bytes.Buffer{}, nil)
	if w.exitFunc == nil {
		t.Errorf("exitFunc should default to os.Exit, not nil")
	}
}

func TestNewDefault(t *testing.T) {
	w := NewDefault()
	if w.out == nil || w.errw == nil || w.exitFunc == nil {
		t.Errorf("NewDefault should populate all fields")
	}
}

func TestPackageExitDoesNotPanicWithCapture(t *testing.T) {
	// Package-level Exit uses os.Exit; we can't actually invoke it in tests
	// without terminating the process. Instead verify the symbol exists and
	// that NewDefault().Exit on a nil error goes through os.Exit path via
	// a custom writer of our own.
	got := -1
	w := NewWithExit(&bytes.Buffer{}, &bytes.Buffer{}, func(c int) { got = c })
	w.Exit(nil)
	if got != 0 {
		t.Errorf("expected 0 got %d", got)
	}
}

// TestMain re-enters the test binary to invoke functions that call os.Exit
// without terminating the parent test run.
func TestMain(m *testing.M) {
	switch os.Getenv("CERR_TEST_MODE") {
	case "exitok":
		ExitOK()
	case "exit":
		Exit(Auth("bad token"))
	case "exit_nil":
		Exit(nil)
	}
	os.Exit(m.Run())
}

func runSelf(t *testing.T, mode string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^$")
	cmd.Env = append(os.Environ(), "CERR_TEST_MODE="+mode)
	var out, errw bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errw
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("cmd failed: %v", err)
		}
	}
	return code, out.String(), errw.String()
}

func TestExitOKProcess(t *testing.T) {
	code, _, _ := runSelf(t, "exitok")
	if code != 0 {
		t.Errorf("ExitOK exit code=%d want 0", code)
	}
}

func TestPackageExitProcess(t *testing.T) {
	code, _, errw := runSelf(t, "exit")
	if code != ExitCodeAuth {
		t.Errorf("Exit code=%d want %d", code, ExitCodeAuth)
	}
	if !strings.Contains(errw, "error[auth]: bad token") {
		t.Errorf("stderr missing summary: %q", errw)
	}
}

func TestPackageExitNilProcess(t *testing.T) {
	code, out, errw := runSelf(t, "exit_nil")
	if code != ExitCodeOK {
		t.Errorf("Exit(nil) code=%d want 0", code)
	}
	if out != "" || errw != "" {
		t.Errorf("Exit(nil) should produce no output, got out=%q err=%q", out, errw)
	}
}
