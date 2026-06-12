// Ccg tracks Claude Pro/Max plan credit-limit windows for the CLI and the
// Claude Code statusline.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/seosd97/cc-token-exposer/internal/cache"
	"github.com/seosd97/cc-token-exposer/internal/creds"
	"github.com/seosd97/cc-token-exposer/internal/engine"
	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/seosd97/cc-token-exposer/internal/transcript"
	"github.com/seosd97/cc-token-exposer/internal/usage"
	"github.com/spf13/cobra"
)

var version = "dev"

var errSilentExit = errors.New("")

type resolver interface {
	Resolve(ctx context.Context) *schema.State
}

func versionString() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}

func openCache() *cache.Cache {
	c, err := cache.New()
	if err != nil {
		return nil
	}
	return c
}

func main() {
	eng := engine.New(engine.Options{
		Creds:      creds.Default(),
		Fetcher:    usage.New(),
		Cache:      engine.CacheFrom(openCache()),
		Transcript: transcript.NewProbe(),
	})

	root := &cobra.Command{
		Use:           "ccx",
		Short:         "Claude plan credit-limit window tracker",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(versionString())
		},
	})

	root.AddCommand(newNowCmd(eng))
	root.AddCommand(newStatuslineCmd(eng))

	if err := root.Execute(); err != nil {
		if !errors.Is(err, errSilentExit) {
			fmt.Fprintln(os.Stderr, "ccx:", err)
		}
		os.Exit(1)
	}
}
