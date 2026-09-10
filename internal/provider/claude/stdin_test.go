package claude

import (
	"strconv"
	"testing"
	"time"
)

func TestParseStatuslineStdin(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	t.Run("malformed JSON yields nil", func(t *testing.T) {
		if snap := ParseStatuslineStdin([]byte("{not json"), now); snap != nil {
			t.Fatalf("malformed input should yield nil, got %+v", snap)
		}
	})

	t.Run("document without rate_limits yields nil", func(t *testing.T) {
		if snap := ParseStatuslineStdin([]byte(`{"session_id":"x"}`), now); snap != nil {
			t.Fatalf("missing rate_limits should yield nil, got %+v", snap)
		}
	})

	t.Run("rate_limits become a snapshot", func(t *testing.T) {
		doc := []byte(`{"session_id":"x","rate_limits":{"five_hour":{"utilization":12,"resets_at":"2026-06-12T16:00:00Z"}}}`)
		snap := ParseStatuslineStdin(doc, now)
		if snap == nil || snap.FiveHour == nil || snap.FiveHour.Utilization != 12 {
			t.Fatalf("five_hour not parsed from rate_limits: %+v", snap)
		}
	})
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
