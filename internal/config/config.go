// Package config loads protoncli runtime configuration (IMAP, Ollama, state)
// from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/akeemjenkins/protoncli/internal/cerr"
	"github.com/akeemjenkins/protoncli/internal/keystore"
	"github.com/akeemjenkins/protoncli/internal/termio"
)

type IMAPSecurity string

const (
	IMAPSecurityStartTLS IMAPSecurity = "starttls"
	IMAPSecurityTLS      IMAPSecurity = "tls"
	IMAPSecurityInsecure IMAPSecurity = "insecure"
)

type IMAPConfig struct {
	Host          string
	Port          int
	Username      string
	Password      string
	Security      IMAPSecurity
	TLSSkipVerify bool
}

type OllamaConfig struct {
	BaseURL string
	Model   string
}

type Config struct {
	IMAP   IMAPConfig
	Ollama OllamaConfig
}

// LoadFromEnv loads config from environment. Default host/port match Proton Mail Bridge
// (see docs/BRIDGE_REFERENCE.md and proton-bridge/internal/constants, vault/types_settings).
func LoadFromEnv() (Config, error) {
	host := getEnvDefault("PM_IMAP_HOST", "127.0.0.1")
	portStr := getEnvDefault("PM_IMAP_PORT", "1143")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Config{}, fmt.Errorf("PM_IMAP_PORT must be an int: %w", err)
	}

	username, password, source := resolveCredentials()
	if username == "" || password == "" {
		return Config{}, cerr.Auth("no credentials: run 'protoncli auth login' or set PM_IMAP_USERNAME/PM_IMAP_PASSWORD")
	}
	termio.Info("IMAP credentials sourced from %s", source)

	security, err := getIMAPSecurity()
	if err != nil {
		return Config{}, err
	}
	tlsSkipVerifyDefault := isLoopback(host)
	tlsSkipVerify, err := getEnvBool("PM_IMAP_TLS_SKIP_VERIFY", tlsSkipVerifyDefault)
	if err != nil {
		return Config{}, err
	}
	switch security {
	case IMAPSecurityStartTLS, IMAPSecurityTLS, IMAPSecurityInsecure:
	default:
		return Config{}, fmt.Errorf("PM_IMAP_SECURITY must be one of starttls,tls,insecure (got %q)", security)
	}

	if tlsSkipVerify && tlsSkipVerifyDefault {
		termio.Info("IMAP host %q is loopback; TLS certificate verification disabled by default (set PM_IMAP_TLS_SKIP_VERIFY=false to verify)", host)
	} else if tlsSkipVerify {
		termio.Info("TLS certificate verification disabled (PM_IMAP_TLS_SKIP_VERIFY=true)")
	}

	termio.Info("IMAP config: host=%s port=%d security=%s tls_skip_verify=%v username=%s", host, port, security, tlsSkipVerify, username)

	ollamaBaseURL := getEnvDefault("PM_OLLAMA_BASE_URL", "http://localhost:11434")
	ollamaModel := getEnvDefault("PM_OLLAMA_MODEL", "gemma4")

	return Config{
		IMAP: IMAPConfig{
			Host:          host,
			Port:          port,
			Username:      username,
			Password:      password,
			Security:      security,
			TLSSkipVerify: tlsSkipVerify,
		},
		Ollama: OllamaConfig{
			BaseURL: ollamaBaseURL,
			Model:   ollamaModel,
		},
	}, nil
}

// resolveCredentials looks up IMAP credentials from (in priority order):
//  1. the process environment (PM_IMAP_USERNAME / PM_IMAP_PASSWORD), and
//  2. the default keystore (OS keyring or encrypted file).
//
// Environment variables always win — they're the documented override. If only
// one half is set in env we still fall back to the keystore for the other
// half. The third return value names the source used (`env`, `keystore`, or
// `env+keystore` for a mixed resolve).
func resolveCredentials() (username, password, source string) {
	envUser := strings.TrimSpace(os.Getenv("PM_IMAP_USERNAME"))
	envPass := os.Getenv("PM_IMAP_PASSWORD")

	if envUser != "" && envPass != "" {
		return envUser, envPass, "env"
	}

	// PM_DISABLE_KEYSTORE is an escape hatch for hermetic test environments
	// and CI — it skips the keystore lookup entirely so the CLI relies on
	// env vars only.
	if b, _ := parseBool(os.Getenv("PM_DISABLE_KEYSTORE")); b {
		if envUser != "" || envPass != "" {
			return envUser, envPass, "env"
		}
		return "", "", ""
	}

	ksUser, ksPass, ksOK := loadKeystoreCreds()

	user := envUser
	pass := envPass
	if user == "" && ksOK {
		user = ksUser
	}
	if pass == "" && ksOK {
		pass = ksPass
	}

	switch {
	case envUser != "" && envPass == "" && ksOK:
		return user, pass, "env+keystore"
	case envPass != "" && envUser == "" && ksOK:
		return user, pass, "env+keystore"
	case envUser != "" && envPass != "":
		return user, pass, "env"
	case ksOK:
		return user, pass, "keystore"
	default:
		return "", "", ""
	}
}

// loadKeystoreCreds reads credentials from the first available backend that
// actually holds a usable pair. A missing-credentials outcome is not an error
// — only genuine backend failures are logged to stderr. Walks all candidates
// so users who drop creds into the encrypted file fallback still resolve,
// even when the keyring backend is reachable but empty.
func loadKeystoreCreds() (string, string, bool) {
	for _, b := range keystore.Available() {
		if !b.Available() {
			continue
		}
		c, err := b.Get()
		if err != nil {
			if !errors.Is(err, keystore.ErrNotFound) {
				termio.Warn("keystore (%s) read failed: %s", b.Name(), err.Error())
			}
			continue
		}
		if c.Username == "" && c.Password == "" {
			continue
		}
		return c.Username, c.Password, true
	}
	return "", "", false
}

func getEnvDefault(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func getIMAPSecurity() (IMAPSecurity, error) {
	// Preferred: PM_IMAP_SECURITY.
	if v := strings.TrimSpace(os.Getenv("PM_IMAP_SECURITY")); v != "" {
		return IMAPSecurity(strings.ToLower(v)), nil
	}

	// Back-compat with the plan’s original env var name.
	//
	// PM_IMAP_TLS can be:
	// - starttls|tls|insecure
	// - true/false (true => tls, false => starttls)
	if v := strings.TrimSpace(os.Getenv("PM_IMAP_TLS")); v != "" {
		vl := strings.ToLower(v)
		switch vl {
		case string(IMAPSecurityStartTLS), string(IMAPSecurityTLS), string(IMAPSecurityInsecure):
			return IMAPSecurity(vl), nil
		}
		b, err := parseBool(v)
		if err != nil {
			return "", fmt.Errorf("PM_IMAP_TLS must be starttls,tls,insecure,true,false (got %q)", v)
		}
		if b {
			return IMAPSecurityTLS, nil
		}
		return IMAPSecurityStartTLS, nil
	}

	return IMAPSecurityStartTLS, nil
}

func getEnvBool(key string, def bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	b, err := parseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean (true/false/1/0): %w", key, err)
	}
	return b, nil
}

func parseBool(v string) (bool, error) {
	return strconv.ParseBool(strings.TrimSpace(v))
}

func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
