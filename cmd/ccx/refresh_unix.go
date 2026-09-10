//go:build unix

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type processRefresher struct{ provider string }

func (p processRefresher) Spawn(ctx context.Context) error {
	if p.provider == "" {
		return errors.New("processRefresher: provider name is empty")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "refresh", "--provider", p.provider)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
