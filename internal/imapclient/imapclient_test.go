package imapclient

import (
	"net"
	"testing"

	"github.com/higgscli/higgs/internal/config"
)

func TestCloseAndLogout_Nil(t *testing.T) {
	// Must not panic with nil.
	CloseAndLogout(nil)
}

// TestDial_ConnectFailure: point Dial at a TCP listener that closes the
// connection immediately so the IMAP greeting read fails. That exercises the
// "IMAP connect failed" error branch without requiring a real IMAP server.
func TestDial_ConnectFailure(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err != nil {
			return
		}
		// Close immediately — no IMAP greeting, so Dial must fail.
		_ = c.Close()
	}()

	addr := l.Addr().(*net.TCPAddr)
	cfg := config.IMAPConfig{
		Host:     "127.0.0.1",
		Port:     addr.Port,
		Username: "u",
		Password: "p",
		Security: config.IMAPSecurityInsecure,
	}
	if _, err := Dial(cfg); err == nil {
		t.Error("expected Dial to fail when server closes immediately")
	}
	<-done
}

// TestDial_UnreachableHost triggers the Dial error path by connecting to a
// port where nothing is listening.
func TestDial_UnreachableHost(t *testing.T) {
	// Bind + close to snag an unused port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	cfg := config.IMAPConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "u",
		Password: "p",
		Security: config.IMAPSecurityInsecure,
	}
	if _, err := Dial(cfg); err == nil {
		t.Error("expected Dial to fail for unreachable port")
	}
}
