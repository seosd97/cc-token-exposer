package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/seosd97/cc-token-exposer/internal/engine"
	"github.com/seosd97/cc-token-exposer/internal/provider"
	"github.com/seosd97/cc-token-exposer/internal/provider/claude"
	"github.com/seosd97/cc-token-exposer/internal/provider/codex"
	"github.com/seosd97/cc-token-exposer/internal/schema"
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

func productionProviders() providers {
	ps := providers{}
	for _, spec := range []provider.Spec{claude.Spec(), codex.Spec()} {
		ps[spec.Name] = providerEntry{
			spec: spec,
			resolver: engine.New(engine.Options{
				Spec:      spec,
				Cache:     cacheFor(spec.Name),
				Refresher: processRefresher{provider: spec.Name},
			}),
		}
	}
	return ps
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
