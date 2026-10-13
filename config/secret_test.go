package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestSecretRoundTrip(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	if fb, err := SetSecret(dir, "db", "p a$s\"wörd"); err != nil || fb {
		t.Fatalf("SetSecret = %v, %v", fb, err)
	}
	if got, err := Secret(dir, "keyring:db"); err != nil || got != "p a$s\"wörd" {
		t.Fatalf("Secret = %q, %v", got, err)
	}
	if err := DeleteSecret(dir, "db"); err != nil {
		t.Fatal(err)
	}
	if _, err := Secret(dir, "keyring:db"); !errors.Is(err, ErrNoSecret) {
		t.Errorf("after delete: %v", err)
	}
	if err := DeleteSecret(dir, "db"); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

func TestSecretRefs(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	if got, err := Secret(dir, ""); got != "" || err != nil {
		t.Errorf("empty ref = %q, %v", got, err)
	}
	if _, err := Secret(dir, "plain:db"); err == nil {
		t.Error("a ref without the keyring: prefix was accepted")
	}
}

// The Linux fallback, exercised directly so every OS runs it.
func TestSecretFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeSecretFile(dir, "db", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if got, err := readSecretFile(dir, "db"); err != nil || got != "s3cret" {
		t.Fatalf("read = %q, %v", got, err)
	}
	raw, _ := os.ReadFile(secretFile(dir, "db"))
	if bytes.Contains(raw, []byte("s3cret")) {
		t.Error("secret stored in clear")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(dir, "secrets", "key")); fi.Mode().Perm() != 0o600 {
			t.Errorf("key mode %v", fi.Mode().Perm())
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets", "key"), bytes.Repeat([]byte{7}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecretFile(dir, "db"); err == nil {
		t.Error("decrypted with another key")
	}
}

func TestSetSecretFallsBackOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the file fallback is Linux only")
	}
	keyring.MockInitWithError(errors.New("no D-Bus session"))
	dir := t.TempDir()
	fb, err := SetSecret(dir, "db", "s3cret")
	if err != nil || !fb {
		t.Fatalf("SetSecret = %v, %v", fb, err)
	}
	if got, err := Secret(dir, "keyring:db"); err != nil || got != "s3cret" {
		t.Errorf("Secret = %q, %v", got, err)
	}
}

func TestSecretErrorsArePlain(t *testing.T) {
	dir := t.TempDir()
	keyring.MockInit()
	_, err := Secret(dir, "keyring:db")
	if !errors.Is(err, ErrNoSecret) || err.Error() != "the database password is not saved on this PC; save it with: sevjala db set --password" {
		t.Errorf("missing secret: %v", err)
	}
	cause := errors.New("The specified service is not available")
	keyring.MockInitWithError(fmt.Errorf("wrapped: %w", cause))
	_, err = Secret(dir, "keyring:db")
	want := "cannot read the saved database password from the system keyring: " + cause.Error()
	if err == nil || err.Error() != want || !errors.Is(err, cause) {
		t.Errorf("keyring failure: %v", err)
	}
}
