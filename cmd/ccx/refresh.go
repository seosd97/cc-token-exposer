package main

import (
	"context"
	"errors"
	"time"

	"github.com/spf13/cobra"
)

const refreshTimeout = 30 * time.Second

func newRefreshCmd(ps providers) *cobra.Command {
	var providerFlag string
	cmd := &cobra.Command{
		Use:           "refresh",
		Short:         "Refresh the shared usage cache (internal)",
		Hidden:        true,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			names := parseProviderList(providerFlag)
			if len(names) != 1 {
				return errors.New("refresh takes exactly one provider")
			}
			entries, err := ps.lookup(names)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), refreshTimeout)
			defer cancel()
			_ = entries[0].resolver.Resolve(ctx)
			return nil
		},
	}
	addProviderFlag(cmd, &providerFlag, "provider whose cache to refresh")
	return cmd
}
