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
					ScopedLimits: map[string]*schema.Window{"Opus": win(5, reset7d)},
				},
			},
			want: "◷ 5h ▮▯▯▯▯ 10% ↻ 4h12m · ◷ 7d ▮▯▯▯▯ 20% ↻ 3d0h · ✧ opus ▯▯▯▯▯ 5% ↻ 3d0h",
		},
		{
			name: "includes fable window",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour:     win(10, reset5h),
					SevenDay:     win(20, reset7d),
					ScopedLimits: map[string]*schema.Window{"Fable": win(94, reset7d)},
				},
			},
			want: "◷ 5h ▮▯▯▯▯ 10% ↻ 4h12m · ◷ 7d ▮▯▯▯▯ 20% ↻ 3d0h · ✧ fable ▮▮▮▮▮ 94% ↻ 3d0h",
		},
		{
			name: "multiple scoped models sorted alphabetically",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour: win(10, reset5h),
					SevenDay: win(20, reset7d),
					ScopedLimits: map[string]*schema.Window{
						"Opus":   win(30, reset7d),
						"Fable":  win(50, reset7d),
						"Sonnet": win(70, reset7d),
					},
				},
			},
			want: "◷ 5h ▮▯▯▯▯ 10% ↻ 4h12m · ◷ 7d ▮▯▯▯▯ 20% ↻ 3d0h · ✧ fable ▮▮▮▯▯ 50% ↻ 3d0h · ✧ opus ▮▮▯▯▯ 30% ↻ 3d0h · ✧ sonnet ▮▮▮▮▯ 70% ↻ 3d0h",
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
			name: "window without any reset omits the reset part",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour: win(50, time.Time{}),
				},
			},
			want: "◷ 5h ▮▮▮▯▯ 50%",
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
	if isTerminal(strings.NewReader("{}")) {
		t.Error("strings.Reader should not be reported as a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("regular file should not be reported as a terminal")
	}
}

func TestSnapshotFromRateLimits(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	t.Run("absent", func(t *testing.T) {
		if _, ok := snapshotFromRateLimits(nil, now); ok {
			t.Error("nil rate_limits should not yield a snapshot")
		}
	})

	t.Run("unrelated shape falls through", func(t *testing.T) {
		if _, ok := snapshotFromRateLimits([]byte(`{"something_else":1}`), now); ok {
			t.Error("unknown shape should yield no snapshot")
		}
	})

	t.Run("parses windows with float utilization", func(t *testing.T) {
		raw := []byte(`{"five_hour":{"utilization":23.0,"resets_at":"2026-06-12T16:00:00Z"},"seven_day":{"utilization":40.6,"resets_at":"2026-06-18T00:00:00Z"}}`)
		snap, ok := snapshotFromRateLimits(raw, now)
		if !ok {
			t.Fatal("expected a snapshot")
		}
		if snap.FiveHour == nil || snap.FiveHour.Utilization != 23 {
			t.Errorf("five_hour = %+v, want 23", snap.FiveHour)
		}
		if snap.SevenDay == nil || snap.SevenDay.Utilization != 40.6 {
			t.Errorf("seven_day = %+v, want 40.6 preserved", snap.SevenDay)
		}
		if !snap.FetchedAt.Equal(now) {
			t.Errorf("fetched_at = %v, want %v", snap.FetchedAt, now)
		}
	})

	t.Run("used_percentage field name with epoch resets_at", func(t *testing.T) {
		epoch := time.Date(2026, 6, 12, 16, 0, 0, 0, time.UTC).Unix()
		raw := []byte(`{"five_hour":{"used_percentage":33,"resets_at":` + strconv.FormatInt(epoch, 10) + `}}`)
		snap, ok := snapshotFromRateLimits(raw, now)
		if !ok {
			t.Fatal("expected a snapshot from used_percentage")
		}
		if snap.FiveHour == nil || snap.FiveHour.Utilization != 33 {
			t.Errorf("five_hour = %+v, want 33", snap.FiveHour)
		}
		if want := time.Date(2026, 6, 12, 16, 0, 0, 0, time.UTC); !snap.FiveHour.ResetsAt.Equal(want) {
			t.Errorf("resets_at = %v, want %v (epoch decoded)", snap.FiveHour.ResetsAt, want)
		}
	})

	t.Run("used_percentage takes priority over utilization", func(t *testing.T) {
		raw := []byte(`{"five_hour":{"used_percentage":70,"utilization":10}}`)
		snap, ok := snapshotFromRateLimits(raw, now)
		if !ok || snap.FiveHour.Utilization != 70 {
			t.Errorf("got %+v, want used_percentage 70 to win", snap)
		}
	})

	t.Run("window without resets_at keeps its percentage", func(t *testing.T) {
		raw := []byte(`{"five_hour":{"used_percentage":47},"seven_day":{"used_percentage":20,"resets_at":null}}`)
		snap, ok := snapshotFromRateLimits(raw, now)
		if !ok {
			t.Fatal("expected a snapshot")
		}
		if snap.FiveHour == nil || snap.FiveHour.Utilization != 47 || !snap.FiveHour.ResetsAt.IsZero() {
			t.Errorf("five_hour = %+v, want 47%% with zero reset", snap.FiveHour)
		}
		if snap.SevenDay == nil || snap.SevenDay.Utilization != 20 || !snap.SevenDay.ResetsAt.IsZero() {
			t.Errorf("seven_day = %+v, want 20%% with zero reset", snap.SevenDay)
		}
	})

	t.Run("window without a percentage is ignored", func(t *testing.T) {
		raw := []byte(`{"five_hour":{"resets_at":"2026-06-12T16:00:00Z"}}`)
		if _, ok := snapshotFromRateLimits(raw, now); ok {
			t.Error("window without a percentage should not yield a snapshot")
		}
	})

	t.Run("model_scoped keyed by display_name", func(t *testing.T) {
		raw := []byte(`{"model_scoped":[` +
			`{"display_name":"Fable","utilization":55.0,"resets_at":"2026-06-18T00:00:00Z"},` +
			`{"display_name":"Opus","utilization":12.5,"resets_at":"2026-06-18T00:00:00Z"}]}`)
		snap, ok := snapshotFromRateLimits(raw, now)
		if !ok {
			t.Fatal("expected a snapshot from model_scoped")
		}
		fable := snap.ScopedLimits["Fable"]
		if fable == nil || fable.Utilization != 55 {
			t.Errorf("Fable = %+v, want 55", fable)
		}
		if want := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC); !fable.ResetsAt.Equal(want) {
			t.Errorf("Fable resets_at = %v, want %v", fable.ResetsAt, want)
		}
		if opus := snap.ScopedLimits["Opus"]; opus == nil || opus.Utilization != 12.5 {
			t.Errorf("Opus = %+v, want 12.5", opus)
		}
	})

	t.Run("model_scoped skips entries without a usable value", func(t *testing.T) {
		raw := []byte(`{"model_scoped":[` +
			`{"display_name":"Fable","utilization":null,"resets_at":"2026-06-18T00:00:00Z"},` +
			`{"display_name":"","utilization":5,"resets_at":null}]}`)
		if _, ok := snapshotFromRateLimits(raw, now); ok {
			t.Error("entries with null utilization or empty name should not yield a snapshot")
		}
	})

	t.Run("seven_day_opus backfills only when model_scoped lacks Opus", func(t *testing.T) {
		raw := []byte(`{"seven_day_opus":{"used_percentage":30,"resets_at":"2026-06-18T00:00:00Z"}}`)
		snap, ok := snapshotFromRateLimits(raw, now)
		if !ok || snap.ScopedLimits["Opus"] == nil || snap.ScopedLimits["Opus"].Utilization != 30 {
			t.Fatalf("seven_day_opus should backfill ScopedLimits[Opus]: %+v", snap)
		}

		raw = []byte(`{"seven_day_opus":{"used_percentage":30},` +
			`"model_scoped":[{"display_name":"Opus","utilization":7,"resets_at":null}]}`)
		snap, ok = snapshotFromRateLimits(raw, now)
		if !ok || snap.ScopedLimits["Opus"].Utilization != 7 {
			t.Fatalf("model_scoped Opus should win over seven_day_opus: %+v", snap)
		}
	})
}
