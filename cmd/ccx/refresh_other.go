//go:build !unix

package main

import (
	"context"
	"errors"
)

// processRefresher is a no-op on platforms without Unix Setsid semantics; the
// statusline path falls back to a synchronous refresh when spawning fails.
type processRefresher struct{}

func (processRefresher) Spawn(ctx context.Context) error {
	return errors.New("processRefresher: background refresh unsupported on this platform")
}
