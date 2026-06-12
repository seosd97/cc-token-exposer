package creds

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeCredsFile writes a fixture credentials file with 0600 perms.
func writeCredsFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestFileSourceValid(t *testing.T) {
	// expiresAt = 2026-06-12T12:00:00Z in epoch millis.
	expMs := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC).UnixMilli()
	content := `{"claudeAiOauth":{"accessToken":"tok-abc","refreshToken":"rt-ignored","expiresAt":` +
		strconv.FormatInt(expMs, 10) + `,"scopes":["a"],"subscriptionType":"max"}}`
	path := writeCredsFile(t, content)

	src := &FileSource{Path: path}
	c, err := src.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AccessToken != "tok-abc" {
		t.Fatalf("token = %q", c.AccessToken)
	}
	if !c.ExpiresAt.Equal(time.UnixMilli(expMs)) {
		t.Fatalf("expiresAt = %v, want %v", c.ExpiresAt, time.UnixMilli(expMs))
	}
	if c.SourceName != "file" {
		t.Fatalf("SourceName = %q", c.SourceName)
	}
}

func TestFileSourceMissing(t *testing.T) {
	src := &FileSource{Path: filepath.Join(t.TempDir(), "nope.json")}
	_, err := src.Load()
	if !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("err = %v, want ErrNotAvailable", err)
	}
}

func TestFileSourceMalformed(t *testing.T) {
	path := writeCredsFile(t, `{not json`)
	src := &FileSource{Path: path}
	_, err := src.Load()
	if err == nil || errors.Is(err, ErrNotAvailable) {
		t.Fatalf("want parse error, got %v", err)
	}
}

func TestFileSourceNoToken(t *testing.T) {
	path := writeCredsFile(t, `{"claudeAiOauth":{"accessToken":"","expiresAt":123}}`)
	src := &FileSource{Path: path}
	_, err := src.Load()
	if !errors.Is(err, errNoToken) {
		t.Fatalf("err = %v, want errNoToken", err)
	}
}

func TestFileSourceErrorDoesNotLeakToken(t *testing.T) {
	// A type mismatch on a non-token field should still parse the structure
	// without exposing the token; ensure error text carries no token.
	path := writeCredsFile(t, `{"claudeAiOauth":{"accessToken":"leaky-token","expiresAt":"not-a-number"}}`)
	src := &FileSource{Path: path}
	_, err := src.Load()
	if err == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(err.Error(), "leaky-token") {
		t.Fatalf("error leaked token: %v", err)
	}
}
