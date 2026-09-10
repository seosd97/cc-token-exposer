package claude

import (
	"github.com/seosd97/cc-token-exposer/internal/provider"
	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/seosd97/cc-token-exposer/internal/transcript"
)

func Spec() provider.Spec {
	return provider.Spec{
		Name:         schema.ProviderClaude,
		LoginCommand: "claude",
		Creds:        DefaultCredentials(),
		Fetcher:      New(),
		Transcript:   transcript.NewProbe(),
		ParseStdin:   ParseStatuslineStdin,
	}
}
