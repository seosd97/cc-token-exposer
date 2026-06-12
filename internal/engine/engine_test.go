package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/creds"
	"github.com/seosd97/cc-token-exposer/internal/engine"
	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/seosd97/cc-token-exposer/internal/usage"
)

var baseTime = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

// --- fakes -----------------------------------------------------------------

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
	fn    func(token string) (*usage.Snapshot, error)
	calls []string
}

func (f *fakeFetcher) Fetch(_ context.Context, token string) (*usage.Snapshot, error) {
	f.calls = append(f.calls, token)
	return f.fn(token)
}

type fakeCache struct {
	payload    []byte
	storedAt   time.Time
	has        bool
	stores     int
	lastStored []byte
}

func (c *fakeCache) Load() ([]byte, time.Time, error) {
	if !c.has {
		return nil, time.Time{}, engine.ErrNoCache
	}
	return c.payload, c.storedAt, nil
}

func (c *fakeCache) Store(payload []byte, storedAt time.Time) error {
	c.stores++
	c.lastStored = payload
	c.payload = payload
	c.storedAt = storedAt
	c.has = true
	return nil
}

type fakeTranscript struct {
	lh *schema.LimitHit
}

func (f *fakeTranscript) Probe(time.Time) (*schema.LimitHit, error) { return f.lh, nil }

// --- helpers ---------------------------------------------------------------

func okCreds(token string) *fakeCreds {
	return &fakeCreds{results: []credResult{{c: &creds.Credentials{AccessToken: token}}}}
}

func snapWith(util float64, resetsAt time.Time) *usage.Snapshot {
	return &usage.Snapshot{
		FetchedAt: baseTime,
		FiveHour:  &usage.Window{Utilization: util, ResetsAt: resetsAt},
	}
}

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

// --- tests -----------------------------------------------------------------

func TestFreshCacheServedWithoutFetch(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cache := &fakeCache{
		payload:  mustMarshal(t, snapWith(23, baseTime.Add(time.Hour))),
		storedAt: baseTime.Add(-30 * time.Second), // within 120s TTL
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) {
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
		storedAt: baseTime.Add(-10 * time.Minute), // older than TTL
		has:      true,
	}
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) {
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
	// Fresh value drops 40 points within the same (not-yet-reset) window.
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) {
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
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) {
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
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) {
		return nil, &usage.RateLimitError{RetryAfter: time.Minute, StatusCode: 429}
	}}

	st := resolve(t, engine.Options{Clock: clk, Creds: okCreds("tok"), Fetcher: fetch, Cache: cache})
	if st.Source != schema.SourceCache || !st.Stale {
		t.Fatalf("got source=%s stale=%v, want cache/true", st.Source, st.Stale)
	}
}

func TestTransientErrorNoCacheIsError(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) {
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
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) {
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
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) { return nil, nil }}

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
	fetch := &fakeFetcher{fn: func(token string) (*usage.Snapshot, error) {
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
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) {
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
	fetch := &fakeFetcher{fn: func(token string) (*usage.Snapshot, error) {
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
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) {
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
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) { return nil, nil }}
	tr := &fakeTranscript{lh: &schema.LimitHit{ResetsAt: &reset, Message: "session limit", DetectedAt: baseTime}}

	st := resolve(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch, Transcript: tr})
	if st.Source != schema.SourceTranscript || st.LimitHit == nil {
		t.Fatalf("got source=%s limitHit=%v, want transcript/non-nil", st.Source, st.LimitHit)
	}
	if st.Auth != schema.AuthMissing {
		t.Fatalf("auth = %s, want missing", st.Auth)
	}
}

func TestErrorStateCarriesNoSnapshot(t *testing.T) {
	clk := &fakeClock{t: baseTime}
	cr := &fakeCreds{results: []credResult{{err: errors.New("boom")}}}
	fetch := &fakeFetcher{fn: func(string) (*usage.Snapshot, error) { return nil, nil }}

	st := resolve(t, engine.Options{Clock: clk, Creds: cr, Fetcher: fetch})
	if st.Snapshot != nil {
		t.Fatalf("error state should have no snapshot")
	}
}
