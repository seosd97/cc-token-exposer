//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

type processRefresher struct{ provider string }

func (p processRefresher) Spawn(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	name := p.provider
	if name == "" {
		name = schema.ProviderClaude
	}
	cmd := exec.Command(exe, "refresh", "--provider", name)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
