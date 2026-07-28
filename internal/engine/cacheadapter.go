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

func (a cacheAdapter) Load() ([]byte, time.Time, time.Time, error) {
	e, err := a.c.Load()
	if err != nil {
		if errors.Is(err, cache.ErrMiss) {
			return nil, time.Time{}, time.Time{}, ErrNoCache
		}
		return nil, time.Time{}, time.Time{}, err
	}
	var attemptedAt time.Time
	if e.AttemptedAt != nil {
		attemptedAt = *e.AttemptedAt
	}
	return []byte(e.Payload), e.FetchedAt, attemptedAt, nil
}

func (a cacheAdapter) Store(payload []byte, storedAt time.Time) error {
	return a.c.Store(json.RawMessage(payload), storedAt)
}

func (a cacheAdapter) Touch(attemptedAt time.Time) error {
	return a.c.Touch(attemptedAt)
}
