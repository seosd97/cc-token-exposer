//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

type processRefresher struct{}

func (processRefresher) Spawn(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "refresh")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
