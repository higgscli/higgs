package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imapfetch"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/smtp"
	"github.com/higgscli/higgs/internal/termio"
)

// privateIPNets contains IP ranges that must never be dialed from email-header URLs.
var privateIPNets = func() []*net.IPNet {
	var nets []*net.IPNet
	for _, cidr := range []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "::1/128", "fc00::/7", "169.254.0.0/16", "fe80::/10",
	} {
		_, n, _ := net.ParseCIDR(cidr)
		nets = append(nets, n)
	}
	return nets
}()

// guardedDialContext rejects connections to private/loopback addresses to
// prevent DNS-rebinding SSRF: a domain in a spoofed email header could resolve
// to an internal IP after TLS cert validation passes for the public name.
func guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, raw := range ips {
		ip := net.ParseIP(raw)
		for _, block := range privateIPNets {
			if block.Contains(ip) {
				return nil, fmt.Errorf("unsubscribe: refusing connection to private/internal address %s", raw)
			}
		}
	}
	return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
}

// unsubscribeHTTPClient is a package variable to allow tests to inject a
// custom http.Client (for redirect-checking, etc.).
var unsubscribeHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	// Disable redirects so one-click POST unsubscribes are a single request,
	// matching RFC 8058 expectations.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		DialContext: guardedDialContext,
	},
}

// unsubscribeSender abstracts SMTP send so tests can verify mailto handling
// without a real SMTP server.
type unsubscribeSender func(cfg smtp.Config, from string, to []string, msg []byte) error

var unsubscribeSend unsubscribeSender = smtp.Send

// unsubscribeSMTPLookup reads SMTP config; tests may replace it.
var unsubscribeSMTPLookup = smtp.ConfigFromEnv

type unsubscribeFlags struct {
	uids       string
	httpOnly   bool
	mailtoOnly bool
	dryRun     bool
}

func newUnsubscribeCmd() *cobra.Command {
	f := &unsubscribeFlags{}
	cmd := &cobra.Command{
		Use:   "unsubscribe <mailbox>",
		Short: "Honor List-Unsubscribe / List-Unsubscribe-Post headers for messages",
		Long: `unsubscribe parses the List-Unsubscribe header on each message and acts on
it: HTTP URLs are POSTed (one-click) when List-Unsubscribe-Post is present,
otherwise GET; mailto: URIs are sent as empty unsubscribe emails (requires
SMTP env vars). Results stream as NDJSON with a summary terminator.`,
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdUnsubscribe(args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.uids, "uid", "", "Comma-separated UIDs to target (required)")
	cmd.Flags().BoolVar(&f.httpOnly, "http-only", false, "Only use HTTP URLs")
	cmd.Flags().BoolVar(&f.mailtoOnly, "mailto-only", false, "Only use mailto: URIs")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Emit the planned action without unsubscribing")
	return cmd
}

func cmdUnsubscribe(mailbox string, f *unsubscribeFlags) error {
	if f.httpOnly && f.mailtoOnly {
		return cerr.Validation("--http-only and --mailto-only are mutually exclusive")
	}
	uids, err := parseUIDList(f.uids)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}
	if len(uids) == 0 {
		return cerr.Validation("--uid is required with at least one UID")
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}
	c, err := imapclient.Dial(cfg.IMAP)
	if err != nil {
		return cerr.Auth("%s", err.Error())
	}
	defer imapclient.CloseAndLogout(c)

	mboxes, err := imaputil.ListMailboxes(c, false)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "LIST failed")
	}
	resolved, err := imaputil.ResolveMailboxName(mailbox, mboxes)
	if err != nil {
		return cerr.Validation("%s", err.Error())
	}
	if _, err := imapfetch.SelectMailbox(c, resolved); err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "SELECT %q", resolved)
	}
	msgs, err := imapfetch.FetchRFC822(c, uids)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "UID FETCH")
	}

	w := termio.Default()
	attempted, succeeded, failed, skipped := 0, 0, 0, 0
	from := cfg.IMAP.Username

	missing, err := reportMissingUIDs(w, resolved, uids, msgs)
	if err != nil {
		return err
	}
	attempted += missing
	failed += missing

	for _, m := range msgs {
		attempted++
		hdr, _ := parseRFC5322Headers(m.RFC822)
		luValue := hdr.Get("List-Unsubscribe")
		lupValue := hdr.Get("List-Unsubscribe-Post")
		if strings.TrimSpace(luValue) == "" {
			skipped++
			if err := w.PrintNDJSON(map[string]any{
				"type":   "skipped",
				"uid":    m.UID,
				"reason": "no List-Unsubscribe header",
			}); err != nil {
				return cerr.Internal(err, "print")
			}
			continue
		}
		targets := parseListUnsubscribe(luValue)
		oneClick := isOneClick(lupValue)

		target, method := pickUnsubscribeTarget(targets, f.httpOnly, f.mailtoOnly)
		if target == "" {
			skipped++
			if err := w.PrintNDJSON(map[string]any{
				"type":   "skipped",
				"uid":    m.UID,
				"reason": "no suitable List-Unsubscribe target",
			}); err != nil {
				return cerr.Internal(err, "print")
			}
			continue
		}

		if f.dryRun {
			if err := w.PrintNDJSON(map[string]any{
				"type":   "pending",
				"uid":    m.UID,
				"method": method,
				"url":    target,
			}); err != nil {
				return cerr.Internal(err, "print")
			}
			continue
		}

		switch method {
		case "http":
			status, err := doHTTPUnsubscribe(target, oneClick)
			if err != nil {
				failed++
				if werr := emitUnsubscribeFailure(w, m.UID, err); werr != nil {
					return werr
				}
				continue
			}
			succeeded++
			if err := w.PrintNDJSON(map[string]any{
				"type":   "unsubscribed",
				"uid":    m.UID,
				"method": "http",
				"url":    target,
				"status": status,
			}); err != nil {
				return cerr.Internal(err, "print")
			}
		case "mailto":
			smtpCfg, ok := unsubscribeSMTPLookup()
			if !ok {
				skipped++
				if err := w.PrintNDJSON(map[string]any{
					"type":   "skipped",
					"uid":    m.UID,
					"method": "mailto",
					"url":    target,
					"reason": "smtp not configured",
				}); err != nil {
					return cerr.Internal(err, "print")
				}
				continue
			}
			if err := doMailtoUnsubscribe(target, from, smtpCfg); err != nil {
				failed++
				if werr := emitUnsubscribeFailure(w, m.UID, err); werr != nil {
					return werr
				}
				continue
			}
			succeeded++
			if err := w.PrintNDJSON(map[string]any{
				"type":   "unsubscribed",
				"uid":    m.UID,
				"method": "mailto",
				"url":    target,
				"status": "sent",
			}); err != nil {
				return cerr.Internal(err, "print")
			}
		}
	}

	return w.PrintNDJSON(map[string]any{
		"type":      "summary",
		"attempted": attempted,
		"succeeded": succeeded,
		"failed":    failed,
		"skipped":   skipped,
	})
}

func emitUnsubscribeFailure(w *termio.Writer, uid uint32, err error) error {
	env := cerr.From(err).ToEnvelope()["error"]
	if err := w.PrintNDJSON(map[string]any{
		"type":  "failed",
		"uid":   uid,
		"error": env,
	}); err != nil {
		return cerr.Internal(err, "print")
	}
	return nil
}

// parseListUnsubscribe extracts the URIs from a List-Unsubscribe header value.
// The header is a comma-separated list of <uri> items per RFC 2369.
func parseListUnsubscribe(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">") {
			part = strings.TrimSpace(part[1 : len(part)-1])
		}
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// isOneClick checks whether a List-Unsubscribe-Post value signals RFC 8058
// one-click unsubscribe.
func isOneClick(v string) bool {
	return strings.Contains(strings.ToLower(strings.ReplaceAll(v, " ", "")), "list-unsubscribe=one-click")
}

// pickUnsubscribeTarget chooses the first matching target honoring filters.
// Returns ("", "") if none suitable.
func pickUnsubscribeTarget(targets []string, httpOnly, mailtoOnly bool) (string, string) {
	// Pass 1: prefer HTTPS unless mailtoOnly. Plain http:// targets are skipped
	// to avoid sending credentials or triggering requests over unencrypted links.
	if !mailtoOnly {
		for _, t := range targets {
			if strings.HasPrefix(strings.ToLower(t), "https://") {
				return t, "http"
			}
		}
	}
	if !httpOnly {
		for _, t := range targets {
			if strings.HasPrefix(strings.ToLower(t), "mailto:") {
				return t, "mailto"
			}
		}
	}
	return "", ""
}

// doHTTPUnsubscribe issues a POST (one-click) or GET, returning the status
// code on success.
func doHTTPUnsubscribe(target string, oneClick bool) (int, error) {
	var req *http.Request
	var err error
	if oneClick {
		req, err = http.NewRequest(http.MethodPost, target, strings.NewReader("List-Unsubscribe=One-Click"))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req, err = http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			return 0, err
		}
	}
	resp, err := unsubscribeHTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("unsubscribe HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// doMailtoUnsubscribe parses a mailto: URI, composes an unsubscribe message,
// and delivers it via smtp.Send.
func doMailtoUnsubscribe(target, from string, cfg smtp.Config) error {
	to, subject, body, err := parseMailto(target)
	if err != nil {
		return err
	}
	if subject == "" {
		subject = "unsubscribe"
	}
	env := smtp.Envelope{
		From:     from,
		To:       []string{to},
		Subject:  subject,
		BodyText: body,
	}
	raw, err := smtp.Build(env)
	if err != nil {
		return err
	}
	return unsubscribeSend(cfg, from, []string{to}, raw)
}

// parseMailto returns the recipient, subject override, and body override
// parsed from a mailto: URI.
func parseMailto(uri string) (to, subject, body string, err error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", "", err
	}
	if !strings.EqualFold(u.Scheme, "mailto") {
		return "", "", "", fmt.Errorf("not a mailto: URI: %q", uri)
	}
	to = u.Opaque
	if to == "" {
		to = u.Path
	}
	if to == "" {
		return "", "", "", fmt.Errorf("mailto has no recipient: %q", uri)
	}
	q := u.Query()
	subject = q.Get("subject")
	body = q.Get("body")
	return to, subject, body, nil
}

// parseRFC5322Headers returns a mail.Header parsed from the leading headers
// of raw. The body is discarded.
func parseRFC5322Headers(raw []byte) (mail.Header, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return msg.Header, nil
}
