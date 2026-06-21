package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/higgscli/higgs/internal/cerr"
	"github.com/higgscli/higgs/internal/keystore"
	"github.com/higgscli/higgs/internal/termio"
)

// authStdin is the reader used by `auth login` for username and piped-password
// input. Tests can replace it to inject stdin without a real TTY.
var authStdin io.Reader = os.Stdin

// authPasswordReader reads a password from the given fd, returning the bytes
// read (without the terminating newline). Tests override this to bypass the
// real terminal-raw-mode requirement.
var authPasswordReader = func(fd int) ([]byte, error) {
	return term.ReadPassword(fd)
}

// authIsTerminal reports whether fd refers to an interactive terminal. Tests
// override this to simulate a TTY or a pipe as needed.
var authIsTerminal = func(fd int) bool {
	return term.IsTerminal(fd)
}

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Store and manage IMAP credentials via the OS keyring or encrypted file",
	}
	cmd.AddCommand(newAuthLoginCmd(), newAuthLogoutCmd(), newAuthStatusCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Interactively store IMAP credentials in the keystore",
		Long: `Prompt for the Bridge IMAP username and password and store them in the
selected backend (OS keyring by default). Use --password-stdin to pipe the
password from another process.`,
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,2,3",
		},
	}

	username := cmd.Flags().String("username", "", "IMAP username (skips the username prompt)")
	passwordStdin := cmd.Flags().Bool("password-stdin", false, "Read password from stdin (single line, newline-terminated)")
	backend := cmd.Flags().String("backend", "", "Force backend: 'keyring' or 'file' (default: auto)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runAuthLogin(*username, *passwordStdin, *backend)
	}
	return cmd
}

func newAuthLogoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored IMAP credentials from every keystore backend",
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0,2",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthLogout()
		},
	}
	return cmd
}

func newAuthStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report which keystore backends are available and hold credentials",
		Annotations: map[string]string{
			"stdout_format": "json",
			"exit_codes":    "0",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthStatus()
		},
	}
	return cmd
}

// selectBackend resolves the --backend flag (or "") to a concrete Backend.
func selectBackend(name string) (keystore.Backend, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return keystore.Default(), nil
	}
	for _, b := range keystore.Available() {
		switch {
		case name == "keyring" && b.Name() == "system keyring":
			return b, nil
		case name == "file" && b.Name() == "encrypted file":
			return b, nil
		}
	}
	return nil, cerr.Validation("unknown backend %q (expected 'keyring' or 'file')", name)
}

func runAuthLogin(username string, passwordStdin bool, backendName string) error {
	tio := termio.Default()

	backend, err := selectBackend(backendName)
	if err != nil {
		return err
	}
	if !backend.Available() {
		return cerr.Auth("backend %q is not available; install libsecret (Linux) or set PM_KEYSTORE_PASSPHRASE", backend.Name())
	}

	// Read username if not provided via flag.
	user := strings.TrimSpace(username)
	if user == "" {
		if !authIsTerminal(fdStdin()) {
			return cerr.Validation("stdin is not a terminal; pass --username to supply a username non-interactively")
		}
		fmt.Fprint(os.Stderr, "IMAP username: ")
		reader := bufio.NewReader(authStdin)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return cerr.Validation("read username: %s", err.Error())
		}
		user = strings.TrimSpace(line)
		if user == "" {
			return cerr.Validation("username cannot be empty")
		}
	}

	// Read password.
	var password string
	if passwordStdin {
		// Read a single line from stdin.
		reader := bufio.NewReader(authStdin)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return cerr.Validation("read --password-stdin: %s", err.Error())
		}
		password = strings.TrimRight(line, "\r\n")
	} else {
		if !authIsTerminal(fdStdin()) {
			return cerr.Validation("stdin is not a terminal and --password-stdin was not set")
		}
		fmt.Fprint(os.Stderr, "IMAP password: ")
		pw, err := authPasswordReader(fdStdin())
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return cerr.Auth("read password: %s", err.Error())
		}
		password = string(pw)
	}
	if password == "" {
		return cerr.Validation("password cannot be empty")
	}

	if err := backend.Set(keystore.Credentials{Username: user, Password: password}); err != nil {
		return cerr.Auth("store credentials in %s: %s", backend.Name(), err.Error())
	}

	return tio.PrintJSON(map[string]any{
		"stored":   true,
		"backend":  backend.Name(),
		"username": termio.SanitizeForTerminal(user),
	})
}

func runAuthLogout() error {
	tio := termio.Default()
	removed := []string{}
	for _, b := range keystore.Available() {
		if !b.Available() {
			continue
		}
		// Only report a removal when the backend actually held credentials.
		if _, err := b.Get(); err != nil {
			// No creds (ErrNotFound) or read error — skip silently.
			continue
		}
		if err := b.Delete(); err != nil {
			return cerr.Auth("delete from %s: %s", b.Name(), err.Error())
		}
		removed = append(removed, b.Name())
	}
	return tio.PrintJSON(map[string]any{"removed": removed})
}

type authBackendStatus struct {
	Name            string `json:"name"`
	Available       bool   `json:"available"`
	AvailableReason string `json:"available_reason,omitempty"`
	HasCredentials  bool   `json:"has_credentials"`
	Username        string `json:"username,omitempty"`
}

func runAuthStatus() error {
	tio := termio.Default()

	var statuses []authBackendStatus
	active := ""
	for _, b := range keystore.Available() {
		s := authBackendStatus{Name: b.Name(), Available: b.Available()}
		if !s.Available {
			s.AvailableReason = availabilityReason(b)
		}
		if s.Available {
			creds, err := b.Get()
			if err == nil {
				s.HasCredentials = true
				s.Username = termio.SanitizeForTerminal(creds.Username)
				if active == "" {
					active = b.Name()
				}
			}
		}
		statuses = append(statuses, s)
	}

	envOverride := os.Getenv("PM_IMAP_USERNAME") != "" || os.Getenv("PM_IMAP_PASSWORD") != ""

	out := map[string]any{
		"backends":     statuses,
		"active":       active,
		"env_override": envOverride,
	}
	return tio.PrintJSON(out)
}

// availabilityReason returns a short explanation for why a backend reports
// Available()=false. It is purely cosmetic and never blocks execution.
func availabilityReason(b keystore.Backend) string {
	switch b.Name() {
	case "encrypted file":
		if os.Getenv("PM_KEYSTORE_PASSPHRASE") == "" {
			return "PM_KEYSTORE_PASSPHRASE not set and no existing file"
		}
		return "cannot resolve credentials path"
	case "system keyring":
		return "keyring unavailable"
	default:
		return ""
	}
}

// fdStdin returns the file descriptor for stdin; extracted so tests could
// override it if ever needed (currently only authIsTerminal varies).
func fdStdin() int { return int(os.Stdin.Fd()) }
