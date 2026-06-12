// Package engine resolves the current State via the degrade ladder.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/creds"
	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/seosd97/cc-token-exposer/internal/usage"
)

const DefaultTTL = 120 * time.Second

var ErrNoCache = errors.New("engine: no cached snapshot")

type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type CredResolver interface {
	Resolve() (*creds.Credentials, error)
}

type Fetcher interface {
	Fetch(ctx context.Context, token string) (*usage.Snapshot, error)
}

type Cache interface {
	Load() (payload []byte, storedAt time.Time, err error)
	Store(payload []byte, storedAt time.Time) error
}

type TranscriptProbe interface {
	Probe(now time.Time) (*schema.LimitHit, error)
}

type Options struct {
	Creds      CredResolver
	Fetcher    Fetcher
	Cache      Cache
	Transcript TranscriptProbe
	Clock      Clock
	TTL        time.Duration
}

type Engine struct {
	creds      CredResolver
	fetcher    Fetcher
	cache      Cache
	transcript TranscriptProbe
	clock      Clock
	ttl        time.Duration
}

func New(o Options) *Engine {
	clock := o.Clock
	if clock == nil {
		clock = realClock{}
	}
	ttl := o.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Engine{
		creds:      o.Creds,
		fetcher:    o.Fetcher,
		cache:      o.Cache,
		transcript: o.Transcript,
		clock:      clock,
		ttl:        ttl,
	}
}

func (e *Engine) Resolve(ctx context.Context) *schema.State {
	now := e.clock.Now()
	cachedSnap, storedAt, haveCache := e.loadCache()

	if haveCache {
		if age := now.Sub(storedAt); age >= 0 && age < e.ttl {
			return snapshotState(cachedSnap, schema.SourceCache, false, 0, schema.AuthOK)
		}
	}

	cr, cerr := e.creds.Resolve()
	if cerr != nil || cr == nil || cr.AccessToken == "" {
		return e.degradeNoCreds(now, cachedSnap, storedAt, haveCache)
	}

	if cr.Expired(now) {
		if cr2, err := e.creds.Resolve(); err == nil && cr2 != nil && !cr2.Expired(now) {
			cr = cr2
		} else {
			return e.degradeAuthExpired(now, cachedSnap, storedAt, haveCache)
		}
	}

	snap, ferr := e.fetcher.Fetch(ctx, cr.AccessToken)
	if ferr == nil {
		merged := usage.Reconcile(cachedSnap, snap, now)
		e.storeCache(merged)
		return snapshotState(merged, schema.SourceOAuth, false, 0, schema.AuthOK)
	}

	if errors.Is(ferr, usage.ErrAuth) {
		if cr2, err := e.creds.Resolve(); err == nil && cr2 != nil &&
			cr2.AccessToken != "" && cr2.AccessToken != cr.AccessToken && !cr2.Expired(now) {
			if snap2, ferr2 := e.fetcher.Fetch(ctx, cr2.AccessToken); ferr2 == nil {
				merged := usage.Reconcile(cachedSnap, snap2, now)
				e.storeCache(merged)
				return snapshotState(merged, schema.SourceOAuth, false, 0, schema.AuthOK)
			}
		}
		return e.degradeAuthExpired(now, cachedSnap, storedAt, haveCache)
	}

	if haveCache {
		return snapshotState(cachedSnap, schema.SourceCache, true, now.Sub(storedAt), schema.AuthOK)
	}
	return e.degradeNoData(now, schema.AuthOK, "usage fetch failed and no cache is available")
}

func (e *Engine) degradeNoCreds(now time.Time, cachedSnap *usage.Snapshot, storedAt time.Time, haveCache bool) *schema.State {
	if haveCache {
		return snapshotState(cachedSnap, schema.SourceCache, true, now.Sub(storedAt), schema.AuthMissing)
	}
	if lh := e.probeTranscript(now); lh != nil {
		return transcriptState(lh, schema.AuthMissing)
	}
	return errorState(schema.AuthMissing, "no credentials found; run `claude` to log in")
}

func (e *Engine) degradeAuthExpired(now time.Time, cachedSnap *usage.Snapshot, storedAt time.Time, haveCache bool) *schema.State {
	if haveCache {
		return snapshotState(cachedSnap, schema.SourceCache, true, now.Sub(storedAt), schema.AuthExpired)
	}
	if lh := e.probeTranscript(now); lh != nil {
		return transcriptState(lh, schema.AuthExpired)
	}
	return errorState(schema.AuthExpired, "token expired; run `claude` to refresh it")
}

func (e *Engine) degradeNoData(now time.Time, auth schema.AuthStatus, msg string) *schema.State {
	if lh := e.probeTranscript(now); lh != nil {
		return transcriptState(lh, auth)
	}
	return errorState(auth, msg)
}

func (e *Engine) probeTranscript(now time.Time) *schema.LimitHit {
	if e.transcript == nil {
		return nil
	}
	lh, err := e.transcript.Probe(now)
	if err != nil {
		return nil
	}
	return lh
}

func (e *Engine) loadCache() (*usage.Snapshot, time.Time, bool) {
	if e.cache == nil {
		return nil, time.Time{}, false
	}
	payload, storedAt, err := e.cache.Load()
	if err != nil || len(payload) == 0 {
		return nil, time.Time{}, false
	}
	var snap usage.Snapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return nil, time.Time{}, false
	}
	return &snap, storedAt, true
}

func (e *Engine) storeCache(snap *usage.Snapshot) {
	if e.cache == nil || snap == nil {
		return
	}
	payload, err := json.Marshal(snap)
	if err != nil {
		return
	}
	when := snap.FetchedAt
	if when.IsZero() {
		when = e.clock.Now()
	}
	_ = e.cache.Store(payload, when)
}
