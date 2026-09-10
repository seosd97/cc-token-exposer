package codex

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/creds"
)

func TestLiveSmokeCodex(t *testing.T) {
	token := os.Getenv("CCX_LIVE_CODEX_TOKEN")
	account := os.Getenv("CCX_LIVE_CODEX_ACCOUNT")
	if token == "" || account == "" {
		t.Skip("CCX_LIVE_CODEX_TOKEN / CCX_LIVE_CODEX_ACCOUNT not set; live smoke is manual-only")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fetched, err := New().Fetch(ctx, &creds.Credentials{AccessToken: token, AccountID: account})
	if err != nil {
		t.Fatalf("live fetch: %v", err)
	}

	s := fetched.Snapshot
	t.Logf("fetched_at=%s five_hour=%+v seven_day=%+v scoped=%d", s.FetchedAt, s.FiveHour, s.SevenDay, len(s.ScopedLimits))
	for name, w := range s.ScopedLimits {
		t.Logf("scoped %q=%+v", name, w)
	}
	if len(fetched.Drift) > 0 {
		t.Logf("drift indicators: %v", fetched.Drift)
	}

	if s.FiveHour == nil && s.SevenDay == nil && len(s.ScopedLimits) == 0 && len(fetched.Drift) == 0 {
		t.Fatalf("no usable window and no drift indicator; the response shape may have changed")
	}
}
