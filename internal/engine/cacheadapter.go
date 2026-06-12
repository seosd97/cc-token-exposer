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

func (a cacheAdapter) Load() ([]byte, time.Time, error) {
	e, err := a.c.Load()
	if err != nil {
		if errors.Is(err, cache.ErrMiss) {
			return nil, time.Time{}, ErrNoCache
		}
		return nil, time.Time{}, err
	}
	return []byte(e.Payload), e.FetchedAt, nil
}

func (a cacheAdapter) Store(payload []byte, storedAt time.Time) error {
	return a.c.Store(json.RawMessage(payload), storedAt)
}
