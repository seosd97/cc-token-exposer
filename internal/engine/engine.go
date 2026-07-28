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

const stdinRefreshTimeout = 5 * time.Second

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
	Load() (payload []byte, storedAt time.Time, attemptedAt time.Time, err error)
	Store(payload []byte, storedAt time.Time) error
	Touch(attemptedAt time.Time) error
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
	cachedSnap, storedAt, _, haveCache := e.loadCache()

	if haveCache {
		if age := now.Sub(storedAt); age >= 0 && age < e.ttl {
			return snapshotState(cachedSnap, schema.SourceCache, false, 0, schema.AuthOK)
		}
	}

	cr, auth := e.resolveToken(now)
	if cr == nil {
		if auth == schema.AuthMissing {
			return e.degradeNoCreds(now, cachedSnap, storedAt, haveCache)
		}
		return e.degradeAuthExpired(now, cachedSnap, storedAt, haveCache)
	}

	snap, ferr := e.fetchWithToken(ctx, cr, now)
	if ferr == nil {
		merged := usage.Reconcile(cachedSnap, snap, now)
		e.storeCache(merged)
		return snapshotState(merged, schema.SourceOAuth, false, 0, schema.AuthOK)
	}

	if errors.Is(ferr, usage.ErrAuth) {
		return e.degradeAuthExpired(now, cachedSnap, storedAt, haveCache)
	}

	if haveCache {
		return snapshotState(cachedSnap, schema.SourceCache, true, now.Sub(storedAt), schema.AuthOK)
	}
	return e.degradeNoData(now, schema.AuthOK, "usage fetch failed and no cache is available")
}

// resolveToken resolves usable credentials, re-reading once when the first read
// is expired. Returns (nil, AuthMissing) with no token and (nil, AuthExpired)
// when an expired token can't be replaced by a re-read.
func (e *Engine) resolveToken(now time.Time) (*creds.Credentials, schema.AuthStatus) {
	cr, err := e.creds.Resolve()
	if err != nil || cr == nil || cr.AccessToken == "" {
		return nil, schema.AuthMissing
	}
	if cr.Expired(now) {
		if cr2, err := e.creds.Resolve(); err == nil && cr2 != nil && !cr2.Expired(now) {
			return cr2, schema.AuthOK
		}
		return nil, schema.AuthExpired
	}
	return cr, schema.AuthOK
}

// fetchWithToken fetches a snapshot, retrying once with a fresh token on a 401
// if Claude Code has rotated it meanwhile. It returns the original error when
// the retry is not applicable or also fails.
func (e *Engine) fetchWithToken(ctx context.Context, cr *creds.Credentials, now time.Time) (*usage.Snapshot, error) {
	snap, err := e.fetcher.Fetch(ctx, cr.AccessToken)
	if err == nil || !errors.Is(err, usage.ErrAuth) {
		return snap, err
	}
	if cr2, err := e.creds.Resolve(); err == nil && cr2 != nil &&
		cr2.AccessToken != "" && cr2.AccessToken != cr.AccessToken && !cr2.Expired(now) {
		if snap2, err := e.fetcher.Fetch(ctx, cr2.AccessToken); err == nil {
			return snap2, nil
		}
	}
	return snap, err
}

// ResolveStdin serves a snapshot piped in by Claude Code's statusline, overlaid
// on the disk cache. When stdin is incomplete and no refresh has run within the
// TTL it does one bounded refresh (reusing resolveToken/fetchWithToken +
// Reconcile + storeCache) to heal the gaps, then re-overlays; a failed refresh
// records its attempt via Cache.Touch so the ≤1/TTL budget holds even under a
// failing endpoint, and the cache-backed merge is served marked stale. No
// suspect guard runs here — the statusline mirrors Claude Code's own values;
// the guard and its marker live on the ladder.
func (e *Engine) ResolveStdin(ctx context.Context, stdin *usage.Snapshot) *schema.State {
	if stdin == nil {
		return e.Resolve(ctx)
	}
	now := e.clock.Now()
	cachedSnap, storedAt, attemptedAt, haveCache := e.loadCache()
	dataStale := !haveCache || now.Sub(storedAt) >= e.ttl
	lastAttempt := storedAt
	if attemptedAt.After(lastAttempt) {
		lastAttempt = attemptedAt
	}
	if e.refreshDue(now, lastAttempt) && !stdinComplete(stdin) {
		if fresh := e.refreshSnapshot(ctx, now, cachedSnap); fresh != nil {
			cachedSnap = fresh
			dataStale = false
		} else if e.cache != nil {
			_ = e.cache.Touch(now)
		}
	}
	merged, usedCache := usage.Overlay(stdin, cachedSnap)
	stale := usedCache && dataStale
	var age time.Duration
	if stale {
		age = now.Sub(storedAt)
	}
	return snapshotState(merged, schema.SourceStdin, stale, age, schema.AuthOK)
}

func (e *Engine) refreshDue(now, lastAttempt time.Time) bool {
	return lastAttempt.IsZero() || now.Sub(lastAttempt) >= e.ttl
}

// refreshSnapshot performs a best-effort fresh fetch, reconciles it against the
// cached snapshot, stores the result, and returns it. Returns nil on any
// failure (missing ports, no usable credentials, fetch error).
func (e *Engine) refreshSnapshot(ctx context.Context, now time.Time, cachedSnap *usage.Snapshot) *usage.Snapshot {
	if e.creds == nil || e.fetcher == nil {
		return nil
	}
	cr, _ := e.resolveToken(now)
	if cr == nil {
		return nil
	}
	rctx, cancel := context.WithTimeout(ctx, stdinRefreshTimeout)
	defer cancel()
	snap, err := e.fetchWithToken(rctx, cr, now)
	if err != nil {
		return nil
	}
	merged := usage.Reconcile(cachedSnap, snap, now)
	e.storeCache(merged)
	return merged
}

func stdinComplete(s *usage.Snapshot) bool {
	return s != nil && s.FiveHour != nil && s.SevenDay != nil && len(s.ScopedLimits) > 0
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

func (e *Engine) loadCache() (*usage.Snapshot, time.Time, time.Time, bool) {
	if e.cache == nil {
		return nil, time.Time{}, time.Time{}, false
	}
	payload, storedAt, attemptedAt, err := e.cache.Load()
	if err != nil || len(payload) == 0 {
		return nil, time.Time{}, attemptedAt, false
	}
	var snap usage.Snapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return nil, time.Time{}, attemptedAt, false
	}
	if snap.FiveHour == nil && snap.SevenDay == nil && len(snap.ScopedLimits) == 0 {
		return nil, storedAt, attemptedAt, false
	}
	return &snap, storedAt, attemptedAt, true
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
