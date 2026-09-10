package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/creds"
	"github.com/seosd97/cc-token-exposer/internal/engine"
	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/seosd97/cc-token-exposer/internal/usage"
)

var baseTime = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

type credResult struct {
	c   *creds.Credentials
	err error
}

type fakeCreds struct {
	results []credResult
	calls   int
}

func (f *fakeCreds) Resolve() (*creds.Credentials, error) {
	i := f.calls
	if i >= len(f.results) {
		i = len(f.results) - 1
	}
	f.calls++
	return f.results[i].c, f.results[i].err
}

type fakeFetcher struct {
	fn     func(token string) (*schema.Snapshot, error)
	probed bool
	drift  []string
	calls  []string
}

func (f *fakeFetcher) Fetch(_ context.Context, cr *creds.Credentials) (*usage.FetchedSnapshot, error) {
	token := cr.AccessToken
	f.calls = append(f.calls, token)
	s, err := f.fn(token)
	if err != nil {
		return nil, err
	}
	return &usage.FetchedSnapshot{Snapshot: s, ScopedProbed: f.probed, Drift: f.drift}, nil
}

type fakeCache struct {
	payload      []byte
	storedAt     time.Time
	attemptedAt  time.Time
	scopedProbed bool
	drift        []string
	has          bool
	stores       int
	claims       int
	claimResult  bool
	claimErr     error
	lastStored   []byte
}

func (c *fakeCache) Load() (*engine.CacheEntry, error) {
	if !c.has {
		return nil, engine.ErrNoCache
	}
	return &engine.CacheEntry{
		Payload:      c.payload,
		StoredAt:     c.storedAt,
		AttemptedAt:  c.attemptedAt,
		ScopedProbed: c.scopedProbed,
		Drift:        c.drift,
	}, nil
}

func (c *fakeCache) Store(e engine.CacheEntry) error {
	c.stores++
	c.lastStored = e.Payload
	c.payload = e.Payload
	c.storedAt = e.StoredAt
	c.scopedProbed = e.ScopedProbed
	c.drift = e.Drift
	c.has = true
	return nil
}

func (c *fakeCache) ClaimRefresh(attemptedAt time.Time, _ time.Duration) (bool, error) {
	c.claims++
	c.attemptedAt = attemptedAt
	if c.claimErr != nil {
		return false, c.claimErr
	}
	if c.claimResult {
		if !c.has {
			c.payload = []byte("null")
			c.has = true
		}
		return true, nil
	}
	return false, nil
}

type fakeRefresher struct {
	spawns int
	err    error
}

func (r *fakeRefresher) Spawn(context.Context) error {
	r.spawns++
	return r.err
}

type fakeTranscript struct {
	lh *schema.LimitHit
}

func (f *fakeTranscript) Probe(time.Time) (*schema.LimitHit, error) { return f.lh, nil }

func okCreds(token string) *fakeCreds {
	return &fakeCreds{results: []credResult{{c: &creds.Credentials{AccessToken: token}}}}
}

func snapWith(util float64, resetsAt time.Time) *schema.Snapshot {
	return &schema.Snapshot{
		FetchedAt: baseTime,
		FiveHour:  &schema.Window{Utilization: util, ResetsAt: resetsAt},
	}
}

func ptr(v float64) *float64 { return &v }

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func resolve(t *testing.T, o engine.Options) *schema.State {
	t.Helper()
	if o.Clock == nil {
		o.Clock = &fakeClock{t: baseTime}
	}
	st := engine.New(o).Resolve(context.Background())
	if st == nil {
		t.Fatalf("Resolve returned nil state")
	}
	if st.SchemaVersion != schema.Version {
		t.Fatalf("schema_version = %d, want %d", st.SchemaVersion, schema.Version)
	}
	return st
}

func TestFreshCacheServedWithoutFetch(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(23, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-30 * time.Second),
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("fetch must not be called on fresh cache")
		return nil, nil
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})

	if st.Source != schema.SourceCache || st.Stale {
		t.Fatalf("got source=%s stale=%v, want cache/false", st.Source, st.Stale)
	}
	if len(fetch.calls) != 0 {
		t.Fatalf("fetch calls = %d, want 0", len(fetch.calls))
	}
	if st.Snapshot == nil || st.Snapshot.FiveHour.Utilization != 23 {
		t.Fatalf("unexpected snapshot: %+v", st.Snapshot)
	}
}

func TestStaleCacheTriggersFetchAndStore(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(10, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-10 * time.Minute),
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		return snapWith(42, baseTime.Add(time.Hour)), nil
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})

	if st.Source != schema.SourceOAuth || st.Stale {
		t.Fatalf("got source=%s stale=%v, want oauth/false", st.Source, st.Stale)
	}
	if st.Snapshot.FiveHour.Utilization != 42 {
		t.Fatalf("util = %v, want 42", st.Snapshot.FiveHour.Utilization)
	}
	if cache.stores != 1 {
		t.Fatalf("cache stores = %d, want 1", cache.stores)
	}
}

func TestReconcileGuardRetainsSuspectValue(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	reset := baseTime.Add(time.Hour)
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(80, reset)),
		storedAt: baseTime.Add(-10 * time.Minute),
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		return snapWith(40, reset), nil
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})

	if st.Snapshot.FiveHour.Utilization != 80 || !st.Snapshot.FiveHour.Suspect {
		t.Fatalf("got util=%v suspect=%v, want 80/true", st.Snapshot.FiveHour.Utilization, st.Snapshot.FiveHour.Suspect)
	}
}

func TestTransientErrorServesStaleCache(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(55, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-10 * time.Minute),
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		return nil, usage.ErrTransient
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})

	if st.Source != schema.SourceCache || !st.Stale {
		t.Fatalf("got source=%s stale=%v, want cache/true", st.Source, st.Stale)
	}
	if st.StaleAge == nil || time.Duration(*st.StaleAge) != 10*time.Minute {
		t.Fatalf("stale_age = %v, want 10m", st.StaleAge)
	}
}

func TestRateLimitedServesStaleCache(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(55, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-10 * time.Minute),
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		return nil, &usage.RateLimitError{RetryAfter: time.Minute, StatusCode: 429}
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})
	if st.Source != schema.SourceCache || !st.Stale {
		t.Fatalf("got source=%s stale=%v, want cache/true", st.Source, st.Stale)
	}
}

func TestTransientErrorNoCacheIsError(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		return nil, usage.ErrTransient
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch})
	if st.Type != schema.TypeError || st.Auth != schema.AuthOK {
		t.Fatalf("got type=%s auth=%s, want error/ok", st.Type, st.Auth)
	}
}

func TestMissingCredsNoCacheIsAuthMissing(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cr := &fakeCreds{results: []credResult{{err: creds.ErrNotFound}}}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("fetch must not be called without creds")
		return nil, nil
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch})
	if st.Type != schema.TypeError || st.Auth != schema.AuthMissing {
		t.Fatalf("got type=%s auth=%s, want error/missing", st.Type, st.Auth)
	}
}

func TestMissingCredsServesStaleCache(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(33, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-10 * time.Minute),
		has:      true,
	}
	cr := &fakeCreds{results: []credResult{{err: creds.ErrNotFound}}}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) { return nil, nil }}

	st := resolve(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch, Cache: cache})
	if st.Source != schema.SourceCache || !st.Stale || st.Auth != schema.AuthMissing {
		t.Fatalf("got source=%s stale=%v auth=%s, want cache/true/missing", st.Source, st.Stale, st.Auth)
	}
}

func TestExpiredTokenReReadRecovers(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	expired := &creds.Credentials{AccessToken: "old", ExpiresAt: baseTime.Add(-time.Minute)}
	fresh := &creds.Credentials{AccessToken: "new", ExpiresAt: baseTime.Add(time.Hour)}
	cr := &fakeCreds{results: []credResult{{c: expired}, {c: fresh}}}
	fetch := &fakeFetcher{fn: func(token string) (*schema.Snapshot, error) {
		if token != "new" {
			t.Fatalf("fetched with stale token %q", token)
		}
		return snapWith(12, baseTime.Add(time.Hour)), nil
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch})
	if st.Source != schema.SourceOAuth || st.Auth != schema.AuthOK {
		t.Fatalf("got source=%s auth=%s, want oauth/ok", st.Source, st.Auth)
	}
}

func TestExpiredTokenStillExpiredIsAuthExpired(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	expired := &creds.Credentials{AccessToken: "old", ExpiresAt: baseTime.Add(-time.Minute)}
	cr := &fakeCreds{results: []credResult{{c: expired}}}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("fetch must not run with an expired token")
		return nil, nil
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch})
	if st.Type != schema.TypeError || st.Auth != schema.AuthExpired {
		t.Fatalf("got type=%s auth=%s, want error/expired", st.Type, st.Auth)
	}
}

func TestAuthErrorRetriesWithNewToken(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	first := &creds.Credentials{AccessToken: "tok-A"}
	second := &creds.Credentials{AccessToken: "tok-B"}
	cr := &fakeCreds{results: []credResult{{c: first}, {c: second}}}
	fetch := &fakeFetcher{fn: func(token string) (*schema.Snapshot, error) {
		if token == "tok-A" {
			return nil, usage.ErrAuth
		}
		return snapWith(7, baseTime.Add(time.Hour)), nil
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch})
	if st.Source != schema.SourceOAuth || st.Auth != schema.AuthOK {
		t.Fatalf("got source=%s auth=%s, want oauth/ok", st.Source, st.Auth)
	}
	if len(fetch.calls) != 2 {
		t.Fatalf("fetch calls = %d, want 2", len(fetch.calls))
	}
}

func TestAuthErrorSameTokenDegradesToStaleCache(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	tok := &creds.Credentials{AccessToken: "tok"}
	cr := &fakeCreds{results: []credResult{{c: tok}}}
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(60, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-10 * time.Minute),
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		return nil, usage.ErrAuth
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch, Cache: cache})
	if st.Source != schema.SourceCache || !st.Stale || st.Auth != schema.AuthExpired {
		t.Fatalf("got source=%s stale=%v auth=%s, want cache/true/expired", st.Source, st.Stale, st.Auth)
	}
}

func TestTranscriptFallbackWhenNoCacheNoCreds(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	reset := baseTime.Add(2 * time.Hour)
	cr := &fakeCreds{results: []credResult{{err: creds.ErrNotFound}}}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) { return nil, nil }}
	tr := &fakeTranscript{lh: &schema.LimitHit{ResetsAt: &reset, Message: "session limit", DetectedAt: baseTime}}

	st := resolve(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch, Transcript: tr})
	if st.Source != schema.SourceTranscript || st.LimitHit == nil {
		t.Fatalf("got source=%s limitHit=%v, want transcript/non-nil", st.Source, st.LimitHit)
	}
	if st.Auth != schema.AuthMissing {
		t.Fatalf("auth = %s, want missing", st.Auth)
	}
}

func resolveStdin(t *testing.T, o engine.Options, stdin *schema.Snapshot) *schema.State {
	t.Helper()
	if o.Clock == nil {
		o.Clock = &fakeClock{t: baseTime}
	}
	st := engine.New(o).ResolveStdin(context.Background(), stdin)
	if st == nil {
		t.Fatalf("ResolveStdin returned nil state")
	}
	return st
}

func fullSnap(five, seven float64, resetsAt time.Time) *schema.Snapshot {
	return &schema.Snapshot{
		FetchedAt:    baseTime,
		FiveHour:     &schema.Window{Utilization: five, ResetsAt: resetsAt},
		SevenDay:     &schema.Window{Utilization: seven, ResetsAt: resetsAt.Add(5 * 24 * time.Hour)},
		ScopedLimits: map[string]*schema.Window{"Sonnet": {Utilization: 7, ResetsAt: resetsAt.Add(5 * 24 * time.Hour)}},
	}
}

func TestStdinNilFallsBackToResolve(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(23, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-30 * time.Second),
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("fetch must not be called on fresh cache")
		return nil, nil
	}}

	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache}, nil)
	if st.Source != schema.SourceCache || st.Snapshot.FiveHour.Utilization != 23 {
		t.Fatalf("nil stdin should delegate to Resolve: source=%s snap=%+v", st.Source, st.Snapshot)
	}
}

func TestStdinCompleteSnapshotServedNoIO(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("complete stdin must not fetch")
		return nil, nil
	}}
	cached := fullSnap(99, 30, baseTime.Add(time.Hour))
	cached.ScopedLimits["Fable"] = &schema.Window{Utilization: 40, ResetsAt: baseTime.Add(5 * 24 * time.Hour)}
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-10 * time.Minute),
		scopedProbed: true,
		has:          true,
	}

	stdin := fullSnap(18, 41, baseTime.Add(time.Hour))
	stdin.ScopedLimits["Fable"] = &schema.Window{Utilization: 55, ResetsAt: baseTime.Add(5 * 24 * time.Hour)}
	st := resolveStdin(t, engine.Options{Clock: clk, Fetcher: fetch, Cache: cache}, stdin)

	if st.Source != schema.SourceStdin || st.Stale || st.Auth != schema.AuthOK {
		t.Fatalf("got source=%s stale=%v auth=%s, want stdin/false/ok", st.Source, st.Stale, st.Auth)
	}
	if len(fetch.calls) != 0 || cache.stores != 0 || cache.claims != 0 {
		t.Fatalf("stdin covering every cached window did I/O: fetch=%d stores=%d claims=%d", len(fetch.calls), cache.stores, cache.claims)
	}
	if st.Snapshot.ScopedLimits["Fable"] == nil || st.Snapshot.ScopedLimits["Fable"].Utilization != 55 {
		t.Fatalf("Fable = %+v, want stdin value 55 to win over cached 40", st.Snapshot.ScopedLimits["Fable"])
	}
	b, err := json.Marshal(st.Snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if !strings.Contains(string(b), "seven_day_fable") {
		t.Fatalf("fable alias not emitted on the wire: %s", b)
	}
}

func TestStdinMissingCachedScopedModelSpawnsRefresh(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := fullSnap(99, 30, baseTime.Add(time.Hour))
	cached.ScopedLimits["Fable"] = &schema.Window{Utilization: 40, ResetsAt: baseTime.Add(5 * 24 * time.Hour)}
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-10 * time.Minute),
		scopedProbed: true,
		claimResult:  true,
		has:          true,
	}
	fetched := fullSnap(99, 30, baseTime.Add(time.Hour))
	fetched.ScopedLimits["Fable"] = &schema.Window{Utilization: 62, ResetsAt: baseTime.Add(5 * 24 * time.Hour)}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) { return fetched, nil }}
	fetch.probed = true
	ref := &fakeRefresher{}

	stdin := fullSnap(18, 41, baseTime.Add(time.Hour))
	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, stdin)

	if ref.spawns != 1 || len(fetch.calls) != 0 || cache.claims != 1 {
		t.Fatalf("expected one claim+spawn and no parent fetch, got spawn=%d fetch=%d claims=%d", ref.spawns, len(fetch.calls), cache.claims)
	}
	if f := st.Snapshot.ScopedLimits["Fable"]; f == nil || f.Utilization != 40 {
		t.Fatalf("Fable = %+v, want cached 40 until the detached refresh lands", f)
	}
	if !st.Stale {
		t.Fatal("stale cache contribution should mark the line ≈ until the refresh lands")
	}
	if st.Snapshot.FiveHour.Utilization != 18 || st.Snapshot.ScopedLimits["Sonnet"].Utilization != 7 {
		t.Fatalf("stdin must still win for the windows it carries: %+v", st.Snapshot)
	}

	if err := cache.Store(engine.CacheEntry{
		Payload:      mustMarshal(t, fetched),
		StoredAt:     clk.Now(),
		ScopedProbed: true,
	}); err != nil {
		t.Fatalf("model detached child store: %v", err)
	}
	cache.claimResult = false
	clk.t = baseTime.Add(3 * time.Second)
	st = resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, stdin)
	if f := st.Snapshot.ScopedLimits["Fable"]; f == nil || f.Utilization != 62 {
		t.Fatalf("Fable = %+v, want healed 62 from cache on the next tick", f)
	}
	if len(fetch.calls) != 0 || ref.spawns != 1 {
		t.Fatalf("a healed cache must not re-refresh: fetch=%d spawn=%d", len(fetch.calls), ref.spawns)
	}
	if st.Stale {
		t.Fatal("fresh cache should not mark the line stale")
	}
}

func TestStdinScopedEmptyWithProbedScopedlessCacheNeedsNoRefresh(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := fullSnap(99, 30, baseTime.Add(time.Hour))
	cached.ScopedLimits = nil
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-10 * time.Minute),
		scopedProbed: true,
		has:          true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("a probed cache without scoped limits proves stdin complete")
		return nil, nil
	}}

	stdin := &schema.Snapshot{
		FetchedAt: baseTime,
		FiveHour:  &schema.Window{Utilization: 18, ResetsAt: baseTime.Add(time.Hour)},
		SevenDay:  &schema.Window{Utilization: 41, ResetsAt: baseTime.Add(time.Hour + 5*24*time.Hour)},
	}
	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache}, stdin)

	if len(fetch.calls) != 0 || cache.claims != 0 {
		t.Fatalf("expected zero I/O, got fetch=%d claims=%d", len(fetch.calls), cache.claims)
	}
	if st.Stale {
		t.Fatal("no cache contribution means not stale")
	}
}

func TestStdinWithoutCacheBootstrapsOnce(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{claimResult: true}
	fetched := fullSnap(99, 30, baseTime.Add(time.Hour))
	fetched.ScopedLimits["Fable"] = &schema.Window{Utilization: 55, ResetsAt: baseTime.Add(5 * 24 * time.Hour)}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) { return fetched, nil }}
	fetch.probed = true
	ref := &fakeRefresher{}

	stdin := fullSnap(18, 41, baseTime.Add(time.Hour))
	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, stdin)

	if ref.spawns != 1 || len(fetch.calls) != 0 || cache.stores != 0 {
		t.Fatalf("no cache must spawn exactly one refresh and never fetch in the parent, got spawn=%d fetch=%d stores=%d", ref.spawns, len(fetch.calls), cache.stores)
	}
	if st.Snapshot.ScopedLimits["Fable"] != nil {
		t.Fatal("bootstrap spawn must not surface scoped models until the detached refresh lands")
	}
	if st.Stale {
		t.Fatal("stdin-only serve should not be stale")
	}

	if err := cache.Store(engine.CacheEntry{Payload: mustMarshal(t, fetched), StoredAt: clk.Now(), ScopedProbed: true}); err != nil {
		t.Fatalf("model detached child store: %v", err)
	}
	cache.claimResult = false
	clk.t = baseTime.Add(3 * time.Second)
	st = resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, stdin)
	if f := st.Snapshot.ScopedLimits["Fable"]; f == nil || f.Utilization != 55 {
		t.Fatalf("bootstrap must surface scoped models the next tick: %+v", st.Snapshot.ScopedLimits)
	}
}

func TestStdinCompleteWithStaleCacheStillNoFetch(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := snapWith(99, baseTime.Add(time.Hour))
	cached.ExtraUsage = &schema.ExtraUsage{Utilization: ptr(5.0)}
	cache := &fakeCache{
		payload:  mustMarshal(t, cached),
		storedAt: baseTime.Add(-10 * time.Minute),
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("complete stdin must not fetch even with stale cache")
		return nil, nil
	}}

	stdin := fullSnap(18, 41, baseTime.Add(time.Hour))
	st := resolveStdin(t, engine.Options{Clock: clk, Fetcher: fetch, Cache: cache}, stdin)

	if st.Stale {
		t.Fatal("complete stdin should not be stale")
	}
	if cache.claims != 0 {
		t.Fatalf("complete stdin must not try to claim a refresh slot, got claims=%d", cache.claims)
	}
	if st.Snapshot.FiveHour.Utilization != 18 || st.Snapshot.SevenDay.Utilization != 41 {
		t.Fatalf("snap = %+v, want stdin values", st.Snapshot)
	}
}

func TestStdinGapsFilledFromFreshCacheNotStale(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := snapWith(99, baseTime.Add(time.Hour))
	cached.ScopedLimits = map[string]*schema.Window{
		"Fable": {Utilization: 40, ResetsAt: baseTime.Add(5 * 24 * time.Hour)},
	}
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-30 * time.Second),
		scopedProbed: true,
		has:          true,
	}
	ref := &fakeRefresher{}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("fresh cache must not trigger a refresh")
		return nil, nil
	}}

	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, snapWith(18, baseTime.Add(time.Hour)))

	if st.Snapshot.FiveHour.Utilization != 18 {
		t.Fatalf("five_hour = %v, want stdin 18", st.Snapshot.FiveHour.Utilization)
	}
	if st.Snapshot.ScopedLimits["Fable"] == nil || st.Snapshot.ScopedLimits["Fable"].Utilization != 40 {
		t.Fatalf("scoped = %+v, want Fable 40 from cache", st.Snapshot.ScopedLimits)
	}
	if st.Stale {
		t.Fatal("cache within TTL should not mark the state stale")
	}
	if cache.claims != 1 || ref.spawns != 0 {
		t.Fatalf("fresh-cache incomplete stdin should claim-but-not-spawn, got claims=%d spawn=%d", cache.claims, ref.spawns)
	}
}

func TestStdinIncompleteClaimsRefreshSlot(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := snapWith(99, baseTime.Add(time.Hour))
	cached.ScopedLimits = map[string]*schema.Window{
		"Fable": {Utilization: 40, ResetsAt: baseTime.Add(5 * 24 * time.Hour)},
	}
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-10 * time.Minute),
		scopedProbed: true,
		claimResult:  true,
		has:          true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("parent must not fetch on the stdin path")
		return nil, nil
	}}
	ref := &fakeRefresher{}

	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, snapWith(18, baseTime.Add(time.Hour)))

	if ref.spawns != 1 || cache.claims != 1 {
		t.Fatalf("expected exactly one claimed+spawned refresh, got spawn=%d claims=%d", ref.spawns, cache.claims)
	}
	if !st.Stale {
		t.Fatal("stale cache contribution should mark the line ≈ until the refresh lands")
	}
	if st.Snapshot.FiveHour.Utilization != 18 {
		t.Fatalf("five_hour = %v, want stdin 18 to win", st.Snapshot.FiveHour.Utilization)
	}
	if st.Snapshot.ScopedLimits["Fable"] == nil || st.Snapshot.ScopedLimits["Fable"].Utilization != 40 {
		t.Fatalf("scoped = %+v, want Fable 40 from cache until healed", st.Snapshot.ScopedLimits)
	}
}

func TestStdinIncompleteNoCacheSpawnsRefresh(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{claimResult: true}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("parent must not fetch on the stdin path")
		return nil, nil
	}}
	ref := &fakeRefresher{}

	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, snapWith(18, baseTime.Add(time.Hour)))

	if ref.spawns != 1 || len(fetch.calls) != 0 {
		t.Fatalf("expected one spawn and no parent fetch, got spawn=%d fetch=%d", ref.spawns, len(fetch.calls))
	}
	if st.Snapshot.SevenDay != nil {
		t.Fatal("no cache means seven_day cannot be healed until the detached refresh lands")
	}
	if st.Stale {
		t.Fatal("stdin-only serve with no cache contribution should not be stale")
	}
}

func TestStdinRefreshSpawnsAndServesStale(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := snapWith(99, baseTime.Add(time.Hour))
	cached.ScopedLimits = map[string]*schema.Window{
		"Fable": {Utilization: 40, ResetsAt: baseTime.Add(5 * 24 * time.Hour)},
	}
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-10 * time.Minute),
		scopedProbed: true,
		claimResult:  true,
		has:          true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("parent must not fetch on the stdin path")
		return nil, nil
	}}
	ref := &fakeRefresher{}

	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, snapWith(18, baseTime.Add(time.Hour)))

	if ref.spawns != 1 {
		t.Fatalf("refresh must be claimed+spawned exactly once, got spawn=%d", ref.spawns)
	}
	if !st.Stale || st.StaleAge == nil || time.Duration(*st.StaleAge) != 10*time.Minute {
		t.Fatalf("got stale=%v age=%v, want true/10m", st.Stale, st.StaleAge)
	}
	if st.Snapshot.FiveHour.Utilization != 18 || st.Snapshot.ScopedLimits["Fable"] == nil {
		t.Fatalf("stale serve should still merge stdin+cache: %+v", st.Snapshot)
	}
	if st.Auth != schema.AuthOK {
		t.Fatalf("spawn path should not surface auth noise: auth=%s", st.Auth)
	}
}

func TestStdinSpawnPathNeverConsultsCreds(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := snapWith(99, baseTime.Add(time.Hour))
	cached.ScopedLimits = map[string]*schema.Window{
		"Fable": {Utilization: 40, ResetsAt: baseTime.Add(5 * 24 * time.Hour)},
	}
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-10 * time.Minute),
		scopedProbed: true,
		claimResult:  true,
		has:          true,
	}
	cr := &fakeCreds{results: []credResult{{err: errors.New("must not resolve creds in the spawn path")}}}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("parent must not fetch on the stdin path")
		return nil, nil
	}}
	ref := &fakeRefresher{}

	st := resolveStdin(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch, Cache: cache, Refresher: ref}, snapWith(18, baseTime.Add(time.Hour)))
	if ref.spawns != 1 {
		t.Fatalf("expected a spawn, got %d", ref.spawns)
	}
	if st.Auth != schema.AuthOK {
		t.Fatalf("spawn path must not surface auth status: auth=%s", st.Auth)
	}
}

func TestStdinSpawnFailureFallsBackToSyncRefresh(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := snapWith(99, baseTime.Add(time.Hour))
	cached.ScopedLimits = map[string]*schema.Window{
		"Fable": {Utilization: 40, ResetsAt: baseTime.Add(5 * 24 * time.Hour)},
	}
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-10 * time.Minute),
		scopedProbed: true,
		claimResult:  true,
		has:          true,
	}
	fetched := fullSnap(99, 30, baseTime.Add(time.Hour))
	fetched.ScopedLimits["Fable"] = &schema.Window{Utilization: 62, ResetsAt: baseTime.Add(5 * 24 * time.Hour)}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) { return fetched, nil }}
	fetch.probed = true
	ref := &fakeRefresher{err: errors.New("spawn failed")}

	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, snapWith(18, baseTime.Add(time.Hour)))

	if ref.spawns != 1 || len(fetch.calls) != 1 || cache.stores != 1 {
		t.Fatalf("spawn failure must fall back to one sync refresh, got spawn=%d fetch=%d stores=%d", ref.spawns, len(fetch.calls), cache.stores)
	}
	if f := st.Snapshot.ScopedLimits["Fable"]; f == nil || f.Utilization != 62 {
		t.Fatalf("Fable = %+v, want healed 62 from the fallback fetch", f)
	}
	if st.Stale {
		t.Fatal("successful fallback refresh should not be stale")
	}
}

func TestStdinClaimFalseThrottlesSpawn(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := snapWith(99, baseTime.Add(time.Hour))
	cached.ScopedLimits = map[string]*schema.Window{
		"Fable": {Utilization: 40, ResetsAt: baseTime.Add(5 * 24 * time.Hour)},
	}
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-10 * time.Minute),
		scopedProbed: true,
		claimResult:  true,
		has:          true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("parent must not fetch on the stdin path")
		return nil, nil
	}}
	ref := &fakeRefresher{}
	o := engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}
	stdin := snapWith(18, baseTime.Add(time.Hour))

	resolveStdin(t, o, stdin)
	cache.claimResult = false
	clk.t = baseTime.Add(3 * time.Second)
	resolveStdin(t, o, stdin)
	if ref.spawns != 1 {
		t.Fatalf("an unelapsed claim must throttle re-spawn, got spawn=%d, want 1", ref.spawns)
	}

	cache.claimResult = true
	clk.t = baseTime.Add(3*time.Second + engine.DefaultTTL)
	resolveStdin(t, o, stdin)
	if ref.spawns != 2 {
		t.Fatalf("a claim should resume once the TTL elapses, got spawn=%d, want 2", ref.spawns)
	}
}

func TestStdinScopedEmptyIsIncompleteAndSpawnsRefresh(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := snapWith(99, baseTime.Add(time.Hour))
	cache := &fakeCache{
		payload:     mustMarshal(t, cached),
		storedAt:    baseTime.Add(-10 * time.Minute),
		claimResult: true,
		has:         true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("parent must not fetch on the stdin path")
		return nil, nil
	}}
	ref := &fakeRefresher{}

	stdin := &schema.Snapshot{
		FetchedAt: baseTime,
		FiveHour:  &schema.Window{Utilization: 18, ResetsAt: baseTime.Add(time.Hour)},
		SevenDay:  &schema.Window{Utilization: 41, ResetsAt: baseTime.Add(5 * 24 * time.Hour)},
	}
	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, stdin)

	if ref.spawns != 1 {
		t.Fatalf("five_hour+seven_day with empty stdin scoped must be incomplete and spawn once, got spawn=%d", ref.spawns)
	}
	if st.Snapshot.ScopedLimits["Sonnet"] != nil {
		t.Fatalf("empty scoped limits heal only after the detached refresh lands: %+v", st.Snapshot.ScopedLimits)
	}
}

func TestStdinResetlessWindowIsIncompleteAndBackfilled(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := fullSnap(99, 30, baseTime.Add(time.Hour))
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-30 * time.Second),
		scopedProbed: true,
		claimResult:  true,
		has:          true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("parent must not fetch on the stdin path")
		return nil, nil
	}}
	ref := &fakeRefresher{}

	stdin := fullSnap(18, 41, baseTime.Add(time.Hour))
	stdin.FiveHour.ResetsAt = time.Time{}
	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, stdin)

	if ref.spawns != 1 || cache.claims != 1 {
		t.Fatalf("a reset-less stdin window must open the bounded refresh, got spawn=%d claims=%d", ref.spawns, cache.claims)
	}
	if st.Snapshot.FiveHour.Utilization != 18 {
		t.Fatalf("five_hour util = %v, want stdin 18", st.Snapshot.FiveHour.Utilization)
	}
	if !st.Snapshot.FiveHour.ResetsAt.Equal(baseTime.Add(time.Hour)) {
		t.Fatalf("five_hour reset = %v, want the cached reset backfilled", st.Snapshot.FiveHour.ResetsAt)
	}
	if st.Stale {
		t.Fatal("a fresh cache contributing only the reset must not mark the line stale")
	}
}

func TestStdinElapsedResetIsIncompleteAndHeals(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := fullSnap(99, 30, baseTime.Add(time.Hour))
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-10 * time.Minute),
		scopedProbed: true,
		claimResult:  true,
		has:          true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("parent must not fetch on the stdin path")
		return nil, nil
	}}
	ref := &fakeRefresher{}

	stdin := fullSnap(18, 41, baseTime.Add(time.Hour))
	stdin.FiveHour.ResetsAt = baseTime.Add(-time.Minute)
	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, stdin)

	if ref.spawns != 1 {
		t.Fatalf("an elapsed stdin reset must open the bounded refresh, got spawn=%d", ref.spawns)
	}
	if !st.Snapshot.FiveHour.ResetsAt.Equal(baseTime.Add(time.Hour)) {
		t.Fatalf("five_hour reset = %v, want the cache's future reset", st.Snapshot.FiveHour.ResetsAt)
	}
	if !st.Stale {
		t.Fatal("a stale cache contributing the reset must mark the line ≈")
	}
}

func TestStdinResetlessWindowNeedsNoHealWhenCacheLacksIt(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cached := fullSnap(99, 30, baseTime.Add(time.Hour))
	cached.FiveHour = nil
	cache := &fakeCache{
		payload:      mustMarshal(t, cached),
		storedAt:     baseTime.Add(-10 * time.Minute),
		scopedProbed: true,
		has:          true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("nothing to heal when the cache lacks the window")
		return nil, nil
	}}
	ref := &fakeRefresher{}

	stdin := fullSnap(18, 41, baseTime.Add(time.Hour))
	stdin.FiveHour.ResetsAt = time.Time{}
	st := resolveStdin(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache, Refresher: ref}, stdin)

	if cache.claims != 0 || ref.spawns != 0 || len(fetch.calls) != 0 {
		t.Fatalf("expected zero I/O, got claims=%d spawns=%d fetch=%d", cache.claims, ref.spawns, len(fetch.calls))
	}
	if st.Snapshot.FiveHour == nil || st.Snapshot.FiveHour.Utilization != 18 {
		t.Fatalf("stdin five_hour must still serve: %+v", st.Snapshot.FiveHour)
	}
}

func TestErrorStateCarriesNoSnapshot(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cr := &fakeCreds{results: []credResult{{err: errors.New("boom")}}}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) { return nil, nil }}

	st := resolve(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch})
	if st.Snapshot != nil {
		t.Fatalf("error state should have no snapshot")
	}
}

func TestDriftIndicatorsRideLiveFetchAndCache(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	const wantDrift = "five_hour missing resets_at"
	cache := &fakeCache{}
	fetch := &fakeFetcher{
		probed: true,
		drift:  []string{wantDrift},
		fn:     func(string) (*schema.Snapshot, error) { return snapWith(23, baseTime.Add(time.Hour)), nil },
	}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})
	if st.Source != schema.SourceOAuth {
		t.Fatalf("source = %s, want oauth", st.Source)
	}
	if len(st.Drift) != 1 || st.Drift[0] != wantDrift {
		t.Fatalf("live state Drift = %v, want %v", st.Drift, wantDrift)
	}
	if cache.drift == nil || len(cache.drift) != 1 || cache.drift[0] != wantDrift {
		t.Fatalf("cache did not persist drift: %+v", cache.drift)
	}

	st = resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})
	if st.Source != schema.SourceCache {
		t.Fatalf("source = %s, want cache (fresh serve, no refetch)", st.Source)
	}
	if len(st.Drift) != 1 || st.Drift[0] != wantDrift {
		t.Fatalf("cached state Drift = %v, want persisted drift", st.Drift)
	}
}

func TestNoDriftOnCleanFetch(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{}
	fetch := &fakeFetcher{
		probed: true,
		fn:     func(string) (*schema.Snapshot, error) { return snapWith(23, baseTime.Add(time.Hour)), nil },
	}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})
	if len(st.Drift) != 0 {
		t.Fatalf("clean fetch state Drift = %v, want none", st.Drift)
	}
}

func TestStaleCacheServeCarriesPersistedDrift(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	const wantDrift = "seven_day missing resets_at"
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(55, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-10 * time.Minute),
		drift:    []string{wantDrift},
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		return nil, usage.ErrTransient
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})
	if st.Source != schema.SourceCache || !st.Stale {
		t.Fatalf("got source=%s stale=%v, want cache/true", st.Source, st.Stale)
	}
	if len(st.Drift) != 1 || st.Drift[0] != wantDrift {
		t.Fatalf("stale state Drift = %v, want [%s]", st.Drift, wantDrift)
	}
}

func TestCleanFetchClearsPersistedDrift(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(55, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-10 * time.Minute),
		drift:    []string{"seven_day missing resets_at"},
		has:      true,
	}
	fetch := &fakeFetcher{
		probed: true,
		fn:     func(string) (*schema.Snapshot, error) { return snapWith(60, baseTime.Add(time.Hour)), nil },
	}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})
	if st.Source != schema.SourceOAuth {
		t.Fatalf("source = %s, want oauth", st.Source)
	}
	if len(st.Drift) != 0 {
		t.Fatalf("clean fetch state Drift = %v, want none", st.Drift)
	}
	if len(cache.drift) != 0 {
		t.Fatalf("cache still carries drift after a clean fetch: %v", cache.drift)
	}
}

var codexProvider = engine.Provider{Name: schema.ProviderCodex, LoginCommand: "codex login"}

func TestStatesCarryTheProviderName(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	fresh := &fakeCache{
		payload:  mustMarshal(t, snapWith(23, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-30 * time.Second),
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) { return nil, nil }}

	if st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: fresh}); st.Provider != schema.ProviderClaude {
		t.Fatalf("default provider = %q, want %q", st.Provider, schema.ProviderClaude)
	}

	missing := &fakeCreds{results: []credResult{{err: creds.ErrNotFound}}}
	st := resolve(t, engine.Options{Provider: codexProvider, Clock: clk, Creds: missing, Fetcher: fetch})
	if st.Provider != schema.ProviderCodex || st.Type != schema.TypeError {
		t.Fatalf("error state provider=%q type=%s, want codex/error", st.Provider, st.Type)
	}

	st = resolveStdin(t, engine.Options{Provider: codexProvider, Clock: clk}, snapWith(18, baseTime.Add(time.Hour)))
	if st.Provider != schema.ProviderCodex || st.Source != schema.SourceStdin {
		t.Fatalf("stdin state provider=%q source=%s, want codex/stdin", st.Provider, st.Source)
	}
}

func TestLoginHintsUseTheProviderLoginCommand(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) { return nil, nil }}

	missing := &fakeCreds{results: []credResult{{err: creds.ErrNotFound}}}
	st := resolve(t, engine.Options{Provider: codexProvider, Clock: clk, Creds: missing, Fetcher: fetch})
	if st.Auth != schema.AuthMissing || !strings.Contains(st.Error, "run `codex login` to log in") {
		t.Fatalf("missing-creds hint = %q (auth %s), want codex login hint", st.Error, st.Auth)
	}

	expired := &fakeCreds{results: []credResult{{c: &creds.Credentials{AccessToken: "old", ExpiresAt: baseTime.Add(-time.Minute)}}}}
	st = resolve(t, engine.Options{Provider: codexProvider, Clock: clk, Creds: expired, Fetcher: fetch})
	if st.Auth != schema.AuthExpired || !strings.Contains(st.Error, "run `codex login` to refresh it") {
		t.Fatalf("expired hint = %q (auth %s), want codex login hint", st.Error, st.Auth)
	}

	st = resolve(t, engine.Options{Clock: clk, Creds: missing, Fetcher: fetch})
	if !strings.Contains(st.Error, "run `claude` to log in") {
		t.Fatalf("default hint = %q, want claude login hint", st.Error)
	}
}

func TestCredentialSourceErrorBecomesTheAuthMissingReason(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	fetch := &fakeFetcher{fn: func(string) (*schema.Snapshot, error) {
		t.Fatalf("fetch must not be called without creds")
		return nil, nil
	}}
	reason := errors.New("codex: logged in with an API key; plan limits do not apply")

	st := resolve(t, engine.Options{Clock: clk, Creds: &fakeCreds{results: []credResult{{err: reason}}}, Fetcher: fetch})
	if st.Auth != schema.AuthMissing || st.Error != reason.Error() {
		t.Fatalf("got auth=%s error=%q, want missing with the source's reason", st.Auth, st.Error)
	}

	st = resolve(t, engine.Options{Clock: clk, Creds: &fakeCreds{results: []credResult{{err: creds.ErrNotAvailable}}}, Fetcher: fetch})
	if !strings.Contains(st.Error, "no credentials found") {
		t.Fatalf("ErrNotAvailable should keep the generic hint, got %q", st.Error)
	}
}
