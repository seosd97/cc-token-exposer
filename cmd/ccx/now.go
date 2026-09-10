package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/spf13/cobra"
)

const nowTimeout = 15 * time.Second

func newNowCmd(ps providers) *cobra.Command {
	var asJSON bool
	var providerFlag string
	cmd := &cobra.Command{
		Use:   "now",
		Short: "Show current plan usage once",
		Long: "Resolve and print the current plan credit-limit windows a single time. " +
			"Reads the disk cache when fresh and only calls the usage API when the " +
			"cache TTL has elapsed. With several providers (--provider claude,codex) " +
			"each one is printed as its own block, or as one JSON line each.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			names := parseProviderList(providerFlag)
			resolvers, err := ps.lookup(names)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), nowTimeout)
			defer cancel()
			states := resolveAll(ctx, resolvers)

			out := cmd.OutOrStdout()
			now := time.Now().UTC()
			failed := false
			for i, st := range states {
				if asJSON {
					if err := json.NewEncoder(out).Encode(st); err != nil {
						return err
					}
				} else {
					if len(states) > 1 {
						if i > 0 {
							fmt.Fprintln(out)
						}
						fmt.Fprintln(out, names[i])
					}
					fmt.Fprint(out, renderHuman(st, now))
				}
				if st == nil || st.Type == schema.TypeError {
					failed = true
				}
			}
			if failed {
				return errSilentExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the State as a single JSON line (one line per provider)")
	addProviderFlag(cmd, &providerFlag, "provider(s) to query, comma-separated (claude, codex)")
	return cmd
}

func resolveAll(ctx context.Context, resolvers []resolver) []*schema.State {
	states := make([]*schema.State, len(resolvers))
	var wg sync.WaitGroup
	for i, r := range resolvers {
		wg.Add(1)
		go func(i int, r resolver) {
			defer wg.Done()
			states[i] = r.Resolve(ctx)
		}(i, r)
	}
	wg.Wait()
	return states
}
