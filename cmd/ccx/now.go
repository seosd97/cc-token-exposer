package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/spf13/cobra"
)

const nowTimeout = 15 * time.Second

func newNowCmd(res resolver) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "now",
		Short: "Show current plan usage once",
		Long: "Resolve and print the current plan credit-limit windows a single time " +
			"(used by the CLI and statusline). Reads the disk cache when fresh and " +
			"only calls the usage API when the cache TTL has elapsed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), nowTimeout)
			defer cancel()

			st := res.Resolve(ctx)

			out := cmd.OutOrStdout()
			if asJSON {
				if err := json.NewEncoder(out).Encode(st); err != nil {
					return err
				}
			} else {
				fmt.Fprint(out, renderHuman(st, time.Now().UTC()))
			}
			if st.Type == schema.TypeError {
				return errSilentExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the State as a single JSON line")
	return cmd
}
