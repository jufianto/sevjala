package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	KeyringPrefix  = "keyring:"
	keyringService = "sevjala"
)

var ErrNoSecret = errors.New("the database password is not saved on this PC; save it with: sevjala db set --password")

// SetSecret stores value in the OS keyring. Linux without a Secret Service
// (a systemd service has no D-Bus session) falls back to an AES-GCM file
// keyed from a root-only key file, and reports fallback so the caller warns.
func SetSecret(dataDir, name, value string) (fallback bool, err error) {
	kerr := keyring.Set(keyringService, name, value)
	if kerr == nil {
		_ = os.Remove(secretFile(dataDir, name))
		return false, nil
	}
	if runtime.GOOS != "linux" {
		return false, fmt.Errorf("cannot save the database password in the system keyring: %w", kerr)
	}
	if err := writeSecretFile(dataDir, name, value); err != nil {
		return true, errors.Join(fmt.Errorf("cannot save the database password in the system keyring: %w", kerr), err)
	}
	return true, nil
}

// Secret resolves a password_ref; an empty ref is an empty password.
func Secret(dataDir, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	name, ok := strings.CutPrefix(ref, KeyringPrefix)
	if !ok || name == "" {
		return "", fmt.Errorf("password_ref %q: must be %q followed by a name", ref, KeyringPrefix)
	}
	v, kerr := keyring.Get(keyringService, name)
	if kerr == nil {
		return v, nil
	}
	v, ferr := readSecretFile(dataDir, name)
	switch {
	case ferr == nil:
		return v, nil
	case !errors.Is(ferr, fs.ErrNotExist):
		return "", ferr
	case errors.Is(kerr, keyring.ErrNotFound):
		return "", ErrNoSecret
	}
	return "", readError(kerr)
}

func DeleteSecret(dataDir, name string) error {
	kerr := keyring.Delete(keyringService, name)
	if errors.Is(kerr, keyring.ErrNotFound) || runtime.GOOS == "linux" {
		kerr = nil
	}
	ferr := os.Remove(secretFile(dataDir, name))
	if errors.Is(ferr, fs.ErrNotExist) {
		ferr = nil
	}
	return errors.Join(kerr, ferr)
}

func secretDir(dataDir string) string { return filepath.Join(dataDir, "secrets") }

func secretFile(dataDir, name string) string { return filepath.Join(secretDir(dataDir), name+".enc") }

func fileKey(dataDir string) ([]byte, error) {
	path := filepath.Join(secretDir(dataDir), "key")
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("%s: want 32 bytes, got %d", path, len(key))
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	_, _ = rand.Read(key)
	if err := os.MkdirAll(secretDir(dataDir), 0o700); err != nil {
		return nil, err
	}
	return key, writeAtomic(path, key, 0o600)
}

func aead(dataDir string) (cipher.AEAD, error) {
	key, err := fileKey(dataDir)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func writeSecretFile(dataDir, name, value string) error {
	a, err := aead(dataDir)
	if err != nil {
		return err
	}
	nonce := make([]byte, a.NonceSize())
	_, _ = rand.Read(nonce)
	return writeAtomic(secretFile(dataDir, name), a.Seal(nonce, nonce, []byte(value), []byte(name)), 0o600)
}

func readSecretFile(dataDir, name string) (string, error) {
	path := secretFile(dataDir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	a, err := aead(dataDir)
	if err != nil {
		return "", err
	}
	if len(raw) < a.NonceSize() {
		return "", fmt.Errorf("%s: truncated", path)
	}
	plain, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], []byte(name))
	if err != nil {
		return "", fmt.Errorf("%s: cannot decrypt (was secrets/key replaced?)", path)
	}
	return string(plain), nil
}

type keyringError struct {
	msg string
	err error
}

func (e *keyringError) Error() string { return e.msg }
func (e *keyringError) Unwrap() error { return e.err }

func readError(kerr error) error {
	inner := kerr
	for u := errors.Unwrap(inner); u != nil; u = errors.Unwrap(inner) {
		inner = u
	}
	return &keyringError{"cannot read the saved database password from the system keyring: " + inner.Error(), kerr}
}
