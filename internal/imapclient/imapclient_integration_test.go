package imapclient_test

import (
	"strings"
	"testing"

	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaptest"
)

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestDial_Insecure_Success exercises the insecure (plain-TCP) Dial + Login
// path against the in-memory IMAP server.
func TestDial_Insecure_Success(t *testing.T) {
	srv := imaptest.Start(t)
	cfg := imaptest.Config(srv)

	c, err := imapclient.Dial(cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if c == nil {
		t.Fatal("Dial returned nil client")
	}
	imapclient.CloseAndLogout(c)
}

// TestDial_Insecure_BadPassword exercises the login-failure branch (we dial
// successfully, but Login returns an error).
func TestDial_Insecure_BadPassword(t *testing.T) {
	srv := imaptest.Start(t)
	cfg := imaptest.Config(srv)
	cfg.Password = "wrong-password"

	if _, err := imapclient.Dial(cfg); err == nil {
		t.Fatal("expected Dial to fail with bad password")
	}
}

// TestDial_StartTLS_NotAdvertised verifies that Dial refuses to log in when the
// server does not advertise STARTTLS. The old behaviour (warn and continue in
// plaintext) was a security hole; Dial must now return an error so credentials
// are never sent over an unencrypted connection.
func TestDial_StartTLS_NotAdvertised(t *testing.T) {
	srv := imaptest.Start(t)
	cfg := imaptest.Config(srv)
	cfg.Security = config.IMAPSecurityStartTLS

	_, err := imapclient.Dial(cfg)
	if err == nil {
		t.Fatal("Dial should have failed when server does not advertise STARTTLS")
	}
	if !containsAny(err.Error(), "did not advertise STARTTLS", "refusing plaintext") {
		t.Errorf("unexpected error text: %v", err)
	}
}

// TestDial_TLS_RequiresTLS verifies that security=tls actually performs a TLS
// handshake and fails against a plain-TCP server. The old behaviour (log a
// warning and fall back to plaintext login) was a security hole; the mode now
// uses client.DialTLS and must return a TLS error when the server is not TLS.
func TestDial_TLSBranch_WarnOnly(t *testing.T) {
	srv := imaptest.Start(t)
	cfg := imaptest.Config(srv)
	cfg.Security = config.IMAPSecurityTLS

	_, err := imapclient.Dial(cfg)
	if err == nil {
		t.Fatal("Dial should have failed: security=tls against a plain-TCP server must not succeed")
	}
}

// TestCloseAndLogout_Roundtrip verifies Logout + Terminate work end-to-end.
func TestCloseAndLogout_Roundtrip(t *testing.T) {
	srv := imaptest.Start(t)
	cfg := imaptest.Config(srv)

	c, err := imapclient.Dial(cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	// Explicitly call twice to make sure the helper is safe on an already-
	// closed connection.
	imapclient.CloseAndLogout(c)
	imapclient.CloseAndLogout(c)
}
