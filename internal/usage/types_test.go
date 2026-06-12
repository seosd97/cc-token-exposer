package usage

import (
	"encoding/json"
	"testing"
)

// TestWindowUnmarshalNumberForms verifies utilization decodes from both int and
// float JSON numbers into float64, preserving fractional precision (the live
// endpoint sends floats; the field is float64 to preserve them).
func TestWindowUnmarshalNumberForms(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{`{"utilization": 18}`, 18},     // int form
		{`{"utilization": 18.0}`, 18},   // float, whole
		{`{"utilization": 18.5}`, 18.5}, // fractional preserved (exactly representable)
		{`{"utilization": 0}`, 0},
		{`{"utilization": 100}`, 100},
	}
	for _, tc := range cases {
		var w Window
		if err := json.Unmarshal([]byte(tc.in), &w); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.in, err)
			continue
		}
		if w.Utilization != tc.want {
			t.Errorf("Unmarshal(%s): Utilization = %v, want %v", tc.in, w.Utilization, tc.want)
		}
	}
}

func TestSnapshotDecodeFloatUtilization(t *testing.T) {
	const body = `{
		"five_hour":      {"utilization": 18.0, "resets_at": "2026-06-12T18:00:00Z"},
		"seven_day":      {"utilization": 7.5,  "resets_at": "2026-06-18T00:00:00Z"},
		"extra_usage":    {"utilization": 12.0}
	}`
	var snap Snapshot
	if err := json.Unmarshal([]byte(body), &snap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if snap.FiveHour == nil || snap.FiveHour.Utilization != 18 {
		t.Errorf("five_hour = %+v, want utilization 18", snap.FiveHour)
	}
	if snap.SevenDay == nil || snap.SevenDay.Utilization != 7.5 {
		t.Errorf("seven_day = %+v, want utilization 7.5", snap.SevenDay)
	}
	if snap.ExtraUsage == nil || snap.ExtraUsage.Utilization == nil || *snap.ExtraUsage.Utilization != 12 {
		t.Errorf("extra_usage = %+v, want utilization 12", snap.ExtraUsage)
	}
}

func TestWindowRoundTrip(t *testing.T) {
	w := Window{Utilization: 42.5, Suspect: true}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Window
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Utilization != 42.5 || !got.Suspect {
		t.Errorf("round trip = %+v, want {42.5, suspect}", got)
	}
}
