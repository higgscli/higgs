package keystore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

const (
	// fileFormatVersion is the first byte of the encrypted file. Bumping this
	// constant invalidates older files by design.
	fileFormatVersion byte = 0x01

	// Argon2id parameters — kept in one place so the test can reference them.
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024 // 64 MiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32

	saltLen  = 16
	nonceLen = 12
)

// fileBackend stores credentials in an encrypted JSON blob on disk.
//
// File layout:
//
//	[1-byte version][16-byte salt][12-byte nonce][ciphertext+GCM tag]
//
// The encryption key is derived from PM_KEYSTORE_PASSPHRASE using Argon2id
// (IDKey, 1 iteration, 64 MiB, 4 threads, 32-byte key) over the file salt.
//
// The directory is created with 0700 and the file with 0600.
type fileBackend struct{}

func newFileBackend() *fileBackend { return &fileBackend{} }

// Name satisfies Backend.
func (f *fileBackend) Name() string { return "encrypted file" }

// Path returns the resolved credential file path. PM_KEYSTORE_PATH overrides the
// default (~/.protoncli/credentials.enc). If HOME cannot be determined, Path
// returns an empty string.
func (f *fileBackend) Path() string {
	if p := os.Getenv("PM_KEYSTORE_PATH"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".protoncli", "credentials.enc")
}

// Available returns true when the backend is usable — that is, when the
// passphrase env var is set (meaning we can write or read), or when the file
// already exists on disk (meaning `logout` and `status` can still act on it
// even without the passphrase).
func (f *fileBackend) Available() bool {
	if os.Getenv("PM_KEYSTORE_PASSPHRASE") != "" {
		return true
	}
	p := f.Path()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// Get loads and decrypts the credential file.
func (f *fileBackend) Get() (Credentials, error) {
	p := f.Path()
	if p == "" {
		return Credentials{}, fmt.Errorf("keystore/file: cannot determine credentials path (HOME not set)")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Credentials{}, ErrNotFound
		}
		return Credentials{}, err
	}
	if len(raw) < 1+saltLen+nonceLen+16 {
		return Credentials{}, fmt.Errorf("keystore/file: credential file is truncated or corrupt")
	}
	if raw[0] != fileFormatVersion {
		return Credentials{}, fmt.Errorf("keystore/file: unsupported file version 0x%02x (expected 0x%02x)", raw[0], fileFormatVersion)
	}

	passphrase, err := requirePassphrase()
	if err != nil {
		return Credentials{}, err
	}

	salt := raw[1 : 1+saltLen]
	nonce := raw[1+saltLen : 1+saltLen+nonceLen]
	ciphertext := raw[1+saltLen+nonceLen:]

	key := deriveKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return Credentials{}, fmt.Errorf("keystore/file: aes init: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Credentials{}, fmt.Errorf("keystore/file: gcm init: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte{fileFormatVersion})
	if err != nil {
		return Credentials{}, fmt.Errorf("keystore/file: decrypt failed (wrong passphrase or tampered file)")
	}

	var c Credentials
	if err := json.Unmarshal(plaintext, &c); err != nil {
		return Credentials{}, fmt.Errorf("keystore/file: decode plaintext: %w", err)
	}
	return c, nil
}

// Set encrypts credentials and atomically writes the credential file.
func (f *fileBackend) Set(c Credentials) error {
	p := f.Path()
	if p == "" {
		return fmt.Errorf("keystore/file: cannot determine credentials path (HOME not set)")
	}
	passphrase, err := requirePassphrase()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("keystore/file: mkdir: %w", err)
	}

	// #nosec G117 -- credentials are serialized only to be AES-256-GCM encrypted on disk.
	plaintext, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("keystore/file: encode credentials: %w", err)
	}

	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("keystore/file: rand salt: %w", err)
	}
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("keystore/file: rand nonce: %w", err)
	}

	key := deriveKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("keystore/file: aes init: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("keystore/file: gcm init: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte{fileFormatVersion})

	out := make([]byte, 0, 1+saltLen+nonceLen+len(ciphertext))
	out = append(out, fileFormatVersion)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)

	// Atomic replace: write to tmp file in the same dir, then rename.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("keystore/file: create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("keystore/file: chmod tmp: %w", err)
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("keystore/file: write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("keystore/file: close tmp: %w", err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		cleanup()
		return fmt.Errorf("keystore/file: rename: %w", err)
	}
	return nil
}

// Delete removes the credential file. A missing file is not an error.
func (f *fileBackend) Delete() error {
	p := f.Path()
	if p == "" {
		return nil
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("keystore/file: remove: %w", err)
	}
	return nil
}

func requirePassphrase() (string, error) {
	p := os.Getenv("PM_KEYSTORE_PASSPHRASE")
	if p == "" {
		return "", fmt.Errorf("keystore/file: PM_KEYSTORE_PASSPHRASE is not set")
	}
	return p, nil
}

func deriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
}
