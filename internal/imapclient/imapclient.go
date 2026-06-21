package imapclient

import (
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/emersion/go-imap/client"

	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/termio"
)

// Client is the go-imap v1 client (same as Proton Bridge tests use).
type Client = client.Client

// Dial connects to IMAP using the same client and flow as Proton Bridge tests
// (go-imap v1: Dial -> StartTLS when needed -> Login). Returns *client.Client.
func Dial(cfg config.IMAPConfig) (*Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	termio.Info("Connecting to IMAP %s (security=%s, tls_skip_verify=%v)", addr, cfg.Security, cfg.TLSSkipVerify)

	tlsConfig := &tls.Config{
		ServerName:         cfg.Host,
		InsecureSkipVerify: cfg.TLSSkipVerify,
	}

	// IMAPSecurityTLS uses implicit TLS (port 993) — connect with DialTLS directly.
	if cfg.Security == config.IMAPSecurityTLS {
		c, err := client.DialTLS(addr, tlsConfig)
		if err != nil {
			termio.Error("IMAP TLS connect failed: %v", err)
			return nil, err
		}
		termio.Info("IMAP TLS OK")
		return loginAndReturn(c, cfg)
	}

	c, err := client.Dial(addr)
	if err != nil {
		termio.Error("IMAP connect failed: %v", err)
		return nil, err
	}

	// IMAPSecurityStartTLS upgrades the plain connection; fail if the server
	// does not advertise STARTTLS rather than falling back to plaintext.
	if cfg.Security == config.IMAPSecurityStartTLS {
		ok, err := c.SupportStartTLS()
		if err != nil {
			_ = c.Logout()
			return nil, err
		}
		if !ok {
			_ = c.Logout()
			return nil, fmt.Errorf("IMAP server at %s did not advertise STARTTLS; refusing plaintext login", cfg.Host)
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			termio.Error("IMAP STARTTLS failed: %v", err)
			_ = c.Logout()
			return nil, err
		}
		termio.Info("IMAP STARTTLS OK")
	}

	return loginAndReturn(c, cfg)
}

func loginAndReturn(c *Client, cfg config.IMAPConfig) (*Client, error) {
	termio.Info("IMAP connected; logging in as %s", cfg.Username)
	if err := c.Login(cfg.Username, cfg.Password); err != nil {
		termio.Error("IMAP login failed: %v", err)
		_ = c.Logout()
		return nil, err
	}
	termio.Info("IMAP login OK")
	return c, nil
}

// CloseAndLogout logs out and closes the connection so the Bridge can release
// resources. We always call Terminate() to force-close the TCP connection even
// if Logout() fails (e.g. server already closed, timeout), so the Bridge sees
// the connection drop and doesn't require a restart.
func CloseAndLogout(c *Client) {
	if c == nil {
		return
	}
	if err := c.Logout(); err != nil && !strings.HasSuffix(err.Error(), "connection closed") {
		termio.Warn("IMAP logout: %v", err)
	}
	// Force-close our side of the connection so Bridge can clean up. Without this,
	// a half-closed or stuck connection can leave Bridge in a bad state.
	_ = c.Terminate()
}
