//go:build darwin

package creds

import (
	"errors"
	"os/exec"
	"testing"
)

func TestKeychainParsesBlob(t *testing.T) {
	src := &KeychainSource{
		run: func(service string) ([]byte, error) {
			if service != keychainService {
				t.Fatalf("service = %q", service)
			}
			return []byte(`{"claudeAiOauth":{"accessToken":"kc-tok","expiresAt":42}}`), nil
		},
	}
	c, err := src.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AccessToken != "kc-tok" {
		t.Fatalf("token = %q", c.AccessToken)
	}
	if c.SourceName != "keychain" {
		t.Fatalf("source = %q", c.SourceName)
	}
}

func TestKeychainItemNotFound(t *testing.T) {
	// Produce a real *exec.ExitError with code 44.
	notFound := exec.Command("/bin/sh", "-c", "exit 44").Run()
	src := &KeychainSource{
		run: func(string) ([]byte, error) { return nil, notFound },
	}
	_, err := src.Load()
	if !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("err = %v, want ErrNotAvailable", err)
	}
}

func TestKeychainOtherErrorSurfaces(t *testing.T) {
	otherErr := exec.Command("/bin/sh", "-c", "exit 1").Run()
	src := &KeychainSource{
		run: func(string) ([]byte, error) { return nil, otherErr },
	}
	_, err := src.Load()
	if err == nil || errors.Is(err, ErrNotAvailable) {
		t.Fatalf("err = %v, want non-nil surfaced error", err)
	}
}
