// Package smtp composes RFC5322 messages and (optionally) sends them via
// stdlib net/smtp. The primary entry point is Build, which produces a byte
// slice suitable for IMAP APPEND (drafts) or SMTP DATA (sending).
//
// Header values are validated to reject CR/LF to prevent SMTP/header
// injection attacks.
package smtp

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"os"
	"sort"
	"strings"
	"time"
)

// Envelope describes a message to compose. BodyText is preferred; BodyHTML
// is optional. When both are provided, the message becomes multipart/alternative.
type Envelope struct {
	From       string
	To         []string
	Cc         []string
	Bcc        []string
	Subject    string
	BodyText   string
	BodyHTML   string
	InReplyTo  string
	References string
	Headers    map[string]string
	// Date may be set by tests; when zero, Build uses time.Now().
	Date time.Time
	// MessageID may be set by callers for deterministic output; when empty,
	// Build generates one of the form "<hex@hostname>".
	MessageID string
}

// ErrHeaderInjection is returned when a header value contains a newline.
var ErrHeaderInjection = errors.New("smtp: header value contains CR or LF (injection attempt)")

// Build renders e as an RFC5322 message. When BodyText and BodyHTML are both
// set, the body is multipart/alternative; otherwise a single text/plain or
// text/html part is emitted. All header values are validated against newline
// injection.
func Build(e Envelope) ([]byte, error) {
	if strings.TrimSpace(e.From) == "" {
		return nil, errors.New("smtp: From is required")
	}
	if len(e.To) == 0 && len(e.Cc) == 0 && len(e.Bcc) == 0 {
		return nil, errors.New("smtp: at least one recipient (To/Cc/Bcc) is required")
	}

	headers := make([][2]string, 0, 16)

	if err := validateAddr(e.From); err != nil {
		return nil, err
	}
	headers = append(headers, [2]string{"From", e.From})

	if len(e.To) > 0 {
		joined, err := joinAddrs(e.To)
		if err != nil {
			return nil, err
		}
		headers = append(headers, [2]string{"To", joined})
	}
	if len(e.Cc) > 0 {
		joined, err := joinAddrs(e.Cc)
		if err != nil {
			return nil, err
		}
		headers = append(headers, [2]string{"Cc", joined})
	}
	// Bcc is intentionally NOT written into the body (per RFC) — but caller
	// may use envelope recipients when sending. Include as an extra header
	// only when caller explicitly opts in via Headers.

	if e.Subject != "" {
		if err := validateHeaderValue(e.Subject); err != nil {
			return nil, err
		}
		headers = append(headers, [2]string{"Subject", e.Subject})
	}

	date := e.Date
	if date.IsZero() {
		date = time.Now()
	}
	headers = append(headers, [2]string{"Date", date.UTC().Format(time.RFC1123Z)})

	msgID := e.MessageID
	if msgID == "" {
		id, err := generateMessageID()
		if err != nil {
			return nil, err
		}
		msgID = id
	}
	if err := validateHeaderValue(msgID); err != nil {
		return nil, err
	}
	headers = append(headers, [2]string{"Message-ID", msgID})

	if e.InReplyTo != "" {
		if err := validateHeaderValue(e.InReplyTo); err != nil {
			return nil, err
		}
		headers = append(headers, [2]string{"In-Reply-To", e.InReplyTo})
	}
	if e.References != "" {
		if err := validateHeaderValue(e.References); err != nil {
			return nil, err
		}
		headers = append(headers, [2]string{"References", e.References})
	}

	// Extra headers: sort for deterministic output.
	if len(e.Headers) > 0 {
		keys := make([]string, 0, len(e.Headers))
		for k := range e.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := e.Headers[k]
			if err := validateHeaderName(k); err != nil {
				return nil, err
			}
			if err := validateHeaderValue(v); err != nil {
				return nil, err
			}
			headers = append(headers, [2]string{k, v})
		}
	}

	headers = append(headers, [2]string{"MIME-Version", "1.0"})

	var body []byte
	if e.BodyText != "" && e.BodyHTML != "" {
		boundary, err := generateBoundary()
		if err != nil {
			return nil, err
		}
		headers = append(headers, [2]string{"Content-Type", `multipart/alternative; boundary="` + boundary + `"`})
		body = buildMultipart(boundary, e.BodyText, e.BodyHTML)
	} else if e.BodyHTML != "" {
		headers = append(headers, [2]string{"Content-Type", "text/html; charset=UTF-8"})
		headers = append(headers, [2]string{"Content-Transfer-Encoding", "quoted-printable"})
		body = encodeQP(e.BodyHTML)
	} else {
		headers = append(headers, [2]string{"Content-Type", "text/plain; charset=UTF-8"})
		headers = append(headers, [2]string{"Content-Transfer-Encoding", "quoted-printable"})
		body = encodeQP(e.BodyText)
	}

	var buf strings.Builder
	for _, h := range headers {
		buf.WriteString(h[0])
		buf.WriteString(": ")
		buf.WriteString(h[1])
		buf.WriteString("\r\n")
	}
	buf.WriteString("\r\n")
	buf.Write(body)

	return []byte(buf.String()), nil
}

// buildMultipart composes a multipart/alternative body with a text/plain
// and text/html part, using quoted-printable encoding for both.
func buildMultipart(boundary, text, html string) []byte {
	var buf strings.Builder
	buf.WriteString("This is a multipart message in MIME format.\r\n")
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	buf.Write(encodeQP(text))
	buf.WriteString("\r\n--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	buf.Write(encodeQP(html))
	buf.WriteString("\r\n--" + boundary + "--\r\n")
	return []byte(buf.String())
}

func encodeQP(s string) []byte {
	var buf strings.Builder
	w := quotedprintable.NewWriter(&buf)
	_, _ = io.WriteString(w, s)
	_ = w.Close()
	return []byte(buf.String())
}

func generateMessageID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "localhost"
	}
	return "<" + hex.EncodeToString(b) + "@" + host + ">", nil
}

func generateBoundary() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "=_higgs_" + hex.EncodeToString(b), nil
}

// validateHeaderValue rejects CR/LF in header values (defense against SMTP
// header injection). Also rejects NULL bytes.
func validateHeaderValue(v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return ErrHeaderInjection
	}
	return nil
}

func validateHeaderName(name string) error {
	if name == "" {
		return errors.New("smtp: empty header name")
	}
	// RFC5322 field-name = 1*(printable ASCII, except ':')
	for _, r := range name {
		if r < 33 || r > 126 || r == ':' {
			return fmt.Errorf("smtp: invalid character in header name %q", name)
		}
	}
	return nil
}

func validateAddr(s string) error {
	if err := validateHeaderValue(s); err != nil {
		return err
	}
	if strings.TrimSpace(s) == "" {
		return errors.New("smtp: empty address")
	}
	return nil
}

func joinAddrs(addrs []string) (string, error) {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if strings.TrimSpace(a) == "" {
			continue
		}
		if err := validateAddr(a); err != nil {
			return "", err
		}
		parts = append(parts, a)
	}
	if len(parts) == 0 {
		return "", errors.New("smtp: no valid addresses")
	}
	return strings.Join(parts, ", "), nil
}

// Config describes a minimal SMTP server. Auth uses PLAIN when Username is
// non-empty; otherwise no AUTH is attempted.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	// TLSSkipVerify disables certificate verification during STARTTLS.
	// Proton Mail Bridge serves a self-signed certificate on loopback, so
	// this defaults to true for loopback hosts (mirroring the IMAP side).
	TLSSkipVerify bool
}

// Addr returns "host:port".
func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// Send delivers raw RFC5322 bytes to cfg's SMTP server, upgrading to TLS via
// STARTTLS when the server advertises it and using PLAIN auth when credentials
// are set. Recipients are the combined To/Cc/Bcc list. Unlike net/smtp.SendMail
// this honors cfg.TLSSkipVerify, which Bridge's self-signed loopback
// certificate requires.
func Send(cfg Config, from string, to []string, msg []byte) error {
	if err := validateAddr(from); err != nil {
		return err
	}
	for _, r := range to {
		if err := validateAddr(r); err != nil {
			return err
		}
	}
	c, err := smtp.Dial(cfg.Addr())
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.TLSSkipVerify}
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); ok {
			auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
			if err := c.Auth(auth); err != nil {
				return err
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// IsLoopback reports whether host is a loopback address ("localhost", 127.x,
// ::1), matching the IMAP side's default for TLS verification.
func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ConfigFromEnv reads PM_SMTP_HOST/PM_SMTP_PORT/PM_SMTP_USERNAME/PM_SMTP_PASSWORD.
// Returns (_, false) when host or port is unset. PM_SMTP_TLS_SKIP_VERIFY
// overrides the loopback-based default for certificate verification.
func ConfigFromEnv() (Config, bool) {
	host := strings.TrimSpace(os.Getenv("PM_SMTP_HOST"))
	portStr := strings.TrimSpace(os.Getenv("PM_SMTP_PORT"))
	if host == "" || portStr == "" {
		return Config{}, false
	}
	port := 0
	_, err := fmt.Sscanf(portStr, "%d", &port)
	if err != nil || port <= 0 {
		return Config{}, false
	}
	skipVerify := IsLoopback(host)
	if v := strings.TrimSpace(os.Getenv("PM_SMTP_TLS_SKIP_VERIFY")); v != "" {
		skipVerify = strings.EqualFold(v, "true")
	}
	return Config{
		Host:          host,
		Port:          port,
		Username:      os.Getenv("PM_SMTP_USERNAME"),
		Password:      os.Getenv("PM_SMTP_PASSWORD"),
		TLSSkipVerify: skipVerify,
	}, true
}
