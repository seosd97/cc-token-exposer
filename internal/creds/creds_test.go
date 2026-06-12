package creds

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeSource is a test double for Source.
type fakeSource struct {
	name string
	c    *Credentials
	err  error
}

func (f *fakeSource) Name() string                { return f.name }
func (f *fakeSource) Load() (*Credentials, error) { return f.c, f.err }

func TestCredentialsExpired(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cases := []struct {
		name string
		c    *Credentials
		want bool
	}{
		{"nil is expired", nil, true},
		{"zero expiry unknown -> not expired", &Credentials{AccessToken: "x"}, false},
		{"future -> not expired", &Credentials{ExpiresAt: now.Add(time.Hour)}, false},
		{"past -> expired", &Credentials{ExpiresAt: now.Add(-time.Hour)}, true},
		{"exactly now -> expired", &Credentials{ExpiresAt: now}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Expired(now); got != tc.want {
				t.Fatalf("Expired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCredentialsStringRedactsToken(t *testing.T) {
	c := &Credentials{AccessToken: "super-secret-token", SourceName: "file"}
	for _, s := range []string{c.String(), c.GoString()} {
		if strings.Contains(s, "super-secret-token") {
			t.Fatalf("String leaked token: %q", s)
		}
		if !strings.Contains(s, "redacted") {
			t.Fatalf("String missing redaction marker: %q", s)
		}
	}
}

func TestResolverReturnsFirstAvailable(t *testing.T) {
	r := NewResolver(
		&fakeSource{name: "injected", err: ErrNotAvailable},
		&fakeSource{name: "file", c: &Credentials{AccessToken: "tok"}},
		&fakeSource{name: "keychain", c: &Credentials{AccessToken: "should-not-reach"}},
	)
	c, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if c.AccessToken != "tok" {
		t.Fatalf("token = %q, want tok", c.AccessToken)
	}
	if c.SourceName != "file" {
		t.Fatalf("SourceName = %q, want file", c.SourceName)
	}
}

func TestResolverSkipsEmptyToken(t *testing.T) {
	r := NewResolver(
		&fakeSource{name: "injected", c: &Credentials{AccessToken: ""}},
		&fakeSource{name: "file", c: &Credentials{AccessToken: "real"}},
	)
	c, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if c.AccessToken != "real" {
		t.Fatalf("token = %q, want real", c.AccessToken)
	}
}

func TestResolverNotFound(t *testing.T) {
	r := NewResolver(
		&fakeSource{name: "injected", err: ErrNotAvailable},
		&fakeSource{name: "file", err: ErrNotAvailable},
	)
	_, err := r.Resolve()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolverSurfacesFirstRealError(t *testing.T) {
	boom := errors.New("malformed file")
	r := NewResolver(
		&fakeSource{name: "file", err: boom},
		&fakeSource{name: "keychain", err: ErrNotAvailable},
	)
	_, err := r.Resolve()
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestResolverRealErrorButLaterSuccess(t *testing.T) {
	r := NewResolver(
		&fakeSource{name: "file", err: errors.New("malformed file")},
		&fakeSource{name: "keychain", c: &Credentials{AccessToken: "recovered"}},
	)
	c, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if c.AccessToken != "recovered" {
		t.Fatalf("token = %q, want recovered", c.AccessToken)
	}
}
