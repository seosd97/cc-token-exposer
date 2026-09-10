package cache

import (
	"encoding/json"
	"errors"
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

	if err := c.Store(Entry{Payload: payload, FetchedAt: fetchedAt}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	e, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !e.FetchedAt.Equal(fetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", e.FetchedAt, fetchedAt)
	}
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
	c := Open(filepath.Join(t.TempDir(), "missing", "snapshot.json"))
	if _, err := c.Load(); err != ErrMiss {
		t.Fatalf("Load with absent parent dir = %v, want ErrMiss", err)
	}
}

func TestClaimRefreshRecordsAttemptPreservingData(t *testing.T) {
	c := tempCache(t)
	fetchedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{"five_hour":{"utilization":23}}`)
	if err := c.Store(Entry{Payload: payload, FetchedAt: fetchedAt}); err != nil {
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
	if err := c.Store(Entry{Payload: json.RawMessage(`{"v":1}`), FetchedAt: t0.Add(2*backoff - 20*time.Second)}); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if got, err := c.ClaimRefresh(t0.Add(2*backoff), backoff); err != nil || got {
		t.Fatalf("claim gated on fresh fetched_at = %v, %v; want false", got, err)
	}
}

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
	if err := c.Store(Entry{Payload: json.RawMessage(`{not json`), FetchedAt: time.Now()}); err == nil {
		t.Fatalf("Store accepted invalid JSON, want error")
	}
	if _, err := c.Load(); err != ErrMiss {
		t.Errorf("Load = %v, want ErrMiss after rejected store", err)
	}
}

func TestNoTokenInCacheFile(t *testing.T) {
	c := tempCache(t)
	const token = "sk-ant-oat-SUPER-SECRET-TOKEN"
	payload := json.RawMessage(`{"schema_version":1,"type":"snapshot","auth":"ok","five_hour":{"utilization":23}}`)
	if err := c.Store(Entry{Payload: payload, FetchedAt: time.Now()}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	raw, err := os.ReadFile(c.Path())
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatalf("token leaked into cache file")
	}

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

func TestScopedProbedRoundTrip(t *testing.T) {
	c := tempCache(t)
	fetchedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{"five_hour":{"utilization":23}}`)

	if err := c.Store(Entry{Payload: payload, FetchedAt: fetchedAt, ScopedProbed: true}); err != nil {
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

	if err := c.Store(Entry{Payload: payload, FetchedAt: fetchedAt}); err != nil {
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
	if err := c.Store(Entry{Payload: json.RawMessage(`{"v":1}`), FetchedAt: t0}); err != nil {
		t.Fatalf("Store1: %v", err)
	}
	t1 := t0.Add(time.Minute)
	if err := c.Store(Entry{Payload: json.RawMessage(`{"v":2}`), FetchedAt: t1}); err != nil {
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

func TestConcurrentAccess(t *testing.T) {
	c := tempCache(t)
	base := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	if err := c.Store(Entry{Payload: json.RawMessage(`{"writer":0,"i":0}`), FetchedAt: base}); err != nil {
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
				if err := c.Store(Entry{Payload: p, FetchedAt: base.Add(time.Duration(i) * time.Second)}); err != nil {
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

func TestDefaultPathForKeepsProvidersInSeparateFiles(t *testing.T) {
	legacy := DefaultPathFor(DefaultName)
	if filepath.Base(legacy) != "snapshot.json" {
		t.Fatalf("legacy cache file = %s, want snapshot.json", legacy)
	}
	codex := DefaultPathFor("codex")
	if filepath.Base(codex) != "codex.json" || filepath.Dir(codex) != filepath.Dir(legacy) {
		t.Fatalf("named cache = %s, want codex.json beside %s", codex, legacy)
	}
}

func TestDefaultPathForFallsBackToTempDirWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	got := DefaultPathFor("codex")
	if !strings.HasPrefix(got, os.TempDir()) || filepath.Base(got) != "codex.json" {
		t.Fatalf("fallback path = %s, want codex.json under %s", got, os.TempDir())
	}
	if NewNamed("codex").Path() != got {
		t.Fatalf("NewNamed must open the same fallback path")
	}
}

func TestUpdateRepairsCorruptEntry(t *testing.T) {
	fetchedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{"five_hour":{"utilization":23}}`)
	for _, corrupt := range []string{"", "{garbage", `{"payload": 1, "fetched_at": "nope"}`} {
		t.Run(strconv.Quote(corrupt), func(t *testing.T) {
			c := tempCache(t)
			if err := os.WriteFile(c.Path(), []byte(corrupt), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Load(); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Load on a corrupt entry = %v, want ErrCorrupt", err)
			}
			if err := c.Store(Entry{Payload: payload, FetchedAt: fetchedAt}); err != nil {
				t.Fatalf("Store must repair a corrupt entry, got %v", err)
			}
			e, err := c.Load()
			if err != nil || !e.FetchedAt.Equal(fetchedAt) || !jsonEqual(t, e.Payload, payload) {
				t.Fatalf("Load after repair = %+v, %v; want the stored entry", e, err)
			}
		})
	}
}

func TestClaimRefreshRepairsCorruptEntry(t *testing.T) {
	c := tempCache(t)
	if err := os.WriteFile(c.Path(), []byte("{garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	got, err := c.ClaimRefresh(now, time.Hour)
	if err != nil || !got {
		t.Fatalf("ClaimRefresh on a corrupt entry = %v, %v; want true, nil", got, err)
	}
	e, err := c.Load()
	if err != nil || e.AttemptedAt == nil || !e.AttemptedAt.Equal(now) {
		t.Fatalf("Load after the claim = %+v, %v; want the claim persisted in a repaired entry", e, err)
	}
}

func TestUpdateKeepsReadErrorsThatAreNotCorruption(t *testing.T) {
	c := Open(filepath.Join(t.TempDir(), "snapshot.json"))
	if err := os.Mkdir(c.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	err := c.Store(Entry{Payload: json.RawMessage(`{}`), FetchedAt: time.Now()})
	if err == nil || errors.Is(err, ErrCorrupt) || errors.Is(err, ErrMiss) {
		t.Fatalf("Store over an unreadable path = %v, want a read error that is neither a miss nor corruption", err)
	}
}

func TestUnsafeCacheDirIsRefused(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	entry := Entry{Payload: json.RawMessage(`{}`), FetchedAt: time.Now()}
	if err := Open(filepath.Join(real, "snapshot.json")).Store(entry); err != nil {
		t.Fatalf("Store through the real dir: %v", err)
	}
	c := Open(filepath.Join(link, "snapshot.json"))
	if err := c.Store(entry); !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("Store through a symlinked dir = %v, want ErrUnsafeDir", err)
	}
	if _, err := c.Load(); !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("Load through a symlinked dir = %v, want ErrUnsafeDir", err)
	}
}
