package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/cache"
	"github.com/seosd97/cc-token-exposer/internal/engine"
	"github.com/seosd97/cc-token-exposer/internal/provider"
	"github.com/seosd97/cc-token-exposer/internal/schema"
)

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

func TestCacheAdapterLimitHitRoundTrip(t *testing.T) {
	a := cacheAdapter{c: cache.Open(filepath.Join(t.TempDir(), "snapshot.json"))}
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	if e, err := a.Load(); e != nil || err != nil {
		t.Fatalf("Load on a miss = %+v, %v; want nil, nil", e, err)
	}
	if err := a.StoreLimitHit(nil); err != nil {
		t.Fatalf("StoreLimitHit(nil) on a miss: %v", err)
	}
	if e, err := a.Load(); e != nil || err != nil {
		t.Fatalf("clearing a hit on a miss must not create an entry, got %+v, %v", e, err)
	}

	reset := now.Add(2 * time.Hour)
	hit := &schema.LimitHit{ResetsAt: &reset, Message: "session limit", DetectedAt: now}
	if err := a.StoreLimitHit(hit); err != nil {
		t.Fatalf("StoreLimitHit: %v", err)
	}
	e, err := a.Load()
	if err != nil || e == nil || e.LimitHit == nil || !e.LimitHit.ResetsAt.Equal(reset) || e.LimitHit.Message != hit.Message {
		t.Fatalf("Load after StoreLimitHit = %+v, %v; want the hit persisted", e, err)
	}

	st := engine.New(engine.Options{Spec: provider.Spec{Name: schema.ProviderClaude}, Cache: a, Clock: fixedClock(now)}).ResolveDetached(context.Background())
	if st.Source != schema.SourceTranscript || st.LimitHit == nil || st.Snapshot != nil {
		t.Fatalf("the detached ladder must serve the persisted hit as a transcript state without a snapshot, got %+v", st)
	}

	if err := a.Store(engine.CacheEntry{Payload: []byte(`{"five_hour":{"utilization":1}}`), StoredAt: now}); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if e, err = a.Load(); err != nil || e == nil || e.LimitHit != nil || len(e.Payload) == 0 {
		t.Fatalf("a stored snapshot must replace the entry and clear the hit, got %+v, %v", e, err)
	}

	if err := a.StoreLimitHit(hit); err != nil {
		t.Fatal(err)
	}
	if err := a.StoreLimitHit(nil); err != nil {
		t.Fatal(err)
	}
	if e, err = a.Load(); err != nil || e == nil || e.LimitHit != nil || len(e.Payload) == 0 {
		t.Fatalf("clearing a hit must keep the snapshot payload, got %+v, %v", e, err)
	}
}

func TestCacheAdapterIgnoresDegenerateLimitHit(t *testing.T) {
	for _, raw := range []string{"null", "{}"} {
		c := cache.Open(filepath.Join(t.TempDir(), "snapshot.json"))
		if err := c.Update(func(e *cache.Entry) (bool, error) {
			e.LimitHit = json.RawMessage(raw)
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
		e, err := cacheAdapter{c: c}.Load()
		if err != nil || e == nil || e.LimitHit != nil {
			t.Fatalf("limit_hit %s: Load = %+v, %v; want the degenerate hit ignored", raw, e, err)
		}
	}
}
