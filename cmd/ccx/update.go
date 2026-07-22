package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/selfupdate"
	"github.com/spf13/cobra"
)

const (
	updateTimeout = 60 * time.Second

	repoOwner = "seosd97"
	repoName  = "cc-token-exposer"
)

func newUpdateCmd(up *selfupdate.Client) *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update ccx to the latest GitHub release",
		Long: "Check the latest GitHub release and, when newer, download the matching " +
			"binary, verify its checksum, and replace the running executable. Use " +
			"--check to only report whether a newer version exists. Homebrew installs " +
			"are left to `brew upgrade`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), updateTimeout)
			defer cancel()
			out := cmd.OutOrStdout()
			current := versionString()

			rel, err := up.Latest(ctx)
			if err != nil {
				return err
			}

			if !selfupdate.IsNewer(rel.Tag, current) {
				fmt.Fprintf(out, "already up to date (%s)\n", current)
				return nil
			}
			if checkOnly {
				fmt.Fprintf(out, "new version available: %s (current %s)\n", rel.Tag, current)
				return nil
			}

			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locate executable: %w", err)
			}
			if selfupdate.IsHomebrew(exe) {
				fmt.Fprintf(out, "installed via Homebrew — run `brew upgrade ccx` to update to %s (current %s)\n", rel.Tag, current)
				return nil
			}

			asset := up.BinaryAsset()
			fmt.Fprintf(out, "downloading %s ...\n", asset)
			targz, err := up.FetchAsset(ctx, rel, asset)
			if err != nil {
				return err
			}
			sums, err := up.FetchAsset(ctx, rel, selfupdate.ChecksumsAsset)
			if err != nil {
				return err
			}
			if err := selfupdate.VerifyChecksum(targz, asset, sums); err != nil {
				return err
			}
			fmt.Fprintln(out, "verifying checksum ... ok")

			bin, err := selfupdate.ExtractBinary(targz)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "replacing %s ...\n", exe)
			if err := selfupdate.Apply(bin, exe); err != nil {
				return err
			}
			fmt.Fprintf(out, "updated %s -> %s\n", current, rel.Tag)
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only report whether a newer version exists")
	return cmd
}

func defaultUpdater() *selfupdate.Client {
	return selfupdate.New(repoOwner, repoName, selfupdate.WithPlatform(runtime.GOOS, runtime.GOARCH))
}
