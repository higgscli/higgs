// Package imaptest provides an in-memory IMAP test server based on
// go-imap v1's memory backend. It is intended for use from tests in
// other packages; it is not part of the production runtime.
package imaptest

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-imap/server"

	"github.com/higgscli/higgs/internal/config"
)

// Credentials used by the memory backend. These match the defaults baked in
// to github.com/emersion/go-imap/backend/memory.New.
const (
	// Username is the login the memory backend accepts.
	Username = "username"
	// Password is the password the memory backend accepts.
	Password = "password"
)

// Message is an in-memory message used when seeding mailboxes. Flags may be
// nil. If RFC822 is empty, a small RFC822-compliant default is synthesized.
type Message struct {
	RFC822 []byte
	Flags  []string
	Date   time.Time
}

// Server is a handle to a running in-memory IMAP server.
type Server struct {
	Addr  string
	Close func()
}

// Option mutates Server start options (seeded mailboxes etc).
type Option func(*options)

type seed struct {
	name string
	msgs []Message
}

type options struct {
	seeds       []seed
	mailboxWrap func(backend.Mailbox) backend.Mailbox
	userWrap    func(backend.User) backend.User
}

// WithMailbox seeds an additional mailbox with the given messages (appended
// via the IMAP client after the server is running). The mailbox is created if
// it does not already exist.
func WithMailbox(name string, msgs []Message) Option {
	return func(o *options) {
		o.seeds = append(o.seeds, seed{name: name, msgs: msgs})
	}
}

// WithMailboxWrapper decorates every backend mailbox the server hands to a
// session, letting tests inject misbehavior a real server can exhibit but the
// honest memory backend never does (e.g. acknowledging writes without
// applying them, or returning unstable SEARCH results). Note that seeding via
// WithMailbox flows through the same wrapper, so wrappers that break writes
// should stay dormant until Start returns (gate on an atomic the test flips).
func WithMailboxWrapper(wrap func(backend.Mailbox) backend.Mailbox) Option {
	return func(o *options) {
		o.mailboxWrap = wrap
	}
}

// WithUserWrapper decorates the backend user handed to each session, letting
// tests inject failures for user-level operations (CREATE/DELETE/RENAME
// mailbox). Seeding flows through the same wrapper — see WithMailboxWrapper.
func WithUserWrapper(wrap func(backend.User) backend.User) Option {
	return func(o *options) {
		o.userWrap = wrap
	}
}

type wrappedBackend struct {
	inner       backend.Backend
	mailboxWrap func(backend.Mailbox) backend.Mailbox
	userWrap    func(backend.User) backend.User
}

func (b *wrappedBackend) Login(ci *imap.ConnInfo, username, password string) (backend.User, error) {
	u, err := b.inner.Login(ci, username, password)
	if err != nil {
		return nil, err
	}
	if b.mailboxWrap != nil {
		u = &wrappedUser{User: u, wrap: b.mailboxWrap}
	}
	if b.userWrap != nil {
		u = b.userWrap(u)
	}
	return u, nil
}

type wrappedUser struct {
	backend.User
	wrap func(backend.Mailbox) backend.Mailbox
}

func (u *wrappedUser) GetMailbox(name string) (backend.Mailbox, error) {
	m, err := u.User.GetMailbox(name)
	if err != nil {
		return nil, err
	}
	return u.wrap(m), nil
}

func (u *wrappedUser) ListMailboxes(subscribed bool) ([]backend.Mailbox, error) {
	ms, err := u.User.ListMailboxes(subscribed)
	if err != nil {
		return nil, err
	}
	out := make([]backend.Mailbox, len(ms))
	for i, m := range ms {
		out[i] = u.wrap(m)
	}
	return out, nil
}

// Start boots an in-memory IMAP server on 127.0.0.1:0 and registers a t.Cleanup
// to close it when the test ends. Returns a handle with the listening address.
func Start(t testing.TB, opts ...Option) *Server {
	t.Helper()

	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	var bkd backend.Backend = memory.New()
	if o.mailboxWrap != nil || o.userWrap != nil {
		bkd = &wrappedBackend{inner: bkd, mailboxWrap: o.mailboxWrap, userWrap: o.userWrap}
	}
	s := server.New(bkd)
	s.AllowInsecureAuth = true
	// Keep idle connections out of the way; tests run quickly.
	s.AutoLogout = 0

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("imaptest: listen: %v", err)
	}
	s.Addr = l.Addr().String()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- s.Serve(l)
	}()

	handle := &Server{
		Addr: l.Addr().String(),
	}

	closed := false
	closeFn := func() {
		if closed {
			return
		}
		closed = true
		_ = s.Close()
		// Drain Serve goroutine but ignore its error; closing the listener
		// during Serve is the normal shutdown signal and yields a non-nil
		// error that we don't care about.
		select {
		case <-serveErr:
		case <-time.After(2 * time.Second):
		}
	}
	handle.Close = closeFn
	t.Cleanup(closeFn)

	// Seed mailboxes by logging in as the memory backend's default user and
	// issuing CREATE + APPEND. We prefer this over reaching into the backend's
	// unexported fields.
	if len(o.seeds) > 0 {
		if err := seedMailboxes(handle.Addr, o.seeds); err != nil {
			t.Fatalf("imaptest: seed: %v", err)
		}
	}

	return handle
}

func seedMailboxes(addr string, seeds []seed) error {
	c, err := client.Dial(addr)
	if err != nil {
		return fmt.Errorf("seed dial: %w", err)
	}
	defer func() { _ = c.Logout() }()
	if err := c.Login(Username, Password); err != nil {
		return fmt.Errorf("seed login: %w", err)
	}

	for _, sd := range seeds {
		// CREATE is allowed to fail if the mailbox already exists (e.g. INBOX).
		if sd.name != "INBOX" {
			if err := c.Create(sd.name); err != nil {
				// Swallow "already exists" errors; surface anything else.
				// The memory backend only errors on duplicate create.
				_ = err
			}
		}
		// The emersion memory backend pre-seeds INBOX with a default message
		// whose internal date is time.Now(). Expunge it so tests that seed
		// INBOX start from a predictable empty state.
		if sd.name == "INBOX" {
			if err := expungeAll(c, sd.name); err != nil {
				return fmt.Errorf("seed purge %q: %w", sd.name, err)
			}
		}
		for _, m := range sd.msgs {
			body := m.RFC822
			if len(body) == 0 {
				body = []byte(defaultRFC822)
			}
			date := m.Date
			if date.IsZero() {
				date = time.Now()
			}
			if err := c.Append(sd.name, m.Flags, date, bytes.NewReader(body)); err != nil {
				return fmt.Errorf("seed append %q: %w", sd.name, err)
			}
		}
	}
	return nil
}

// expungeAll flags every message in mbox as \Deleted and issues EXPUNGE.
// Used to purge the memory backend's default-seeded INBOX message.
func expungeAll(c *client.Client, mbox string) error {
	mboxStatus, err := c.Select(mbox, false)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	if mboxStatus.Messages == 0 {
		return nil
	}
	seqSet := new(imap.SeqSet)
	seqSet.AddRange(1, mboxStatus.Messages)
	item := imap.FormatFlagsOp(imap.SetFlags, true)
	flags := []interface{}{imap.DeletedFlag}
	if err := c.Store(seqSet, item, flags, nil); err != nil {
		return fmt.Errorf("store deleted: %w", err)
	}
	if err := c.Expunge(nil); err != nil {
		return fmt.Errorf("expunge: %w", err)
	}
	return nil
}

const defaultRFC822 = "From: seed@example.com\r\n" +
	"To: user@example.com\r\n" +
	"Subject: seeded message\r\n" +
	"Date: Mon, 01 Jan 2024 00:00:00 +0000\r\n" +
	"Message-ID: <seed@example.com>\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"seeded body\r\n"

// Config returns an IMAPConfig pointing at s with the memory backend's
// credentials, configured for plaintext ("insecure") connections.
func Config(s *Server) config.IMAPConfig {
	host, port := splitHostPort(s.Addr)
	return config.IMAPConfig{
		Host:          host,
		Port:          port,
		Username:      Username,
		Password:      Password,
		Security:      config.IMAPSecurityInsecure,
		TLSSkipVerify: true,
	}
}

func splitHostPort(addr string) (string, int) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1", 0
	}
	var port int
	_, _ = fmt.Sscanf(p, "%d", &port)
	if h == "" {
		h = "127.0.0.1"
	}
	return h, port
}
