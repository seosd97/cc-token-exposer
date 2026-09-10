package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

var ErrMiss = errors.New("cache: no entry")

type Entry struct {
	FetchedAt    time.Time       `json:"fetched_at"`
	AttemptedAt  *time.Time      `json:"attempted_at,omitempty"`
	ScopedProbed bool            `json:"scoped_probed,omitempty"`
	Drift        []string        `json:"drift,omitempty"`
	Payload      json.RawMessage `json:"payload"`
}

type Cache struct {
	path string
}

const DefaultName = "snapshot"

func DefaultPath() (string, error) { return DefaultPathFor(DefaultName) }

func DefaultPathFor(name string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("cache: resolve cache dir: %w", err)
	}
	return filepath.Join(dir, "cc-token-exposer", name+".json"), nil
}

func New() (*Cache, error) { return NewNamed(DefaultName) }

func NewNamed(name string) (*Cache, error) {
	path, err := DefaultPathFor(name)
	if err != nil {
		return nil, err
	}
	return Open(path), nil
}

func Open(path string) *Cache {
	return &Cache{path: path}
}

func (c *Cache) Path() string { return c.path }

func (c *Cache) lockPath() string { return c.path + ".lock" }

func (c *Cache) Store(payload json.RawMessage, fetchedAt time.Time, scopedProbed bool, drift []string) error {
	if !json.Valid(payload) {
		return errors.New("cache: payload is not valid JSON")
	}
	return c.withWriteLock(func() error {
		return c.writeEntryLocked(Entry{FetchedAt: fetchedAt, ScopedProbed: scopedProbed, Drift: drift, Payload: payload})
	})
}

func (c *Cache) ClaimRefresh(now time.Time, backoff time.Duration) (bool, error) {
	var claimed bool
	err := c.withWriteLock(func() error {
		e, err := c.readEntryLocked()
		if err != nil {
			if !errors.Is(err, ErrMiss) {
				return err
			}
			claimed = true
			at := now
			return c.writeEntryLocked(Entry{AttemptedAt: &at, Payload: json.RawMessage("null")})
		}
		last := e.FetchedAt
		if e.AttemptedAt != nil && e.AttemptedAt.After(last) {
			last = *e.AttemptedAt
		}
		if !last.IsZero() && now.Sub(last) < backoff {
			return nil
		}
		claimed = true
		at := now
		e.AttemptedAt = &at
		return c.writeEntryLocked(*e)
	})
	return claimed, err
}

func (c *Cache) Load() (*Entry, error) {
	if _, err := os.Stat(c.path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrMiss
		}
		return nil, fmt.Errorf("cache: stat: %w", err)
	}

	lock := flock.New(c.lockPath())
	if err := lock.RLock(); err != nil {
		return nil, fmt.Errorf("cache: acquire read lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()

	return c.readEntryLocked()
}

func (c *Cache) withWriteLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("cache: create dir: %w", err)
	}
	lock := flock.New(c.lockPath())
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("cache: acquire write lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}

func (c *Cache) readEntryLocked() (*Entry, error) {
	f, err := os.Open(c.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrMiss
		}
		return nil, fmt.Errorf("cache: open: %w", err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("cache: read: %w", err)
	}

	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("cache: decode entry: %w", err)
	}
	return &e, nil
}

func (c *Cache) writeEntryLocked(e Entry) error {
	dir := filepath.Dir(c.path)
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("cache: marshal entry: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("cache: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cache: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cache: close temp: %w", err)
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		return fmt.Errorf("cache: rename temp: %w", err)
	}
	return nil
}
