// Package imapclient dials Proton Mail Bridge over IMAP and normalizes
// protocol errors into structured CLI errors.
package imapclient

import (
	"errors"
	"fmt"
	"strings"
)

// ErrConnection is a sentinel wrapped around the underlying error whenever a
// heuristic classifies it as a connection-loss condition.
var ErrConnection = errors.New("imap: connection error")

var connectionSubstrings = []string{
	"connection closed",
	"i/o timeout",
	"connection reset",
	"EOF",
	"use of closed network connection",
	"Not logged in",
	"not logged in",
	"broken pipe",
}

// IsConnectionError reports whether err (or anything it wraps) is a connection
// error. It first checks errors.Is for ErrConnection, then falls back to
// substring matching for go-imap's untyped errors.
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrConnection) {
		return true
	}
	s := err.Error()
	for _, sub := range connectionSubstrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// WrapIfConnection returns err wrapped as ErrConnection when it matches the
// connection-loss heuristic; otherwise it returns err unchanged. nil passes
// through.
func WrapIfConnection(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrConnection) {
		return err
	}
	if !IsConnectionError(err) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrConnection, err)
}

// Wrap is the call-site helper: applied to results of List/Select/Fetch/Search/
// Copy/Store/Expunge so that callers can branch on IsConnectionError.
func Wrap(err error) error { return WrapIfConnection(err) }
