package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/spf13/cobra"
)

type providers map[string]resolver

func (p providers) names() []string {
	out := make([]string, 0, len(p))
	for name := range p {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (p providers) lookup(names []string) ([]resolver, error) {
	out := make([]resolver, 0, len(names))
	for _, name := range names {
		r, ok := p[name]
		if !ok {
			return nil, fmt.Errorf("unknown provider %q (known: %s)", name, strings.Join(p.names(), ", "))
		}
		out = append(out, r)
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
