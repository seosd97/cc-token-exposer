//go:build !unix

package main

import (
	"context"
	"errors"
)

type processRefresher struct{ provider string }

func (processRefresher) Spawn(ctx context.Context) error {
	return errors.New("processRefresher: background refresh unsupported on this platform")
}
