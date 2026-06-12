// Package creds resolves Claude Code OAuth credentials for the usage API.
package creds

import (
	"errors"
	"fmt"
	"time"
)

var ErrNotAvailable = errors.New("creds: source not available")

var ErrNotFound = errors.New("creds: no credentials found in any source")

type Credentials struct {
	AccessToken string
	ExpiresAt   time.Time
	SourceName  string
}

func (c *Credentials) Expired(now time.Time) bool {
	if c == nil {
		return true
	}
	if c.ExpiresAt.IsZero() {
		return false
	}
	return !now.Before(c.ExpiresAt)
}

func (c *Credentials) String() string {
	if c == nil {
		return "<nil credentials>"
	}
	exp := "unknown"
	if !c.ExpiresAt.IsZero() {
		exp = c.ExpiresAt.Format(time.RFC3339)
	}
	return fmt.Sprintf("Credentials{source=%s, token=<redacted>, expires=%s}", c.SourceName, exp)
}

func (c *Credentials) GoString() string { return c.String() }

type Source interface {
	Name() string
	Load() (*Credentials, error)
}

type Resolver struct {
	Sources []Source
}

func NewResolver(sources ...Source) *Resolver {
	return &Resolver{Sources: sources}
}

func (r *Resolver) Resolve() (*Credentials, error) {
	var firstErr error
	for _, s := range r.Sources {
		c, err := s.Load()
		if err != nil {
			if errors.Is(err, ErrNotAvailable) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if c == nil || c.AccessToken == "" {
			continue
		}
		if c.SourceName == "" {
			c.SourceName = s.Name()
		}
		return c, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, ErrNotFound
}
