package main

import (
	"strings"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

func TestHumanizeDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-time.Minute, "now"},
		{0, "now"},
		{20 * time.Second, "<1m"},
		{7 * time.Minute, "7m"},
		{4*time.Hour + 12*time.Minute, "4h12m"},
		{4*time.Hour + 5*time.Minute, "4h05m"},
		{3*24*time.Hour + 12*time.Hour + 59*time.Minute, "3d12h"},
		{7 * 24 * time.Hour, "7d0h"},
	}
	for _, tc := range cases {
		if got := humanizeDuration(tc.in); got != tc.want {
			t.Errorf("humanizeDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderHumanTranscriptStateShowsAuthProblem(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	st := &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeSnapshot,
		Source:        schema.SourceTranscript,
		Stale:         true,
		Auth:          schema.AuthMissing,
		LimitHit:      &schema.LimitHit{ResetsAt: &reset, DetectedAt: now},
	}
	got := renderHuman(st, now)
	for _, want := range []string{"⛔ limit hit · resets in 2h00m", "source: transcript fallback · ⚠ no credentials"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderHumanStaleCacheShowsExpiredToken(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	snap := &schema.Snapshot{FiveHour: &schema.Window{Utilization: 47, ResetsAt: now.Add(time.Hour)}}
	st := &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeSnapshot,
		Source:        schema.SourceCache,
		Stale:         true,
		StaleAge:      schema.NewDuration(3 * time.Minute),
		Auth:          schema.AuthExpired,
		Snapshot:      snap,
	}
	if got := renderHuman(st, now); !strings.Contains(got, "source: cache · stale (3m old) · ⚠ token expired") {
		t.Fatalf("footer missing the auth note:\n%s", got)
	}

	healthy := &schema.State{Type: schema.TypeSnapshot, Source: schema.SourceOAuth, Auth: schema.AuthOK, Snapshot: snap}
	if got := renderHuman(healthy, now); strings.Contains(got, "⚠") {
		t.Fatalf("a healthy state must carry no auth note:\n%s", got)
	}
}

func TestRenderHumanNoPlanState(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	reason := "codex: logged in with an API key; plan limits do not apply"
	if got := renderHuman(&schema.State{Type: schema.TypeError, Auth: schema.AuthNoPlan, Error: reason}, now); got != "⚠ "+reason+"\n" {
		t.Fatalf("renderHuman = %q, want the source's reason", got)
	}
	if got := renderHuman(&schema.State{Type: schema.TypeError, Auth: schema.AuthNoPlan}, now); got != "⚠ no plan limits\n" {
		t.Fatalf("renderHuman without a reason = %q, want the generic no-plan text", got)
	}
}
