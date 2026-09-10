package provider

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestParseTolerantTime(t *testing.T) {
	want := time.Date(2026, 6, 12, 16, 0, 0, 0, time.UTC)
	epoch := strconv.FormatInt(want.Unix(), 10)
	cases := []struct {
		name string
		raw  string
		want time.Time
	}{
		{"rfc3339 string", `"2026-06-12T16:00:00Z"`, want},
		{"epoch seconds", epoch, want},
		{"epoch milliseconds", epoch + "000", want},
		{"fractional epoch seconds truncate", epoch + ".9", want},
		{"null", `null`, time.Time{}},
		{"empty", ``, time.Time{}},
		{"zero", `0`, time.Time{}},
		{"negative", `-5`, time.Time{}},
		{"garbage string", `"soon"`, time.Time{}},
		{"object", `{"at":1}`, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseTolerantTime(json.RawMessage(tc.raw)); !got.Equal(tc.want) {
				t.Fatalf("ParseTolerantTime(%s) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"  ", 0},
		{"0", 0},
		{"-5", 0},
		{"30", 30 * time.Second},
		{" 30 ", 30 * time.Second},
		{"not-a-number", 0},
		{now.Add(60 * time.Second).UTC().Format(http.TimeFormat), 60 * time.Second},
		{now.Add(-60 * time.Second).UTC().Format(http.TimeFormat), 0},
	}
	for _, tc := range cases {
		if got := ParseRetryAfter(tc.in, now); got != tc.want {
			t.Errorf("ParseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
