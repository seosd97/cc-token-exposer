package main

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/cache"
	"github.com/seosd97/cc-token-exposer/internal/engine"
	"github.com/seosd97/cc-token-exposer/internal/schema"
)

type cacheAdapter struct {
	c *cache.Cache
}

func cacheFor(providerName string) engine.Cache {
	return cacheAdapter{c: cache.NewNamed(cacheNameFor(providerName))}
}

func cacheNameFor(providerName string) string {
	if providerName == schema.ProviderClaude {
		return cache.DefaultName
	}
	return providerName
}

func (a cacheAdapter) Load() (*engine.CacheEntry, error) {
	e, err := a.c.Load()
	if err != nil {
		if errors.Is(err, cache.ErrMiss) {
			return nil, nil
		}
		return nil, err
	}
	out := &engine.CacheEntry{
		Payload:      []byte(e.Payload),
		StoredAt:     e.FetchedAt,
		ScopedProbed: e.ScopedProbed,
		Drift:        e.Drift,
	}
	if e.AttemptedAt != nil {
		out.AttemptedAt = *e.AttemptedAt
	}
	if len(e.LimitHit) > 0 {
		var lh schema.LimitHit
		if json.Unmarshal(e.LimitHit, &lh) == nil && !lh.DetectedAt.IsZero() {
			out.LimitHit = &lh
		}
	}
	return out, nil
}

func (a cacheAdapter) Store(e engine.CacheEntry) error {
	return a.c.Store(cache.Entry{
		FetchedAt:    e.StoredAt,
		ScopedProbed: e.ScopedProbed,
		Drift:        e.Drift,
		Payload:      json.RawMessage(e.Payload),
	})
}

func (a cacheAdapter) ClaimRefresh(now time.Time, backoff time.Duration) (bool, error) {
	return a.c.ClaimRefresh(now, backoff)
}

func (a cacheAdapter) StoreLimitHit(lh *schema.LimitHit) error {
	var raw json.RawMessage
	if lh != nil {
		b, err := json.Marshal(lh)
		if err != nil {
			return err
		}
		raw = b
	}
	return a.c.Update(func(e *cache.Entry) (bool, error) {
		if len(e.LimitHit) == 0 && raw == nil {
			return false, nil
		}
		e.LimitHit = raw
		return true, nil
	})
}
