package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

func win(util float64, reset time.Time) *schema.Window {
	return &schema.Window{Utilization: util, ResetsAt: reset}
}

func TestFormatStatusline(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	reset5h := now.Add(4*time.Hour + 12*time.Minute)
	reset7d := now.Add(3 * 24 * time.Hour)

	cases := []struct {
		name string
		st   *schema.State
		want string
	}{
		{
			name: "nil state",
			st:   nil,
			want: "⚠ ccx",
		},
		{
			name: "auth missing",
			st:   &schema.State{Auth: schema.AuthMissing, Type: schema.TypeError},
			want: "⚠ login",
		},
		{
			name: "auth expired",
			st:   &schema.State{Auth: schema.AuthExpired, Type: schema.TypeError},
			want: "⚠ login",
		},
		{
			name: "full snapshot with inline reset",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour: win(23, reset5h),
					SevenDay: win(41, reset7d),
				},
			},
			want: "◷ 5h ▮▯▯▯▯ 23% ↻ 4h12m · ◷ 7d ▮▮▯▯▯ 41% ↻ 3d0h",
		},
		{
			name: "includes opus window",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour:     win(10, reset5h),
					SevenDay:     win(20, reset7d),
					SevenDayOpus: win(5, reset7d),
				},
			},
			want: "◷ 5h ▮▯▯▯▯ 10% ↻ 4h12m · ◷ 7d ▮▯▯▯▯ 20% ↻ 3d0h · ✦ opus ▯▯▯▯▯ 5% ↻ 3d0h",
		},
		{
			name: "stale prefixes approx sign",
			st: &schema.State{
				Auth:  schema.AuthOK,
				Stale: true,
				Snapshot: &schema.Snapshot{
					FiveHour: win(50, reset5h),
				},
			},
			want: "≈ ◷ 5h ▮▮▮▯▯ 50% ↻ 4h12m",
		},
		{
			name: "auth expired but stale snapshot keeps last-known values",
			st: &schema.State{
				Auth:  schema.AuthExpired,
				Stale: true,
				Snapshot: &schema.Snapshot{
					FiveHour: win(23, reset5h),
					SevenDay: win(41, reset7d),
				},
			},
			want: "≈ ◷ 5h ▮▯▯▯▯ 23% ↻ 4h12m · ◷ 7d ▮▮▯▯▯ 41% ↻ 3d0h · ⚠ login",
		},
		{
			name: "all resets elapsed omits the reset part",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour: win(50, now.Add(-time.Hour)),
				},
			},
			want: "◷ 5h ▮▮▮▯▯ 50%",
		},
		{
			name: "limit hit with already-elapsed reset shows generic",
			st: func() *schema.State {
				r := now.Add(-time.Hour)
				return &schema.State{Auth: schema.AuthOK, LimitHit: &schema.LimitHit{ResetsAt: &r}}
			}(),
			want: "⛔ limit",
		},
		{
			name: "no windows but limit hit with reset",
			st: func() *schema.State {
				r := now.Add(2 * time.Hour)
				return &schema.State{Auth: schema.AuthOK, LimitHit: &schema.LimitHit{ResetsAt: &r}}
			}(),
			want: "⛔ ↻ 2h00m",
		},
		{
			name: "no windows, limit hit without reset",
			st:   &schema.State{Auth: schema.AuthOK, LimitHit: &schema.LimitHit{}},
			want: "⛔ limit",
		},
		{
			name: "auth ok but nothing to show",
			st:   &schema.State{Auth: schema.AuthOK},
			want: "⚠ ccx",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatStatusline(tc.st, now, false)
			if got != tc.want {
				t.Errorf("formatStatusline = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadStatuslineInput(t *testing.T) {
	t.Run("empty stdin", func(t *testing.T) {
		in, err := readStatuslineInput(strings.NewReader(""))
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(in.RateLimits) != 0 {
			t.Errorf("expected no rate_limits, got %s", in.RateLimits)
		}
	})

	t.Run("malformed JSON is tolerated", func(t *testing.T) {
		in, err := readStatuslineInput(strings.NewReader("{not json"))
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(in.RateLimits) != 0 {
			t.Errorf("expected no rate_limits from malformed input")
		}
	})

	t.Run("captures rate_limits", func(t *testing.T) {
		in, err := readStatuslineInput(strings.NewReader(`{"session_id":"x","rate_limits":{"five_hour":{"utilization":12}}}`))
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(in.RateLimits) == 0 {
			t.Fatalf("expected rate_limits captured")
		}
	})

	t.Run("nil reader", func(t *testing.T) {
		if _, err := readStatuslineInput(nil); err != nil {
			t.Fatalf("nil reader should not error: %v", err)
		}
	})
}

func TestIsTerminal(t *testing.T) {
	// A non-*os.File reader (pipe/buffer) is never a terminal, so stdin is read.
	if isTerminal(strings.NewReader("{}")) {
		t.Error("strings.Reader should not be reported as a terminal")
	}
	// A regular file (not a char device) is also not a terminal.
	f, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("regular file should not be reported as a terminal")
	}
}

func TestStateFromRateLimits(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	t.Run("absent", func(t *testing.T) {
		if _, ok := stateFromRateLimits(nil, now); ok {
			t.Error("nil rate_limits should not yield a state")
		}
	})

	t.Run("unrelated shape falls through", func(t *testing.T) {
		if _, ok := stateFromRateLimits([]byte(`{"something_else":1}`), now); ok {
			t.Error("unknown shape should yield no state")
		}
	})

	t.Run("parses windows with float utilization", func(t *testing.T) {
		raw := []byte(`{"five_hour":{"utilization":23.0,"resets_at":"2026-06-12T16:00:00Z"},"seven_day":{"utilization":40.6,"resets_at":"2026-06-18T00:00:00Z"}}`)
		st, ok := stateFromRateLimits(raw, now)
		if !ok {
			t.Fatal("expected a state")
		}
		if st.Source != schema.SourceOAuth || st.Auth != schema.AuthOK || st.Type != schema.TypeSnapshot {
			t.Errorf("unexpected state envelope: %+v", st)
		}
		// The wire type is float64: the raw value is preserved, not rounded.
		if st.Snapshot.FiveHour == nil || st.Snapshot.FiveHour.Utilization != 23 {
			t.Errorf("five_hour = %+v, want 23", st.Snapshot.FiveHour)
		}
		if st.Snapshot.SevenDay == nil || st.Snapshot.SevenDay.Utilization != 40.6 {
			t.Errorf("seven_day = %+v, want 40.6 preserved", st.Snapshot.SevenDay)
		}
		// Rounding happens only at display time; each window shows gauge, %, and reset inline.
		line := formatStatusline(st, now, false)
		if !strings.Contains(line, "◷ 5h ▮▯▯▯▯ 23%") || !strings.Contains(line, "◷ 7d ▮▮▯▯▯ 41%") {
			t.Errorf("formatted line = %q", line)
		}
	})

	t.Run("used_percentage field name with epoch resets_at", func(t *testing.T) {
		// The observed statusline schema uses used_percentage and may send
		// resets_at as an epoch (seconds here).
		epoch := time.Date(2026, 6, 12, 16, 0, 0, 0, time.UTC).Unix()
		raw := []byte(`{"five_hour":{"used_percentage":33,"resets_at":` + strconv.FormatInt(epoch, 10) + `}}`)
		st, ok := stateFromRateLimits(raw, now)
		if !ok {
			t.Fatal("expected a state from used_percentage")
		}
		if st.Snapshot.FiveHour == nil || st.Snapshot.FiveHour.Utilization != 33 {
			t.Errorf("five_hour = %+v, want 33", st.Snapshot.FiveHour)
		}
		if want := time.Date(2026, 6, 12, 16, 0, 0, 0, time.UTC); !st.Snapshot.FiveHour.ResetsAt.Equal(want) {
			t.Errorf("resets_at = %v, want %v (epoch decoded)", st.Snapshot.FiveHour.ResetsAt, want)
		}
	})

	t.Run("used_percentage takes priority over utilization", func(t *testing.T) {
		raw := []byte(`{"five_hour":{"used_percentage":70,"utilization":10}}`)
		st, ok := stateFromRateLimits(raw, now)
		if !ok || st.Snapshot.FiveHour.Utilization != 70 {
			t.Errorf("got %+v, want used_percentage 70 to win", st.Snapshot)
		}
	})

	t.Run("window without a percentage is ignored", func(t *testing.T) {
		// Only resets_at, no used_percentage/utilization -> not a usable window.
		raw := []byte(`{"five_hour":{"resets_at":"2026-06-12T16:00:00Z"}}`)
		if _, ok := stateFromRateLimits(raw, now); ok {
			t.Error("window without a percentage should not yield a state")
		}
	})
}
