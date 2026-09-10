package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func tempCache(t *testing.T) *Cache {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.json")
	return Open(path)
}

func TestStoreLoadRoundTrip(t *testing.T) {
	c := tempCache(t)
	fetchedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{"type":"snapshot","five_hour":{"utilization":23}}`)

	if err := c.Store(payload, fetchedAt, false, nil); err != nil {
		t.Fatalf("Store: %v", err)
	}

	e, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !e.FetchedAt.Equal(fetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", e.FetchedAt, fetchedAt)
	}
	// Payload must round-trip byte-for-byte (semantically equal JSON).
	if !jsonEqual(t, e.Payload, payload) {
		t.Errorf("payload = %s, want %s", e.Payload, payload)
	}
}

func TestLoadMiss(t *testing.T) {
	c := tempCache(t)
	if _, err := c.Load(); err != ErrMiss {
		t.Fatalf("Load on empty cache = %v, want ErrMiss", err)
	}
}

func TestLoadMissWhenParentDirAbsent(t *testing.T) {
	// Loading a cache whose parent directory does not exist must be a fast
	// miss, never a lock- or mkdir-induced error (hot path on cold start).
	c := Open(filepath.Join(t.TempDir(), "missing", "snapshot.json"))
	if _, err := c.Load(); err != ErrMiss {
		t.Fatalf("Load with absent parent dir = %v, want ErrMiss", err)
	}
}

func TestClaimRefreshRecordsAttemptPreservingData(t *testing.T) {
	c := tempCache(t)
	fetchedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{"five_hour":{"utilization":23}}`)
	if err := c.Store(payload, fetchedAt, false, nil); err != nil {
		t.Fatalf("Store: %v", err)
	}

	claim := fetchedAt.Add(10 * time.Minute)
	got, err := c.ClaimRefresh(claim, time.Minute)
	if err != nil || !got {
		t.Fatalf("ClaimRefresh = %v, %v; want true, nil", got, err)
	}

	e, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !e.FetchedAt.Equal(fetchedAt) {
		t.Errorf("ClaimRefresh changed FetchedAt = %v, want %v preserved", e.FetchedAt, fetchedAt)
	}
	if !jsonEqual(t, e.Payload, payload) {
		t.Errorf("ClaimRefresh changed payload = %s, want %s preserved", e.Payload, payload)
	}
	if e.AttemptedAt == nil || !e.AttemptedAt.Equal(claim) {
		t.Errorf("AttemptedAt = %v, want %v", e.AttemptedAt, claim)
	}
}

func TestClaimRefreshWithoutEntryClaimsOnce(t *testing.T) {
	c := tempCache(t)
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	got, err := c.ClaimRefresh(now, time.Hour)
	if err != nil || !got {
		t.Fatalf("ClaimRefresh on empty cache = %v, %v; want true, nil", got, err)
	}
	e, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if e.AttemptedAt == nil || !e.AttemptedAt.Equal(now) {
		t.Errorf("AttemptedAt = %v, want %v", e.AttemptedAt, now)
	}
}

func TestClaimRefreshThrottlesWithinBackoff(t *testing.T) {
	c := tempCache(t)
	t0 := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	const backoff = 120 * time.Second

	if got, err := c.ClaimRefresh(t0, backoff); err != nil || !got {
		t.Fatalf("first claim = %v, %v; want true", got, err)
	}
	if got, err := c.ClaimRefresh(t0.Add(time.Second), backoff); err != nil || got {
		t.Fatalf("second claim within backoff = %v, %v; want false", got, err)
	}
	if got, err := c.ClaimRefresh(t0.Add(backoff), backoff); err != nil || !got {
		t.Fatalf("claim after backoff = %v, %v; want true", got, err)
	}
	// A fresh data store becomes the later of the two and throttles a claim.
	if err := c.Store(json.RawMessage(`{"v":1}`), t0.Add(2*backoff-20*time.Second), false, nil); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if got, err := c.ClaimRefresh(t0.Add(2*backoff), backoff); err != nil || got {
		t.Fatalf("claim gated on fresh fetched_at = %v, %v; want false", got, err)
	}
}

// TestClaimRefreshConcurrent hammers ClaimRefresh from many goroutines at the
// same instant and asserts exactly one claim wins — the cross-process (here
// cross-goroutine) dedup that bounds refreshes to ≤1 per backoff.
func TestClaimRefreshConcurrent(t *testing.T) {
	c := tempCache(t)
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	const workers = 16

	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := c.ClaimRefresh(now, time.Hour)
			if err != nil {
				t.Errorf("ClaimRefresh: %v", err)
				return
			}
			if got {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("concurrent claims = %d, want exactly 1 winner", wins)
	}
}

func TestStoreRejectsInvalidJSON(t *testing.T) {
	c := tempCache(t)
	if err := c.Store(json.RawMessage(`{not json`), time.Now(), false, nil); err == nil {
		t.Fatalf("Store accepted invalid JSON, want error")
	}
	// Nothing should have been written.
	if _, err := c.Load(); err != ErrMiss {
		t.Errorf("Load = %v, want ErrMiss after rejected store", err)
	}
}

// TestNoTokenInCacheFile asserts the cache is a transparent passthrough: the
// on-disk file contains only the caller's payload plus the two wrapper keys,
// and never any value the caller did not put there. This is the structural
// guarantee that a token cannot be persisted by the cache layer itself.
func TestNoTokenInCacheFile(t *testing.T) {
	c := tempCache(t)
	const token = "sk-ant-oat-SUPER-SECRET-TOKEN"
	// A realistic, token-free State payload.
	payload := json.RawMessage(`{"schema_version":1,"type":"snapshot","auth":"ok","five_hour":{"utilization":23}}`)
	if err := c.Store(payload, time.Now(), false, nil); err != nil {
		t.Fatalf("Store: %v", err)
	}

	raw, err := os.ReadFile(c.Path())
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatalf("token leaked into cache file")
	}

	// On-disk top-level keys must be exactly {fetched_at, payload}.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("decode cache file: %v", err)
	}
	if len(top) != 2 {
		t.Errorf("cache file has %d top-level keys, want 2: %v", len(top), keys(top))
	}
	if _, ok := top["fetched_at"]; !ok {
		t.Errorf("missing fetched_at key")
	}
	if _, ok := top["payload"]; !ok {
		t.Errorf("missing payload key")
	}
}

// TestScopedProbedRoundTrip asserts the provenance flag is persisted at the
// entry level (never inside the opaque payload) and round-trips through
// Store/Load.
func TestScopedProbedRoundTrip(t *testing.T) {
	c := tempCache(t)
	fetchedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{"five_hour":{"utilization":23}}`)

	if err := c.Store(payload, fetchedAt, true, nil); err != nil {
		t.Fatalf("Store: %v", err)
	}
	e, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !e.ScopedProbed {
		t.Errorf("ScopedProbed = false, want true")
	}
	if strings.Contains(string(e.Payload), "scoped_probed") {
		t.Errorf("scoped_probed leaked into the payload: %s", e.Payload)
	}

	if err := c.Store(payload, fetchedAt, false, nil); err != nil {
		t.Fatalf("Store(false): %v", err)
	}
	e, err = c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if e.ScopedProbed {
		t.Errorf("ScopedProbed = true, want false after Store(false)")
	}
}

func TestStoreOverwrites(t *testing.T) {
	c := tempCache(t)
	t0 := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	if err := c.Store(json.RawMessage(`{"v":1}`), t0, false, nil); err != nil {
		t.Fatalf("Store1: %v", err)
	}
	t1 := t0.Add(time.Minute)
	if err := c.Store(json.RawMessage(`{"v":2}`), t1, false, nil); err != nil {
		t.Fatalf("Store2: %v", err)
	}
	e, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !jsonEqual(t, e.Payload, json.RawMessage(`{"v":2}`)) {
		t.Errorf("payload = %s, want {\"v\":2}", e.Payload)
	}
	if !e.FetchedAt.Equal(t1) {
		t.Errorf("FetchedAt = %v, want %v", e.FetchedAt, t1)
	}
}

// TestConcurrentAccess hammers the cache from many goroutines. With flock +
// atomic rename, every Load must see a complete, valid entry whose payload is
// one of the written values (never a torn write).
func TestConcurrentAccess(t *testing.T) {
	c := tempCache(t)
	base := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	// Seed so early readers don't all miss.
	if err := c.Store(json.RawMessage(`{"writer":0,"i":0}`), base, false, nil); err != nil {
		t.Fatalf("seed Store: %v", err)
	}

	const writers, readers, iters = 4, 4, 60
	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				p := json.RawMessage(`{"writer":` + strconv.Itoa(w) + `,"i":` + strconv.Itoa(i) + `}`)
				if err := c.Store(p, base.Add(time.Duration(i)*time.Second), false, nil); err != nil {
					t.Errorf("writer %d Store: %v", w, err)
					return
				}
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				e, err := c.Load()
				if err != nil {
					t.Errorf("Load: %v", err)
					return
				}
				// Payload must always be valid JSON (no torn writes).
				var obj map[string]int
				if err := json.Unmarshal(e.Payload, &obj); err != nil {
					t.Errorf("torn/invalid payload %q: %v", e.Payload, err)
					return
				}
			}
		}()
	}

	wg.Wait()
}

// --- helpers ---

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal b: %v", err)
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
