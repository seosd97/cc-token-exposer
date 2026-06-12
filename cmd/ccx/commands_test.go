package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

// fakeResolver returns a canned State, exercising the full command path
// (flags, rendering, exit codes) without any real IO.
type fakeResolver struct{ st *schema.State }

func (f *fakeResolver) Resolve(context.Context) *schema.State { return f.st }

func snapshotState(fiveHour, sevenDay float64, resetsAt time.Time) *schema.State {
	return &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeSnapshot,
		Source:        schema.SourceOAuth,
		Auth:          schema.AuthOK,
		Snapshot: &schema.Snapshot{
			FetchedAt: resetsAt.Add(-time.Hour),
			FiveHour:  &schema.Window{Utilization: fiveHour, ResetsAt: resetsAt},
			SevenDay:  &schema.Window{Utilization: sevenDay, ResetsAt: resetsAt.Add(5 * 24 * time.Hour)},
		},
	}
}

func authErrorState() *schema.State {
	return &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeError,
		Auth:          schema.AuthMissing,
		Error:         "no credentials found; run `claude` to log in",
	}
}

func TestNowRendersSnapshotAndExitsZero(t *testing.T) {
	cmd := newNowCmd(&fakeResolver{st: snapshotState(63.4, 44, time.Now().UTC().Add(2*time.Hour))})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"5h", "63%", "7d", "44%", "source: live"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestNowErrorStateExitsNonZeroSilently(t *testing.T) {
	cmd := newNowCmd(&fakeResolver{st: authErrorState()})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if !errors.Is(err, errSilentExit) {
		t.Fatalf("err = %v, want errSilentExit", err)
	}
	if !strings.Contains(out.String(), "no credentials") {
		t.Fatalf("user-facing message missing:\n%s", out.String())
	}
}

func TestNowJSONEmitsVersionedState(t *testing.T) {
	cmd := newNowCmd(&fakeResolver{st: snapshotState(21.5, 35, time.Now().UTC().Add(time.Hour))})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var got schema.State
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not one JSON State: %v\n%s", err, out.String())
	}
	if got.SchemaVersion != schema.Version {
		t.Fatalf("schema_version = %d, want %d", got.SchemaVersion, schema.Version)
	}
	if got.Snapshot == nil || got.Snapshot.FiveHour.Utilization != 21.5 {
		t.Fatalf("fractional utilization lost on the wire: %+v", got.Snapshot)
	}
}

func TestStatuslineUsesInjectedResolver(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	st := snapshotState(72, 41, time.Now().UTC().Add(4*time.Hour))
	st.Source = schema.SourceCache
	st.Stale = true
	cmd := newStatuslineCmd(&fakeResolver{st: st})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(bytes.NewBufferString("{}")) // no rate_limits → engine path
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	line := strings.TrimSpace(out.String())
	if !strings.HasPrefix(line, "≈ ") {
		t.Fatalf("stale state should render with ≈ prefix: %q", line)
	}
	if !strings.Contains(line, "5h ▮▮▮▮▯ 72%") {
		t.Fatalf("line missing window gauge: %q", line)
	}
}

func TestStatuslinePrefersStdinRateLimits(t *testing.T) {
	// When stdin carries rate_limits, the resolver must NOT be consulted.
	t.Setenv("NO_COLOR", "1")
	cmd := newStatuslineCmd(&fakeResolver{st: authErrorState()})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(bytes.NewBufferString(
		`{"rate_limits":{"five_hour":{"used_percentage":18.2,"resets_at":"2026-06-12T12:00:00Z"}}}`))
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	line := strings.TrimSpace(out.String())
	if !strings.Contains(line, "5h ▮▯▯▯▯ 18%") {
		t.Fatalf("rate_limits path not taken (resolver would say ⚠ login): %q", line)
	}
}

func TestFormatStatuslineColorsByThreshold(t *testing.T) {
	// Alert-only coloring: 91% → muted red, 72% → muted yellow.
	now := time.Now().UTC()
	st := snapshotState(91, 72, now.Add(2*time.Hour))

	line := formatStatusline(st, now, true)
	if !strings.Contains(line, ansiRed+"▮▮▮▮▮ 91%"+ansiReset) {
		t.Fatalf("missing red gauge for 91%%: %q", line)
	}
	if !strings.Contains(line, ansiYellow+"▮▮▮▮▯ 72%"+ansiReset) {
		t.Fatalf("missing yellow gauge for 72%%: %q", line)
	}

	// Healthy windows (<60%) get no alert color; only gray icon/label chrome.
	calm := snapshotState(23, 41, now.Add(2*time.Hour))
	line = formatStatusline(calm, now, true)
	if strings.Contains(line, ansiRed) || strings.Contains(line, ansiYellow) {
		t.Fatalf("calm line should carry no alert colors: %q", line)
	}
	if !strings.Contains(line, ansiGray+"◷ 5h"+ansiReset+" ▮▯▯▯▯ 23%") {
		t.Fatalf("missing gray chrome with plain gauge: %q", line)
	}

	// Stale renders the whole line gray with no per-window colors.
	st.Stale = true
	line = formatStatusline(st, now, true)
	if !strings.HasPrefix(line, ansiGray+"≈ ") {
		t.Fatalf("stale line should be gray: %q", line)
	}
	if strings.Contains(line, ansiYellow) || strings.Contains(line, ansiRed) {
		t.Fatalf("stale line should not keep window colors: %q", line)
	}

	// colored=false yields plain text only.
	st.Stale = false
	line = formatStatusline(st, now, false)
	if strings.Contains(line, "\x1b[") {
		t.Fatalf("uncolored line contains ANSI escapes: %q", line)
	}
}
