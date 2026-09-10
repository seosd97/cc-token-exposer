package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

var ErrMiss = errors.New("cache: no entry")

var ErrCorrupt = errors.New("cache: entry is not decodable")

var ErrUnsafeDir = errors.New("cache: unsafe cache directory")

type Entry struct {
	FetchedAt    time.Time       `json:"fetched_at"`
	AttemptedAt  *time.Time      `json:"attempted_at,omitempty"`
	ScopedProbed bool            `json:"scoped_probed,omitempty"`
	Drift        []string        `json:"drift,omitempty"`
	LimitHit     json.RawMessage `json:"limit_hit,omitempty"`
	Payload      json.RawMessage `json:"payload"`
}

type Cache struct {
	path string
}

const DefaultName = "snapshot"

const dirName = "cc-token-exposer"

func DefaultPathFor(name string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", dirName, os.Getuid()), name+".json")
	}
	return filepath.Join(dir, dirName, name+".json")
}

func NewNamed(name string) *Cache {
	return Open(DefaultPathFor(name))
}

func Open(path string) *Cache {
	return &Cache{path: path}
}

func (c *Cache) Path() string { return c.path }

func (c *Cache) lockPath() string { return c.path + ".lock" }

func (c *Cache) Store(e Entry) error {
	if !json.Valid(e.Payload) {
		return errors.New("cache: payload is not valid JSON")
	}
	return c.withWriteLock(func() error { return c.writeEntryLocked(e) })
}

func (c *Cache) ClaimRefresh(now time.Time, backoff time.Duration) (bool, error) {
	claimed := false
	err := c.Update(func(e *Entry) (bool, error) {
		last := e.FetchedAt
		if e.AttemptedAt != nil && e.AttemptedAt.After(last) {
			last = *e.AttemptedAt
		}
		if !last.IsZero() && now.Sub(last) < backoff {
			return false, nil
		}
		at := now
		e.AttemptedAt = &at
		claimed = true
		return true, nil
	})
	return claimed, err
}

func (c *Cache) Update(fn func(e *Entry) (write bool, err error)) error {
	return c.withWriteLock(func() error {
		e, err := c.readEntryLocked()
		if err != nil {
			if !errors.Is(err, ErrMiss) && !errors.Is(err, ErrCorrupt) {
				return err
			}
			e = &Entry{}
		}
		write, err := fn(e)
		if err != nil || !write {
			return err
		}
		return c.writeEntryLocked(*e)
	})
}

func (c *Cache) Load() (*Entry, error) {
	if _, err := os.Stat(c.path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrMiss
		}
		return nil, fmt.Errorf("cache: stat: %w", err)
	}
	if err := c.checkDir(); err != nil {
		return nil, err
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
	if err := c.checkDir(); err != nil {
		return err
	}
	lock := flock.New(c.lockPath())
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("cache: acquire write lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}

func (c *Cache) checkDir() error {
	dir := filepath.Dir(c.path)
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("cache: stat dir: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrUnsafeDir, dir)
	}
	if !ownedByCaller(info) {
		return fmt.Errorf("%w: %s is owned by another user", ErrUnsafeDir, dir)
	}
	return nil
}

func (c *Cache) readEntryLocked() (*Entry, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrMiss
		}
		return nil, fmt.Errorf("cache: read: %w", err)
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
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
