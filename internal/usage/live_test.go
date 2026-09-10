package usage

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/creds"
	"github.com/seosd97/cc-token-exposer/internal/schema"
)

func TestLiveSmoke(t *testing.T) {
	token := os.Getenv("CCX_LIVE_TOKEN")
	if token == "" {
		t.Skip("CCX_LIVE_TOKEN not set; live smoke is manual-only")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fetched, err := New().Fetch(ctx, &creds.Credentials{AccessToken: token})
	if err != nil {
		t.Fatalf("live fetch: %v", err)
	}

	s := fetched.Snapshot
	t.Logf("fetched_at=%s five_hour=%+v seven_day=%+v scoped=%v extra=%+v",
		s.FetchedAt, s.FiveHour, s.SevenDay, scopedSummary(s.ScopedLimits), s.ExtraUsage)
	if len(fetched.Drift) > 0 {
		t.Logf("drift indicators: %v", fetched.Drift)
	}

	if s.FiveHour == nil && s.SevenDay == nil && len(s.ScopedLimits) == 0 && len(fetched.Drift) == 0 {
		t.Fatalf("no usable window and no drift indicator; the response shape may have changed")
	}
}

func scopedSummary(m map[string]*schema.Window) []string {
	out := make([]string, 0, len(m))
	for name, w := range m {
		if w != nil {
			out = append(out, fmt.Sprintf("%s=%.1f", name, w.Utilization))
		}
	}
	sort.Strings(out)
	return out
}
