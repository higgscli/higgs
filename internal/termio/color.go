// Package termio writes structured JSON to stdout and sanitized human-readable
// progress to stderr.
package termio

import (
	"os"
)

// StderrSupportsColor reports whether stderr is a TTY and NO_COLOR is unset.
func StderrSupportsColor() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	stat, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

// Colorize wraps text with bold + ANSI color when supported and ansiCode is all ASCII digits.
func Colorize(text, ansiCode string) string {
	if !allASCIIDigits(ansiCode) {
		return text
	}
	if !StderrSupportsColor() {
		return text
	}
	return "\x1b[1;" + ansiCode + "m" + text + "\x1b[0m"
}

func allASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
