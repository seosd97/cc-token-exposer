package main

import (
	"context"
	"time"

	"github.com/spf13/cobra"
)

const refreshTimeout = 30 * time.Second

// newRefreshCmd runs the full engine ladder once, discarding the result. It is
// the detached workhorse behind the statusline path: a spawned `ccx refresh`
// refreshes and stores the shared disk cache without any output, so the next
// statusline tick serves fresher data.
func newRefreshCmd(res resolver) *cobra.Command {
	return &cobra.Command{
		Use:    "refresh",
		Short:  "Refresh the shared usage cache (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), refreshTimeout)
			defer cancel()
			_ = res.Resolve(ctx)
			return nil
		},
	}
}
