package transcript

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseReset(t *testing.T) {
	loc := time.UTC
	ref := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC) // noon

	cases := []struct {
		name   string
		text   string
		want   time.Time
		wantOK bool
	}{
		{
			name:   "afternoon same day",
			text:   "You've hit your session limit · resets 2:30pm",
			want:   time.Date(2026, 6, 12, 14, 30, 0, 0, loc),
			wantOK: true,
		},
		{
			name:   "hour only, no minutes",
			text:   "resets 4pm",
			want:   time.Date(2026, 6, 12, 16, 0, 0, 0, loc),
			wantOK: true,
		},
		{
			name:   "earlier than ref rolls forward a day",
			text:   "resets 9:00am",
			want:   time.Date(2026, 6, 13, 9, 0, 0, 0, loc),
			wantOK: true,
		},
		{
			name:   "noon is 12pm",
			text:   "resets 12:00pm",
			want:   time.Date(2026, 6, 12, 12, 0, 0, 0, loc).AddDate(0, 0, 1), // == ref, not after -> rolls
			wantOK: true,
		},
		{
			name:   "midnight is 12am",
			text:   "resets 12:15am",
			want:   time.Date(2026, 6, 13, 0, 15, 0, 0, loc),
			wantOK: true,
		},
		{
			name:   "dotted a.m. spelling",
			text:   "resets 1:05 p.m.",
			want:   time.Date(2026, 6, 12, 13, 5, 0, 0, loc),
			wantOK: true,
		},
		{name: "no match", text: "nothing to see here", wantOK: false},
		{name: "invalid hour", text: "resets 19:00pm", wantOK: false},
		{name: "invalid minute", text: "resets 4:90pm", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseReset(tc.text, ref, loc)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseResetHonorsExplicitTimezone(t *testing.T) {
	seoul, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Skip("Asia/Seoul tzdata unavailable")
	}
	// Default loc is UTC, but the message names Asia/Seoul (UTC+9), which must win.
	ref := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC) // 09:00 KST
	got, ok := ParseReset("You've hit your session limit · resets 4:50pm (Asia/Seoul)", ref, time.UTC)
	if !ok {
		t.Fatal("expected a parse")
	}
	want := time.Date(2026, 6, 12, 16, 50, 0, 0, seoul) // 16:50 KST, after 09:00 KST
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseResetInvalidTimezoneFallsBack(t *testing.T) {
	// A bogus IANA name must fall back to the supplied loc, not fail.
	ref := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	got, ok := ParseReset("resets 2:30pm (Not/AZone)", ref, time.UTC)
	if !ok {
		t.Fatal("expected a parse")
	}
	want := time.Date(2026, 6, 12, 14, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v (UTC fallback)", got, want)
	}
}

func TestScanReaderDetectsLimitHit(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	const line = `{"type":"assistant","timestamp":"2026-06-12T16:10:00Z","isApiErrorMessage":true,"message":{"role":"assistant","content":[{"type":"text","text":"You've hit your session limit · resets 6:30pm"}]}}`
	hit, err := ScanReader(strings.NewReader(line), now, loc)
	if err != nil {
		t.Fatalf("ScanReader: %v", err)
	}
	if hit == nil {
		t.Fatal("expected a limit hit, got nil")
	}
	if !hit.HasReset {
		t.Fatal("expected HasReset true")
	}
	wantReset := time.Date(2026, 6, 12, 18, 30, 0, 0, loc)
	if !hit.ResetsAt.Equal(wantReset) {
		t.Errorf("ResetsAt = %v, want %v", hit.ResetsAt, wantReset)
	}
	wantDetected := time.Date(2026, 6, 12, 16, 10, 0, 0, loc)
	if !hit.DetectedAt.Equal(wantDetected) {
		t.Errorf("DetectedAt = %v, want %v", hit.DetectedAt, wantDetected)
	}
}

func TestScanReaderIgnoresNonLimit(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	lines := strings.Join([]string{
		`{"type":"user","timestamp":"2026-06-12T16:00:00Z","message":{"role":"user","content":"how do i increase my session limit?"}}`,
		`{"type":"assistant","timestamp":"2026-06-12T16:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"You can wait until the window resets."}]}}`,
	}, "\n")
	hit, err := ScanReader(strings.NewReader(lines), now, time.UTC)
	if err != nil {
		t.Fatalf("ScanReader: %v", err)
	}
	if hit != nil {
		t.Fatalf("expected no hit (no isApiErrorMessage), got %+v", hit)
	}
}

func TestScanReaderStringContent(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	// content as a plain string rather than a block array.
	const line = `{"type":"assistant","timestamp":"2026-06-12T13:00:00Z","isApiErrorMessage":true,"message":{"role":"assistant","content":"You've hit your session limit · resets 5pm"}}`
	hit, err := ScanReader(strings.NewReader(line), now, time.UTC)
	if err != nil {
		t.Fatalf("ScanReader: %v", err)
	}
	if hit == nil || !hit.HasReset {
		t.Fatalf("expected hit with reset, got %+v", hit)
	}
	if want := time.Date(2026, 6, 12, 17, 0, 0, 0, time.UTC); !hit.ResetsAt.Equal(want) {
		t.Errorf("ResetsAt = %v, want %v", hit.ResetsAt, want)
	}
}

func TestScanReaderLimitWithoutReset(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	const line = `{"type":"assistant","timestamp":"2026-06-12T13:00:00Z","isApiErrorMessage":true,"message":{"role":"assistant","content":"You've hit your session limit. Please try again later."}}`
	hit, err := ScanReader(strings.NewReader(line), now, time.UTC)
	if err != nil {
		t.Fatalf("ScanReader: %v", err)
	}
	if hit == nil {
		t.Fatal("expected a hit even without a reset time")
	}
	if hit.HasReset {
		t.Errorf("expected HasReset false, got reset %v", hit.ResetsAt)
	}
}

// TestScanLatestGolden uses a synthetic transcript fixture and asserts the most
// recent limit hit (by transcript timestamp) is selected with the right reset.
func TestScanLatestGolden(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 12, 20, 0, 0, 0, time.UTC)

	hit, err := ScanLatest([]string{filepath.Join("testdata", "sample.jsonl")}, now, loc)
	if err != nil {
		t.Fatalf("ScanLatest: %v", err)
	}
	if hit == nil {
		t.Fatal("expected a limit hit from fixture")
	}
	// The later of the two hits is at 18:45Z, "resets 11:50pm" -> 23:50Z same day.
	wantDetected := time.Date(2026, 6, 12, 18, 45, 0, 0, loc)
	if !hit.DetectedAt.Equal(wantDetected) {
		t.Errorf("DetectedAt = %v, want %v (latest hit)", hit.DetectedAt, wantDetected)
	}
	wantReset := time.Date(2026, 6, 12, 23, 50, 0, 0, loc)
	if !hit.HasReset || !hit.ResetsAt.Equal(wantReset) {
		t.Errorf("ResetsAt = %v (hasReset=%v), want %v", hit.ResetsAt, hit.HasReset, wantReset)
	}
}

func TestScanLatestNoFiles(t *testing.T) {
	hit, err := ScanLatest([]string{filepath.Join("testdata", "does-not-exist.jsonl")}, time.Now(), time.UTC)
	if err != nil {
		t.Fatalf("ScanLatest should skip unreadable files, got err: %v", err)
	}
	if hit != nil {
		t.Fatalf("expected nil hit, got %+v", hit)
	}
}
