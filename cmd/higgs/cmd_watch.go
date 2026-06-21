package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/config"
	"github.com/higgscli/higgs/internal/imapclient"
	"github.com/higgscli/higgs/internal/imaputil"
	"github.com/higgscli/higgs/internal/termio"
)

type watchFlags struct {
	pollInterval time.Duration
	maxEvents    int
	timeout      time.Duration
}

func newWatchCmd() *cobra.Command {
	f := &watchFlags{}
	cmd := &cobra.Command{
		Use:   "watch [mailbox]",
		Short: "Stream mailbox change events as NDJSON until cancelled",
		Long: `watch polls the given mailbox (default INBOX) and emits NDJSON event rows
whenever messages are appended, expunged, or have their flags changed. The
memory-backend test server and some IMAP servers do not support IDLE, so this
implementation uses a UID-set polling strategy (--poll-interval, default 30s).

Exits cleanly on SIGINT/SIGTERM, after --max-events events have been emitted,
or after --timeout has elapsed.`,
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"stdout_format": "ndjson",
			"exit_codes":    "0,3,4,5",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			mailbox := "INBOX"
			if len(args) > 0 {
				mailbox = args[0]
			}
			return cmdWatch(mailbox, f)
		},
	}
	cmd.Flags().DurationVar(&f.pollInterval, "poll-interval", 30*time.Second, "Polling interval (e.g. 5s, 1m)")
	cmd.Flags().IntVar(&f.maxEvents, "max-events", 0, "Exit after emitting N events (0 = no limit)")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 0, "Exit after this duration (0 = no timeout)")
	return cmd
}

func cmdWatch(mailbox string, f *watchFlags) error {
	if f.pollInterval < 0 {
		return cerr.Validation("--poll-interval must be >= 0")
	}
	if f.maxEvents < 0 {
		return cerr.Validation("--max-events must be >= 0")
	}
	if f.timeout < 0 {
		return cerr.Validation("--timeout must be >= 0")
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

	base := context.Background()
	var cancelTimeout context.CancelFunc
	if f.timeout > 0 {
		base, cancelTimeout = context.WithTimeout(base, f.timeout)
		defer cancelTimeout()
	}
	ctx, stop := signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM)
	defer stop()

	events, errs, err := imapclient.Watch(ctx, c, resolved, f.pollInterval)
	if err != nil {
		return cerr.IMAP(imapclient.Wrap(err), "watch %q", resolved)
	}

	w := termio.Default()
	emitted := 0
	var watchErr error
LOOP:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break LOOP
			}
			if err := w.PrintNDJSON(map[string]any{
				"type":    "event",
				"kind":    ev.Kind,
				"uid":     ev.UID,
				"mailbox": ev.Mailbox,
				"at":      ev.At.UTC().Format(time.RFC3339),
			}); err != nil {
				return cerr.Internal(err, "print event")
			}
			emitted++
			if f.maxEvents > 0 && emitted >= f.maxEvents {
				break LOOP
			}
		case e, ok := <-errs:
			if !ok {
				break LOOP
			}
			if e != nil {
				watchErr = e
			}
		case <-ctx.Done():
			break LOOP
		}
	}
	stop()
	// Drain remaining events post-cancel so the channels can close.
	drainWatchChannels(events, errs, 2*time.Second)

	if err := w.PrintNDJSON(map[string]any{
		"type":           "summary",
		"mailbox":        resolved,
		"events_emitted": emitted,
	}); err != nil {
		return cerr.Internal(err, "print summary")
	}
	if watchErr != nil {
		return cerr.IMAP(imapclient.Wrap(watchErr), "watch")
	}
	return nil
}

// drainWatchChannels consumes from the two channels until both are closed or
// the deadline fires. Safe to call on already-closed channels.
func drainWatchChannels(events <-chan imapclient.Event, errs <-chan error, d time.Duration) {
	deadline := time.After(d)
	for events != nil || errs != nil {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case _, ok := <-errs:
			if !ok {
				errs = nil
			}
		case <-deadline:
			return
		}
	}
}
