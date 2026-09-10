package codex

import (
	"github.com/seosd97/cc-token-exposer/internal/provider"
	"github.com/seosd97/cc-token-exposer/internal/schema"
)

func Spec() provider.Spec {
	return provider.Spec{
		Name:         schema.ProviderCodex,
		LoginCommand: "codex login",
		Creds:        provider.NewResolver(&AuthSource{}),
		Fetcher:      New(),
	}
}
