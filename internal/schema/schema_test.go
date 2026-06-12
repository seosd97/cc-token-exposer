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
