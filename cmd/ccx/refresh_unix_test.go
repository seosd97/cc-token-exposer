//go:build unix

package main

import (
	"context"
	"testing"
)

func TestProcessRefresherRejectsEmptyProvider(t *testing.T) {
	if err := (processRefresher{}).Spawn(context.Background()); err == nil {
		t.Fatal("an empty provider name must be a spawn error, not a claude refresh")
	}
}
