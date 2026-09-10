package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/seosd97/cc-token-exposer/internal/cache"
	"github.com/seosd97/cc-token-exposer/internal/codex"
	"github.com/seosd97/cc-token-exposer/internal/creds"
	"github.com/seosd97/cc-token-exposer/internal/engine"
	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/seosd97/cc-token-exposer/internal/transcript"
	"github.com/seosd97/cc-token-exposer/internal/usage"
	"github.com/spf13/cobra"
)

var version = "dev"

var errSilentExit = errors.New("error state already printed to stdout")

type resolver interface {
	Resolve(ctx context.Context) *schema.State
	ResolveStdin(ctx context.Context, stdin *schema.Snapshot) *schema.State
	ResolveDetached(ctx context.Context) *schema.State
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

func openCache(name string) *cache.Cache {
	c, err := cache.NewNamed(name)
	if err != nil {
		return nil
	}
	return c
}

func productionProviders() providers {
	return providers{
		schema.ProviderClaude: engine.New(engine.Options{
			Provider:   engine.ClaudeProvider,
			Creds:      creds.Default(),
			Fetcher:    usage.New(),
			Cache:      engine.CacheFrom(openCache(cache.DefaultName)),
			Transcript: transcript.NewProbe(),
			Refresher:  processRefresher{provider: schema.ProviderClaude},
		}),
		schema.ProviderCodex: engine.New(engine.Options{
			Provider:  engine.Provider{Name: schema.ProviderCodex, LoginCommand: "codex login"},
			Creds:     creds.NewResolver(&codex.AuthSource{}),
			Fetcher:   codex.New(),
			Cache:     engine.CacheFrom(openCache(schema.ProviderCodex)),
			Refresher: processRefresher{provider: schema.ProviderCodex},
		}),
	}
}

func main() {
	ps := productionProviders()

	root := &cobra.Command{
		Use:           "ccx",
		Short:         "Claude and Codex plan credit-limit window tracker",
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

	root.AddCommand(newNowCmd(ps))
	root.AddCommand(newStatuslineCmd(ps))
	root.AddCommand(newRefreshCmd(ps))
	root.AddCommand(newUpdateCmd(defaultUpdater()))

	if err := root.Execute(); err != nil {
		if !errors.Is(err, errSilentExit) {
			fmt.Fprintln(os.Stderr, "ccx:", err)
		}
		os.Exit(1)
	}
}
