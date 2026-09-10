package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/provider"
	"github.com/seosd97/cc-token-exposer/internal/schema"
)

const DefaultTTL = 120 * time.Second

const syncFallbackTimeout = 5 * time.Second

type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type CacheEntry struct {
	Payload      []byte
	StoredAt     time.Time
	AttemptedAt  time.Time
	ScopedProbed bool
	Drift        []string
	LimitHit     *schema.LimitHit
}

type Cache interface {
	Load() (*CacheEntry, error)
	Store(e CacheEntry) error
	ClaimRefresh(now time.Time, backoff time.Duration) (bool, error)
	StoreLimitHit(lh *schema.LimitHit) error
}

type Refresher interface {
	Spawn(ctx context.Context) error
}

type Options struct {
	Spec      provider.Spec
	Cache     Cache
	Refresher Refresher
	Clock     Clock
	TTL       time.Duration
}

type Engine struct {
	spec      provider.Spec
	cache     Cache
	refresher Refresher
	clock     Clock
	ttl       time.Duration
}

func New(o Options) *Engine {
	e := &Engine{spec: o.Spec, cache: o.Cache, refresher: o.Refresher, clock: o.Clock, ttl: o.TTL}
	if e.clock == nil {
		e.clock = realClock{}
	}
	if e.ttl <= 0 {
		e.ttl = DefaultTTL
	}
	return e
}

func (e *Engine) Resolve(ctx context.Context) *schema.State {
	return e.tag(e.resolve(ctx, inlineRefresh))
}

func (e *Engine) ResolveDetached(ctx context.Context) *schema.State {
	return e.tag(e.resolve(ctx, detachedRefresh))
}

func (e *Engine) ResolveStdin(ctx context.Context, stdin *schema.Snapshot) *schema.State {
	return e.tag(e.resolveStdin(ctx, stdin))
}

func (e *Engine) tag(st *schema.State) *schema.State {
	st.Provider = e.spec.Name
	return st
}

type refreshMode int

const (
	inlineRefresh refreshMode = iota
	detachedRefresh
)

func (m refreshMode) noDataMessage() string {
	if m == detachedRefresh {
		return "usage refresh in progress; no cache yet"
	}
	return "usage fetch failed and no cache is available"
}

func (e *Engine) resolve(ctx context.Context, mode refreshMode) *schema.State {
	now := e.clock.Now()
	cached, meta, haveCache := e.loadCache()
	if st := e.freshCacheState(now, cached, meta, haveCache); st != nil {
		return st
	}

	cr, auth, cerr := e.resolveToken(now)
	if cr == nil {
		if reason, ok := noPlanReason(cerr); ok {
			return errorState(schema.AuthNoPlan, reason)
		}
		return e.degradeAuth(now, mode, auth, credsReason(cerr), cached, meta, haveCache)
	}

	fresh, drift, ferr := e.refresh(ctx, mode, now, cr, cached, meta)
	if fresh != nil {
		return freshState(fresh, schema.SourceOAuth, drift)
	}
	if errors.Is(ferr, provider.ErrAuth) {
		return e.degradeAuth(now, mode, schema.AuthExpired, "", cached, meta, haveCache)
	}
	if haveCache {
		return staleCacheState(now, cached, meta, schema.AuthOK)
	}
	return e.degradeNoData(now, mode, meta, mode.noDataMessage())
}

func (e *Engine) resolveStdin(ctx context.Context, stdin *schema.Snapshot) *schema.State {
	if stdin == nil {
		return e.resolve(ctx, detachedRefresh)
	}
	now := e.clock.Now()
	cached, meta, haveCache := e.loadCache()
	dataStale := !haveCache || now.Sub(meta.StoredAt) >= e.ttl
	if !stdinComplete(stdin, cached, meta.ScopedProbed, now) {
		if fresh, _, _ := e.detachedRefresh(ctx, now, nil, cached, meta); fresh != nil {
			cached, dataStale = fresh, false
		}
	}
	merged, usedCache := overlay(stdin, cached, now)
	if usedCache && dataStale {
		return staleState(merged, schema.SourceStdin, now.Sub(meta.StoredAt), schema.AuthOK, nil)
	}
	return freshState(merged, schema.SourceStdin, nil)
}

func (e *Engine) refresh(ctx context.Context, mode refreshMode, now time.Time, cr *provider.Credentials, cached *schema.Snapshot, meta CacheEntry) (*schema.Snapshot, []string, error) {
	if mode == inlineRefresh {
		return e.fetchAndStore(ctx, now, cr, cached)
	}
	return e.detachedRefresh(ctx, now, cr, cached, meta)
}

func (e *Engine) detachedRefresh(ctx context.Context, now time.Time, cr *provider.Credentials, cached *schema.Snapshot, meta CacheEntry) (*schema.Snapshot, []string, error) {
	if e.cache == nil || !e.refreshDue(now, meta) {
		return nil, nil, nil
	}
	claimed, err := e.cache.ClaimRefresh(now, e.ttl)
	if err != nil || !claimed {
		return nil, nil, nil
	}
	if e.refresher != nil && e.refresher.Spawn(ctx) == nil {
		return nil, nil, nil
	}
	return e.boundedRefresh(ctx, now, cr, cached)
}

func (e *Engine) refreshDue(now time.Time, meta CacheEntry) bool {
	last := meta.StoredAt
	if meta.AttemptedAt.After(last) {
		last = meta.AttemptedAt
	}
	return last.IsZero() || now.Sub(last) >= e.ttl
}

func (e *Engine) boundedRefresh(ctx context.Context, now time.Time, cr *provider.Credentials, cached *schema.Snapshot) (*schema.Snapshot, []string, error) {
	if cr == nil {
		if cr, _, _ = e.resolveToken(now); cr == nil {
			return nil, nil, nil
		}
	}
	rctx, cancel := context.WithTimeout(ctx, syncFallbackTimeout)
	defer cancel()
	return e.fetchAndStore(rctx, now, cr, cached)
}

func (e *Engine) fetchAndStore(ctx context.Context, now time.Time, cr *provider.Credentials, cached *schema.Snapshot) (*schema.Snapshot, []string, error) {
	snap, err := e.fetchWithToken(ctx, cr, now)
	if err != nil {
		return nil, nil, err
	}
	merged := reconcile(cached, snap.Snapshot, now)
	e.storeCache(merged, snap.ScopedProbed, snap.Drift)
	return merged, snap.Drift, nil
}

func (e *Engine) freshCacheState(now time.Time, cached *schema.Snapshot, meta CacheEntry, haveCache bool) *schema.State {
	if !haveCache {
		return nil
	}
	if age := now.Sub(meta.StoredAt); age < 0 || age >= e.ttl {
		return nil
	}
	return freshState(cached, schema.SourceCache, meta.Drift)
}

func staleCacheState(now time.Time, cached *schema.Snapshot, meta CacheEntry, auth schema.AuthStatus) *schema.State {
	return staleState(cached, schema.SourceCache, now.Sub(meta.StoredAt), auth, meta.Drift)
}

func (e *Engine) resolveToken(now time.Time) (*provider.Credentials, schema.AuthStatus, error) {
	if e.spec.Creds == nil {
		return nil, schema.AuthMissing, nil
	}
	cr, err := e.spec.Creds.Resolve()
	if err != nil || cr == nil || cr.AccessToken == "" {
		return nil, schema.AuthMissing, err
	}
	if cr.Expired(now) {
		return nil, schema.AuthExpired, nil
	}
	return cr, schema.AuthOK, nil
}

func credsReason(err error) string {
	if err == nil || errors.Is(err, provider.ErrNotFound) || errors.Is(err, provider.ErrNotAvailable) {
		return ""
	}
	return err.Error()
}

func noPlanReason(err error) (string, bool) {
	var np *provider.NoPlanError
	if errors.As(err, &np) {
		return np.Reason, true
	}
	return "", false
}

func (e *Engine) fetchWithToken(ctx context.Context, cr *provider.Credentials, now time.Time) (*provider.FetchedSnapshot, error) {
	if e.spec.Fetcher == nil {
		return nil, fmt.Errorf("%w: no fetcher configured", provider.ErrTransient)
	}
	snap, err := e.spec.Fetcher.Fetch(ctx, cr)
	if err == nil || !errors.Is(err, provider.ErrAuth) {
		return snap, err
	}
	rotated := e.rotatedCredentials(cr, now)
	if rotated == nil {
		return nil, err
	}
	return e.spec.Fetcher.Fetch(ctx, rotated)
}

func (e *Engine) rotatedCredentials(cr *provider.Credentials, now time.Time) *provider.Credentials {
	if e.spec.Creds == nil {
		return nil
	}
	cr2, err := e.spec.Creds.Resolve()
	if err != nil || cr2 == nil || cr2.AccessToken == "" || cr2.AccessToken == cr.AccessToken || cr2.Expired(now) {
		return nil
	}
	return cr2
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

func (e *Engine) degradeAuth(now time.Time, mode refreshMode, auth schema.AuthStatus, reason string, cached *schema.Snapshot, meta CacheEntry, haveCache bool) *schema.State {
	if haveCache {
		return staleCacheState(now, cached, meta, auth)
	}
	if lh := e.limitHit(now, mode, meta); lh != nil {
		return transcriptState(lh, auth)
	}
	if reason == "" {
		reason = e.authMessage(auth)
	}
	return errorState(auth, reason)
}

func (e *Engine) authMessage(auth schema.AuthStatus) string {
	msg, verb := "no credentials found", "log in"
	if auth == schema.AuthExpired {
		msg, verb = "token expired", "refresh it"
	}
	if e.spec.LoginCommand == "" {
		return msg
	}
	return fmt.Sprintf("%s; run `%s` to %s", msg, e.spec.LoginCommand, verb)
}

func (e *Engine) degradeNoData(now time.Time, mode refreshMode, meta CacheEntry, msg string) *schema.State {
	if lh := e.limitHit(now, mode, meta); lh != nil {
		return transcriptState(lh, schema.AuthOK)
	}
	return errorState(schema.AuthOK, msg)
}

func (e *Engine) limitHit(now time.Time, mode refreshMode, meta CacheEntry) *schema.LimitHit {
	if mode == detachedRefresh {
		return activeLimitHit(meta.LimitHit, now)
	}
	lh := activeLimitHit(e.probeTranscript(now), now)
	if e.cache != nil && (lh != nil || meta.LimitHit != nil) {
		_ = e.cache.StoreLimitHit(lh)
	}
	return lh
}

func activeLimitHit(lh *schema.LimitHit, now time.Time) *schema.LimitHit {
	if !lh.Active(now) {
		return nil
	}
	return lh
}

func (e *Engine) probeTranscript(now time.Time) *schema.LimitHit {
	if e.spec.Transcript == nil {
		return nil
	}
	lh, err := e.spec.Transcript.Probe(now)
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
	if err != nil || entry == nil {
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
