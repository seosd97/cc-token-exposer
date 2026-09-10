package main

import (
	"context"
	"time"

	"github.com/spf13/cobra"
)

const refreshTimeout = 30 * time.Second

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
