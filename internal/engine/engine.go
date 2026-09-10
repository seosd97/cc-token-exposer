package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type Provider struct {
	Name         string
	LoginCommand string
}

var ClaudeProvider = Provider{Name: schema.ProviderClaude, LoginCommand: "claude"}

type CredResolver interface {
	Resolve() (*creds.Credentials, error)
}

type Fetcher interface {
	Fetch(ctx context.Context, cr *creds.Credentials) (*usage.FetchedSnapshot, error)
}

type CacheEntry struct {
	Payload      []byte
	StoredAt     time.Time
	AttemptedAt  time.Time
	ScopedProbed bool
	Drift        []string
}

type Cache interface {
	Load() (*CacheEntry, error)
	Store(e CacheEntry) error
	ClaimRefresh(now time.Time, backoff time.Duration) (bool, error)
}

type Refresher interface {
	Spawn(ctx context.Context) error
}

type TranscriptProbe interface {
	Probe(now time.Time) (*schema.LimitHit, error)
}

type Options struct {
	Provider   Provider
	Creds      CredResolver
	Fetcher    Fetcher
	Cache      Cache
	Transcript TranscriptProbe
	Refresher  Refresher
	Clock      Clock
	TTL        time.Duration
}

type Engine struct {
	provider   Provider
	creds      CredResolver
	fetcher    Fetcher
	cache      Cache
	transcript TranscriptProbe
	refresher  Refresher
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
	provider := o.Provider
	if provider.Name == "" {
		provider = ClaudeProvider
	}
	if provider.LoginCommand == "" {
		provider.LoginCommand = provider.Name
	}
	return &Engine{
		provider:   provider,
		creds:      o.Creds,
		fetcher:    o.Fetcher,
		cache:      o.Cache,
		transcript: o.Transcript,
		refresher:  o.Refresher,
		clock:      clock,
		ttl:        ttl,
	}
}

func (e *Engine) Resolve(ctx context.Context) *schema.State {
	return e.tag(e.resolve(ctx))
}

func (e *Engine) ResolveStdin(ctx context.Context, stdin *schema.Snapshot) *schema.State {
	return e.tag(e.resolveStdin(ctx, stdin))
}

func (e *Engine) tag(st *schema.State) *schema.State {
	st.Provider = e.provider.Name
	return st
}

func (e *Engine) resolve(ctx context.Context) *schema.State {
	now := e.clock.Now()
	cachedSnap, meta, haveCache := e.loadCache()

	if haveCache {
		if age := now.Sub(meta.StoredAt); age >= 0 && age < e.ttl {
			st := snapshotState(cachedSnap, schema.SourceCache, false, 0, schema.AuthOK)
			st.Drift = meta.Drift
			return st
		}
	}

	cr, auth, cerr := e.resolveToken(now)
	if cr == nil {
		return e.degradeAuth(now, auth, credsReason(cerr), cachedSnap, meta, haveCache)
	}

	snap, ferr := e.fetchWithToken(ctx, cr, now)
	if ferr == nil {
		merged := usage.Reconcile(cachedSnap, snap.Snapshot, now)
		e.storeCache(merged, snap.ScopedProbed, snap.Drift)
		st := snapshotState(merged, schema.SourceOAuth, false, 0, schema.AuthOK)
		st.Drift = snap.Drift
		return st
	}

	if errors.Is(ferr, usage.ErrAuth) {
		return e.degradeAuth(now, schema.AuthExpired, "", cachedSnap, meta, haveCache)
	}

	if haveCache {
		st := snapshotState(cachedSnap, schema.SourceCache, true, now.Sub(meta.StoredAt), schema.AuthOK)
		st.Drift = meta.Drift
		return st
	}
	return e.degradeNoData(now, schema.AuthOK, "usage fetch failed and no cache is available")
}

func (e *Engine) resolveToken(now time.Time) (*creds.Credentials, schema.AuthStatus, error) {
	if e.creds == nil {
		return nil, schema.AuthMissing, nil
	}
	cr, err := e.creds.Resolve()
	if err != nil || cr == nil || cr.AccessToken == "" {
		return nil, schema.AuthMissing, err
	}
	if cr.Expired(now) {
		if cr2, err := e.creds.Resolve(); err == nil && cr2 != nil && cr2.AccessToken != "" && !cr2.Expired(now) {
			return cr2, schema.AuthOK, nil
		}
		return nil, schema.AuthExpired, nil
	}
	return cr, schema.AuthOK, nil
}

func credsReason(err error) string {
	if err == nil || errors.Is(err, creds.ErrNotFound) || errors.Is(err, creds.ErrNotAvailable) {
		return ""
	}
	return err.Error()
}

func (e *Engine) fetchWithToken(ctx context.Context, cr *creds.Credentials, now time.Time) (*usage.FetchedSnapshot, error) {
	if e.fetcher == nil {
		return nil, fmt.Errorf("%w: no fetcher configured", usage.ErrTransient)
	}
	snap, err := e.fetcher.Fetch(ctx, cr)
	if err == nil || !errors.Is(err, usage.ErrAuth) {
		return snap, err
	}
	if cr2, err := e.creds.Resolve(); err == nil && cr2 != nil &&
		cr2.AccessToken != "" && cr2.AccessToken != cr.AccessToken && !cr2.Expired(now) {
		if snap2, err := e.fetcher.Fetch(ctx, cr2); err == nil {
			return snap2, nil
		}
	}
	return snap, err
}

func (e *Engine) resolveStdin(ctx context.Context, stdin *schema.Snapshot) *schema.State {
	if stdin == nil {
		return e.resolve(ctx)
	}
	now := e.clock.Now()
	cachedSnap, meta, haveCache := e.loadCache()
	dataStale := !haveCache || now.Sub(meta.StoredAt) >= e.ttl
	if !stdinComplete(stdin, cachedSnap, meta.ScopedProbed, now) {
		if e.cache != nil && e.refresher != nil {
			if claimed, err := e.cache.ClaimRefresh(now, e.ttl); err == nil && claimed {
				if serr := e.refresher.Spawn(ctx); serr != nil {
					if fresh, _ := e.refreshSnapshot(ctx, now, cachedSnap); fresh != nil {
						cachedSnap = fresh
						dataStale = false
					}
				}
			}
		}
	}
	merged, usedCache := usage.Overlay(stdin, cachedSnap)
	stale := usedCache && dataStale
	var age time.Duration
	if stale {
		age = now.Sub(meta.StoredAt)
	}
	return snapshotState(merged, schema.SourceStdin, stale, age, schema.AuthOK)
}

func (e *Engine) refreshSnapshot(ctx context.Context, now time.Time, cachedSnap *schema.Snapshot) (*schema.Snapshot, []string) {
	if e.creds == nil || e.fetcher == nil {
		return nil, nil
	}
	cr, _, _ := e.resolveToken(now)
	if cr == nil {
		return nil, nil
	}
	rctx, cancel := context.WithTimeout(ctx, stdinRefreshTimeout)
	defer cancel()
	snap, err := e.fetchWithToken(rctx, cr, now)
	if err != nil {
		return nil, nil
	}
	merged := usage.Reconcile(cachedSnap, snap.Snapshot, now)
	e.storeCache(merged, snap.ScopedProbed, snap.Drift)
	return merged, snap.Drift
}

func stdinComplete(s, base *schema.Snapshot, probed bool, now time.Time) bool {
	if s == nil || base == nil {
		return false
	}
	if !coversWindow(s.FiveHour, base.FiveHour, now) {
		return false
	}
	if !coversWindow(s.SevenDay, base.SevenDay, now) {
		return false
	}
	if len(s.ScopedLimits) == 0 && !probed {
		return false
	}
	for name, w := range base.ScopedLimits {
		if w == nil {
			continue
		}
		if !coversWindow(s.ScopedLimits[name], w, now) {
			return false
		}
	}
	return true
}

func coversWindow(stdinW, baseW *schema.Window, now time.Time) bool {
	if baseW == nil {
		return true
	}
	return stdinW != nil && stdinW.ResetsAt.After(now)
}

func (e *Engine) degradeAuth(now time.Time, auth schema.AuthStatus, reason string, cachedSnap *schema.Snapshot, meta CacheEntry, haveCache bool) *schema.State {
	if haveCache {
		st := snapshotState(cachedSnap, schema.SourceCache, true, now.Sub(meta.StoredAt), auth)
		st.Drift = meta.Drift
		return st
	}
	if lh := e.probeTranscript(now); lh != nil {
		return transcriptState(lh, auth)
	}
	if reason == "" {
		reason = e.authMessage(auth)
	}
	return errorState(auth, reason)
}

func (e *Engine) authMessage(auth schema.AuthStatus) string {
	if auth == schema.AuthExpired {
		return fmt.Sprintf("token expired; run `%s` to refresh it", e.provider.LoginCommand)
	}
	return fmt.Sprintf("no credentials found; run `%s` to log in", e.provider.LoginCommand)
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

func (e *Engine) loadCache() (*schema.Snapshot, CacheEntry, bool) {
	if e.cache == nil {
		return nil, CacheEntry{}, false
	}
	entry, err := e.cache.Load()
	if err != nil {
		return nil, CacheEntry{}, false
	}
	if len(entry.Payload) == 0 {
		return nil, *entry, false
	}
	var snap schema.Snapshot
	if err := json.Unmarshal(entry.Payload, &snap); err != nil {
		return nil, *entry, false
	}
	if snap.FiveHour == nil && snap.SevenDay == nil && len(snap.ScopedLimits) == 0 {
		return nil, *entry, false
	}
	return &snap, *entry, true
}

func (e *Engine) storeCache(snap *schema.Snapshot, probed bool, drift []string) {
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
	_ = e.cache.Store(CacheEntry{
		Payload:      payload,
		StoredAt:     when,
		ScopedProbed: probed,
		Drift:        drift,
	})
}
