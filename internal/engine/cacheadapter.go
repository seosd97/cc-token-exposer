package engine

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/cache"
)

type cacheAdapter struct {
	c *cache.Cache
}

func CacheFrom(c *cache.Cache) Cache {
	if c == nil {
		return nil
	}
	return cacheAdapter{c: c}
}

func (a cacheAdapter) Load() (*CacheEntry, error) {
	e, err := a.c.Load()
	if err != nil {
		if errors.Is(err, cache.ErrMiss) {
			return nil, ErrNoCache
		}
		return nil, err
	}
	var attemptedAt time.Time
	if e.AttemptedAt != nil {
		attemptedAt = *e.AttemptedAt
	}
	return &CacheEntry{
		Payload:      []byte(e.Payload),
		StoredAt:     e.FetchedAt,
		AttemptedAt:  attemptedAt,
		ScopedProbed: e.ScopedProbed,
		Drift:        e.Drift,
	}, nil
}

func (a cacheAdapter) Store(e CacheEntry) error {
	return a.c.Store(json.RawMessage(e.Payload), e.StoredAt, e.ScopedProbed, e.Drift)
}

func (a cacheAdapter) ClaimRefresh(now time.Time, backoff time.Duration) (bool, error) {
	return a.c.ClaimRefresh(now, backoff)
}
