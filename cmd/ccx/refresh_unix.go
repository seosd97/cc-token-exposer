//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// processRefresher spawns a detached `ccx refresh` child so the statusline
// path never waits on the network. Setsid detaches the child from any
// controlling terminal/session so it survives this process exiting.
type processRefresher struct{}

func (processRefresher) Spawn(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe, "refresh")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
