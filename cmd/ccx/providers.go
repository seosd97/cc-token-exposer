package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/provider"
	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/spf13/cobra"
)

type providerEntry struct {
	spec     provider.Spec
	resolver resolver
}

func (p providerEntry) resolveStatusline(ctx context.Context, stdinDoc []byte, now time.Time) *schema.State {
	if p.spec.ParseStdin == nil {
		return p.resolver.ResolveDetached(ctx)
	}
	var snap *schema.Snapshot
	if stdinDoc != nil {
		snap = p.spec.ParseStdin(stdinDoc, now)
	}
	return p.resolver.ResolveStdin(ctx, snap)
}

type providers map[string]providerEntry

func (p providers) names() []string {
	out := make([]string, 0, len(p))
	for name := range p {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (p providers) lookup(names []string) ([]providerEntry, error) {
	out := make([]providerEntry, 0, len(names))
	for _, name := range names {
		entry, ok := p[name]
		if !ok {
			return nil, fmt.Errorf("unknown provider %q (known: %s)", name, strings.Join(p.names(), ", "))
		}
		out = append(out, entry)
	}
	return out, nil
}

func parseProviderList(s string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(s, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return []string{schema.ProviderClaude}
	}
	return out
}

func addProviderFlag(cmd *cobra.Command, dst *string, usage string) {
	cmd.Flags().StringVar(dst, "provider", schema.ProviderClaude, usage)
}
