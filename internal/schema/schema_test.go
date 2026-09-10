package schema

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDurationRoundTrip(t *testing.T) {
	cases := []time.Duration{0, 45 * time.Second, 4*time.Hour + 12*time.Minute, 3 * 24 * time.Hour}
	for _, d := range cases {
		b, err := json.Marshal(NewDuration(d))
		if err != nil {
			t.Fatalf("marshal %v: %v", d, err)
		}
		var got Duration
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", b, err)
		}
		if time.Duration(got) != d {
			t.Fatalf("round trip: got %v, want %v (json %s)", time.Duration(got), d, b)
		}
	}
}

func TestDurationMarshalsAsSeconds(t *testing.T) {
	b, err := json.Marshal(NewDuration(90 * time.Second))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "90" {
		t.Fatalf("got %s, want 90", b)
	}
}

func TestStateJSONShape(t *testing.T) {
	reset := time.Date(2026, 6, 12, 16, 0, 0, 0, time.UTC)
	st := &State{
		SchemaVersion: Version,
		Type:          TypeSnapshot,
		Source:        SourceOAuth,
		Auth:          AuthOK,
		Snapshot: &Snapshot{
			FetchedAt: time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC),
			FiveHour:  &Window{Utilization: 23, ResetsAt: reset},
		},
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if generic["schema_version"].(float64) != float64(Version) {
		t.Fatalf("schema_version missing/wrong: %v", generic["schema_version"])
	}
	for _, k := range []string{"type", "source", "auth", "stale", "snapshot"} {
		if _, ok := generic[k]; !ok {
			t.Fatalf("missing key %q in %s", k, b)
		}
	}
	// Optional fields absent when empty.
	if _, ok := generic["stale_age"]; ok {
		t.Fatalf("stale_age should be omitted when nil")
	}
	if _, ok := generic["limit_hit"]; ok {
		t.Fatalf("limit_hit should be omitted when nil")
	}
}

func TestSuspectOmittedWhenFalse(t *testing.T) {
	b, _ := json.Marshal(&Window{Utilization: 10, ResetsAt: time.Unix(1, 0)})
	if strings.Contains(string(b), "suspect") {
		t.Fatalf("suspect should be omitted when false: %s", b)
	}
}

// TestSnapshotMarshalEmitsAliases guards the wire contract: legacy
// seven_day_opus / seven_day_fable aliases must be emitted from ScopedLimits at
// marshal time (only for those two models), and must round-trip through the
// Snapshot type without leaking into internal state.
func TestSnapshotMarshalEmitsAliases(t *testing.T) {
	reset := time.Date(2026, 6, 12, 16, 0, 0, 0, time.UTC)
	s := &Snapshot{
		FetchedAt: reset.Add(-time.Hour),
		FiveHour:  &Window{Utilization: 23, ResetsAt: reset},
		ScopedLimits: map[string]*Window{
			"Opus":   {Utilization: 10, ResetsAt: reset},
			"Fable":  {Utilization: 94, ResetsAt: reset},
			"Sonnet": {Utilization: 33, ResetsAt: reset},
		},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := generic["seven_day_opus"]; !ok {
		t.Fatalf("missing seven_day_opus alias: %s", b)
	}
	if _, ok := generic["seven_day_fable"]; !ok {
		t.Fatalf("missing seven_day_fable alias: %s", b)
	}
	sc, _ := generic["scoped_limits"].(map[string]any)
	if len(sc) != 3 {
		t.Fatalf("scoped_limits has %d entries, want 3 (including new models): %s", len(sc), b)
	}

	var back Snapshot
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal alias JSON: %v", err)
	}
	if back.ScopedLimits["Opus"] == nil || back.ScopedLimits["Fable"] == nil || back.ScopedLimits["Sonnet"] == nil {
		t.Fatalf("aliases must not replace scoped_limits state: %+v", back.ScopedLimits)
	}
}

func TestSnapshotMarshalOmitsAliasesWithoutScopedModels(t *testing.T) {
	s := &Snapshot{FiveHour: &Window{Utilization: 5, ResetsAt: time.Now()}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "seven_day_opus") || strings.Contains(string(b), "seven_day_fable") {
		t.Fatalf("aliases should be omitted when no scoped models: %s", b)
	}
}

func TestStateDriftOmittedWhenEmpty(t *testing.T) {
	b, _ := json.Marshal(&State{SchemaVersion: Version, Type: TypeSnapshot, Source: SourceOAuth, Auth: AuthOK})
	if strings.Contains(string(b), "drift") {
		t.Fatalf("drift should be omitted when empty: %s", b)
	}
}

func TestStateDriftRoundTrip(t *testing.T) {
	want := []string{"five_hour missing resets_at", `unknown scoped limits kind "x"`}
	st := &State{SchemaVersion: Version, Type: TypeSnapshot, Source: SourceOAuth, Auth: AuthOK, Drift: want}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back State
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Drift) != len(want) {
		t.Fatalf("Drift = %v, want %v", back.Drift, want)
	}
	for i := range want {
		if back.Drift[i] != want[i] {
			t.Fatalf("Drift[%d] = %q, want %q", i, back.Drift[i], want[i])
		}
	}
}
