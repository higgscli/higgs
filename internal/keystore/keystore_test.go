package keystore

import (
	"errors"
	"testing"
)

func TestDefault_PrefersKeyringWhenAvailable(t *testing.T) {
	// Keyring is always "available" per our design. Default should return it.
	b := Default()
	if b.Name() != "system keyring" {
		t.Fatalf("default backend = %q, want %q", b.Name(), "system keyring")
	}
}

func TestAvailable_ReturnsAllCandidates(t *testing.T) {
	list := Available()
	if len(list) != 2 {
		t.Fatalf("expected 2 candidate backends, got %d", len(list))
	}
	if list[0].Name() != "system keyring" || list[1].Name() != "encrypted file" {
		t.Errorf("unexpected candidate order: %q, %q", list[0].Name(), list[1].Name())
	}
}

func TestNoneBackend_Operations(t *testing.T) {
	n := &noneBackend{}
	if n.Name() != "none" {
		t.Errorf("name = %q", n.Name())
	}
	if !n.Available() {
		t.Error("none should report Available=true (it is always usable as a fallthrough)")
	}
	if _, err := n.Get(); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get err = %v, want ErrNotFound", err)
	}
	if err := n.Set(Credentials{Username: "u", Password: "p"}); err == nil {
		t.Error("Set should error")
	}
	if err := n.Delete(); err == nil {
		t.Error("Delete should error")
	}
}

func TestServiceConstants(t *testing.T) {
	if ServiceName != "protoncli" {
		t.Errorf("ServiceName = %q", ServiceName)
	}
	if KeyringAccount != "imap" {
		t.Errorf("KeyringAccount = %q", KeyringAccount)
	}
}

// stubBackend lets Default() selection tests force Available() values.
type stubBackend struct {
	name  string
	avail bool
}

func (s *stubBackend) Get() (Credentials, error) { return Credentials{}, ErrNotFound }
func (s *stubBackend) Set(Credentials) error      { return nil }
func (s *stubBackend) Delete() error              { return nil }
func (s *stubBackend) Name() string               { return s.name }
func (s *stubBackend) Available() bool            { return s.avail }

func TestDefault_SkipsUnavailable(t *testing.T) {
	candidatesForTest = func() []Backend {
		return []Backend{&stubBackend{name: "first", avail: false}, &stubBackend{name: "second", avail: true}}
	}
	t.Cleanup(func() { candidatesForTest = nil })
	got := Default()
	if got.Name() != "second" {
		t.Errorf("Default() = %q, want %q", got.Name(), "second")
	}
}

func TestDefault_AllUnavailableReturnsNone(t *testing.T) {
	candidatesForTest = func() []Backend {
		return []Backend{&stubBackend{name: "a", avail: false}, &stubBackend{name: "b", avail: false}}
	}
	t.Cleanup(func() { candidatesForTest = nil })
	got := Default()
	if got.Name() != "none" {
		t.Errorf("Default() = %q, want none", got.Name())
	}
}
