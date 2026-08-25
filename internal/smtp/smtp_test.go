package smtp

import (
	"bytes"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestBuildTextOnly(t *testing.T) {
	got, err := Build(Envelope{
		From:      "a@example.com",
		To:        []string{"b@example.com"},
		Subject:   "hello",
		BodyText:  "world\n",
		Date:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		MessageID: "<fixed@t>",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(got)
	for _, sub := range []string{
		"From: a@example.com",
		"To: b@example.com",
		"Subject: hello",
		"Message-ID: <fixed@t>",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
		"MIME-Version: 1.0",
	} {
		if !strings.Contains(s, sub) {
			t.Errorf("missing %q in output:\n%s", sub, s)
		}
	}
	if !strings.Contains(s, "\r\n\r\n") {
		t.Errorf("no CRLF CRLF separator: %q", s)
	}
	// Round-trip parse via net/mail.
	m, err := mail.ReadMessage(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("round-trip parse: %v", err)
	}
	if m.Header.Get("Subject") != "hello" {
		t.Errorf("subject: got %q", m.Header.Get("Subject"))
	}
}

func TestBuildMultipart(t *testing.T) {
	got, err := Build(Envelope{
		From:     "a@x.com",
		To:       []string{"b@x.com"},
		Subject:  "S",
		BodyText: "plain text",
		BodyHTML: "<b>html</b>",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "multipart/alternative") {
		t.Errorf("not multipart: %s", s)
	}
	if !strings.Contains(s, "Content-Type: text/plain") {
		t.Errorf("no text part: %s", s)
	}
	if !strings.Contains(s, "Content-Type: text/html") {
		t.Errorf("no html part: %s", s)
	}
	// Round-trip
	if _, err := mail.ReadMessage(bytes.NewReader(got)); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestBuildHTMLOnly(t *testing.T) {
	got, err := Build(Envelope{
		From:     "a@x.com",
		To:       []string{"b@x.com"},
		BodyHTML: "<p>hi</p>",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(got), "Content-Type: text/html") {
		t.Errorf("expected text/html content-type: %s", string(got))
	}
}

func TestBuildCcAndBccAndHeaders(t *testing.T) {
	got, err := Build(Envelope{
		From:       "a@x.com",
		To:         []string{"b@x.com", "c@x.com"},
		Cc:         []string{"d@x.com"},
		Subject:    "hi",
		BodyText:   "t",
		InReplyTo:  "<orig@t>",
		References: "<orig@t>",
		Headers: map[string]string{
			"X-Custom": "value",
			"X-Foo":    "bar",
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "To: b@x.com, c@x.com") {
		t.Errorf("To join wrong: %s", s)
	}
	if !strings.Contains(s, "Cc: d@x.com") {
		t.Errorf("Cc missing: %s", s)
	}
	if !strings.Contains(s, "In-Reply-To: <orig@t>") {
		t.Errorf("In-Reply-To missing: %s", s)
	}
	if !strings.Contains(s, "References: <orig@t>") {
		t.Errorf("References missing: %s", s)
	}
	if !strings.Contains(s, "X-Custom: value") || !strings.Contains(s, "X-Foo: bar") {
		t.Errorf("custom headers: %s", s)
	}
}

func TestBuildHeaderInjectionRejected(t *testing.T) {
	cases := []struct {
		name string
		e    Envelope
	}{
		{"from-newline", Envelope{From: "a@x.com\r\nBcc: attacker@x.com", To: []string{"b@x.com"}, BodyText: "t"}},
		{"to-newline", Envelope{From: "a@x.com", To: []string{"b@x.com\nBcc: e@x.com"}, BodyText: "t"}},
		{"subject-cr", Envelope{From: "a@x.com", To: []string{"b@x.com"}, Subject: "evil\rX: Y", BodyText: "t"}},
		{"subject-lf", Envelope{From: "a@x.com", To: []string{"b@x.com"}, Subject: "evil\nX: Y", BodyText: "t"}},
		{"custom-header-val", Envelope{From: "a@x.com", To: []string{"b@x.com"}, BodyText: "t", Headers: map[string]string{"X-Evil": "a\r\nB: b"}}},
		{"custom-header-name", Envelope{From: "a@x.com", To: []string{"b@x.com"}, BodyText: "t", Headers: map[string]string{"Evil:Name": "v"}}},
		{"in-reply-to-newline", Envelope{From: "a@x.com", To: []string{"b@x.com"}, BodyText: "t", InReplyTo: "<a\n>"}},
		{"references-newline", Envelope{From: "a@x.com", To: []string{"b@x.com"}, BodyText: "t", References: "<a\r>"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.e)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestBuildValidation(t *testing.T) {
	if _, err := Build(Envelope{To: []string{"b@x.com"}, BodyText: "t"}); err == nil {
		t.Error("expected error for missing From")
	}
	if _, err := Build(Envelope{From: "a@x.com", BodyText: "t"}); err == nil {
		t.Error("expected error for missing recipient")
	}
}

func TestGenerateMessageID(t *testing.T) {
	id, err := generateMessageID()
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, ">") || !strings.Contains(id, "@") {
		t.Errorf("bad id: %q", id)
	}
	id2, _ := generateMessageID()
	if id == id2 {
		t.Errorf("ids collided: %q %q", id, id2)
	}
}

func TestValidateHeaderName(t *testing.T) {
	if err := validateHeaderName("X-Good"); err != nil {
		t.Errorf("good: %v", err)
	}
	if err := validateHeaderName(""); err == nil {
		t.Error("empty should fail")
	}
	if err := validateHeaderName("Bad:Name"); err == nil {
		t.Error("colon should fail")
	}
	if err := validateHeaderName("Bad Name"); err == nil {
		t.Error("space should fail")
	}
}

func TestConfigAddr(t *testing.T) {
	c := Config{Host: "h", Port: 25}
	if c.Addr() != "h:25" {
		t.Errorf("got %q", c.Addr())
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("PM_SMTP_HOST", "")
	t.Setenv("PM_SMTP_PORT", "")
	if _, ok := ConfigFromEnv(); ok {
		t.Error("empty env should be not-ok")
	}
	t.Setenv("PM_SMTP_HOST", "smtp.example.com")
	t.Setenv("PM_SMTP_PORT", "587")
	t.Setenv("PM_SMTP_USERNAME", "u")
	t.Setenv("PM_SMTP_PASSWORD", "p")
	cfg, ok := ConfigFromEnv()
	if !ok {
		t.Fatal("expected ok")
	}
	if cfg.Host != "smtp.example.com" || cfg.Port != 587 || cfg.Username != "u" || cfg.Password != "p" {
		t.Errorf("bad cfg: %+v", cfg)
	}

	t.Setenv("PM_SMTP_PORT", "not-an-int")
	if _, ok := ConfigFromEnv(); ok {
		t.Error("bad port should fail")
	}
	t.Setenv("PM_SMTP_PORT", "0")
	if _, ok := ConfigFromEnv(); ok {
		t.Error("zero port should fail")
	}
}

func TestConfigFromEnvTLSSkipVerify(t *testing.T) {
	t.Setenv("PM_SMTP_USERNAME", "")
	t.Setenv("PM_SMTP_PASSWORD", "")
	t.Setenv("PM_SMTP_TLS_SKIP_VERIFY", "")

	t.Setenv("PM_SMTP_HOST", "smtp.example.com")
	t.Setenv("PM_SMTP_PORT", "587")
	if cfg, _ := ConfigFromEnv(); cfg.TLSSkipVerify {
		t.Error("non-loopback host should verify by default")
	}

	t.Setenv("PM_SMTP_HOST", "127.0.0.1")
	if cfg, _ := ConfigFromEnv(); !cfg.TLSSkipVerify {
		t.Error("loopback host should skip verification by default")
	}

	t.Setenv("PM_SMTP_TLS_SKIP_VERIFY", "false")
	if cfg, _ := ConfigFromEnv(); cfg.TLSSkipVerify {
		t.Error("explicit false should win over loopback default")
	}

	t.Setenv("PM_SMTP_HOST", "smtp.example.com")
	t.Setenv("PM_SMTP_TLS_SKIP_VERIFY", "true")
	if cfg, _ := ConfigFromEnv(); !cfg.TLSSkipVerify {
		t.Error("explicit true should win over non-loopback default")
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1":        true,
		"::1":              true,
		"localhost":        true,
		"LOCALHOST":        true,
		"smtp.example.com": false,
		"192.168.1.10":     false,
		"":                 false,
	} {
		if got := IsLoopback(host); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestSendInvalidAddr(t *testing.T) {
	err := Send(Config{Host: "127.0.0.1", Port: 1}, "a@x.com\r\nX: y", []string{"b@x.com"}, []byte("msg"))
	if err == nil {
		t.Error("expected injection error on from")
	}
	err = Send(Config{Host: "127.0.0.1", Port: 1}, "a@x.com", []string{"b@x.com\r\n"}, []byte("msg"))
	if err == nil {
		t.Error("expected injection error on to")
	}
}

func TestBuildWithDefaultDate(t *testing.T) {
	got, err := Build(Envelope{From: "a@x.com", To: []string{"b@x.com"}, BodyText: "t"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(got), "Date: ") {
		t.Errorf("missing date: %s", string(got))
	}
}
