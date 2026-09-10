package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/engine"
	"github.com/seosd97/cc-token-exposer/internal/schema"
)

type fakeResolver struct {
	st    *schema.State
	stdin *schema.Snapshot
}

func (f *fakeResolver) Resolve(context.Context) *schema.State { return f.st }

func (f *fakeResolver) ResolveStdin(_ context.Context, stdin *schema.Snapshot) *schema.State {
	f.stdin = stdin
	return f.st
}

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

func TestNowRendersDriftIndicators(t *testing.T) {
	st := snapshotState(47, 23, time.Now().UTC().Add(2*time.Hour))
	st.Drift = []string{"five_hour missing resets_at", `unknown scoped limits kind "x"`}
	cmd := newNowCmd(&fakeResolver{st: st})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "drift: five_hour missing resets_at · unknown scoped limits kind \"x\"") {
		t.Fatalf("output missing drift line:\n%s", got)
	}
}

func TestNowOmitsDriftLineWhenClean(t *testing.T) {
	cmd := newNowCmd(&fakeResolver{st: snapshotState(47, 23, time.Now().UTC().Add(2*time.Hour))})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out.String(), "drift") {
		t.Fatalf("drift line must be absent without indicators:\n%s", out.String())
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
	cmd.SetIn(bytes.NewBufferString("{}"))
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

func TestStatuslinePipesStdinSnapshotToResolver(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	res := &fakeResolver{st: snapshotState(18, 0, time.Now().UTC().Add(2*time.Hour))}
	cmd := newStatuslineCmd(res)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(bytes.NewBufferString(
		`{"rate_limits":{"five_hour":{"used_percentage":18.2,"resets_at":"2026-06-12T12:00:00Z"}}}`))
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.stdin == nil || res.stdin.FiveHour == nil || res.stdin.FiveHour.Utilization != 18.2 {
		t.Fatalf("resolver stdin path not fed from rate_limits: %+v", res.stdin)
	}
	line := strings.TrimSpace(out.String())
	if !strings.Contains(line, "5h ▮▯▯▯▯ 18%") {
		t.Fatalf("line missing stdin window: %q", line)
	}
}

func TestStatuslineNoRateLimitsYieldsNilStdin(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	res := &fakeResolver{st: snapshotState(72, 41, time.Now().UTC().Add(4*time.Hour))}
	cmd := newStatuslineCmd(res)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(bytes.NewBufferString("{}"))
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.stdin != nil {
		t.Fatalf("empty stdin should yield nil snapshot, got %+v", res.stdin)
	}
}

func TestStatuslineOmitsDriftMarkers(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	st := snapshotState(47, 23, time.Now().UTC().Add(2*time.Hour))
	st.Drift = []string{"seven_day missing resets_at"}
	cmd := newStatuslineCmd(&fakeResolver{st: st})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(bytes.NewBufferString("{}"))
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out.String(), "drift") {
		t.Fatalf("statusline must not render drift markers: %q", out.String())
	}
}

func TestStatuslineRendersScopedModelsFromStdin(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	cmd := newStatuslineCmd(engine.New(engine.Options{}))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(bytes.NewBufferString(
		`{"rate_limits":{"five_hour":{"used_percentage":23,"resets_at":"2099-01-01T00:00:00Z"},` +
			`"model_scoped":[{"display_name":"Fable","utilization":55.0,"resets_at":"2099-01-01T00:00:00Z"}]}}`))
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	line := strings.TrimSpace(out.String())
	if !strings.Contains(line, "5h ▮▯▯▯▯ 23%") || !strings.Contains(line, "✧ fable ▮▮▮▯▯ 55%") {
		t.Fatalf("scoped model from stdin not rendered: %q", line)
	}
}

func TestFormatStatuslineColorsByThreshold(t *testing.T) {
	now := time.Now().UTC()
	st := snapshotState(91, 72, now.Add(2*time.Hour))

	line := formatStatusline(st, now, true)
	if !strings.Contains(line, ansiRed+"▮▮▮▮▮ 91%"+ansiReset) {
		t.Fatalf("missing red gauge for 91%%: %q", line)
	}
	if !strings.Contains(line, ansiYellow+"▮▮▮▮▯ 72%"+ansiReset) {
		t.Fatalf("missing yellow gauge for 72%%: %q", line)
	}

	calm := snapshotState(23, 41, now.Add(2*time.Hour))
	line = formatStatusline(calm, now, true)
	if strings.Contains(line, ansiRed) || strings.Contains(line, ansiYellow) {
		t.Fatalf("calm line should carry no alert colors: %q", line)
	}
	if !strings.Contains(line, ansiGray+"◷ 5h"+ansiReset+" ▮▯▯▯▯ 23%") {
		t.Fatalf("missing gray chrome with plain gauge: %q", line)
	}

	st.Stale = true
	line = formatStatusline(st, now, true)
	if !strings.HasPrefix(line, ansiGray+"≈ ") {
		t.Fatalf("stale line should be gray: %q", line)
	}
	if strings.Contains(line, ansiYellow) || strings.Contains(line, ansiRed) {
		t.Fatalf("stale line should not keep window colors: %q", line)
	}

	st.Stale = false
	line = formatStatusline(st, now, false)
	if strings.Contains(line, "\x1b[") {
		t.Fatalf("uncolored line contains ANSI escapes: %q", line)
	}
}
