package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

var (
	ErrAuth        = errors.New("provider: authentication failed")
	ErrRateLimited = errors.New("provider: rate limited")
	ErrTransient   = errors.New("provider: transient error")
)

type RateLimitError struct {
	RetryAfter time.Duration
	StatusCode int
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("provider: rate limited (retry after %s)", e.RetryAfter)
	}
	return "provider: rate limited"
}

func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

type FetchedSnapshot struct {
	Snapshot     *schema.Snapshot
	ScopedProbed bool
	Drift        []string
}

type CredResolver interface {
	Resolve() (*Credentials, error)
}

type Fetcher interface {
	Fetch(ctx context.Context, cr *Credentials) (*FetchedSnapshot, error)
}

type TranscriptProbe interface {
	Probe(now time.Time) (*schema.LimitHit, error)
}

type StdinParser func(doc []byte, now time.Time) *schema.Snapshot

type Spec struct {
	Name         string
	LoginCommand string
	Creds        CredResolver
	Fetcher      Fetcher
	Transcript   TranscriptProbe
	ParseStdin   StdinParser
}
