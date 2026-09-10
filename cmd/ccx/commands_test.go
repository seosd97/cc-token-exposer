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
	st       *schema.State
	stdin    *schema.Snapshot
	resolved int
	stdinRes int
	detached int
}

func (f *fakeResolver) Resolve(context.Context) *schema.State {
	f.resolved++
	return f.st
}

func (f *fakeResolver) ResolveStdin(_ context.Context, stdin *schema.Snapshot) *schema.State {
	f.stdinRes++
	f.stdin = stdin
	return f.st
}

func (f *fakeResolver) ResolveDetached(context.Context) *schema.State {
	f.detached++
	return f.st
}

func claudeOnly(r resolver) providers { return providers{schema.ProviderClaude: r} }

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
	cmd := newNowCmd(claudeOnly(&fakeResolver{st: snapshotState(63.4, 44, time.Now().UTC().Add(2*time.Hour))}))
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
	cmd := newNowCmd(claudeOnly(&fakeResolver{st: authErrorState()}))
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
	cmd := newNowCmd(claudeOnly(&fakeResolver{st: snapshotState(21.5, 35, time.Now().UTC().Add(time.Hour))}))
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
	cmd := newNowCmd(claudeOnly(&fakeResolver{st: st}))
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
	cmd := newNowCmd(claudeOnly(&fakeResolver{st: snapshotState(47, 23, time.Now().UTC().Add(2*time.Hour))}))
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
	cmd := newStatuslineCmd(claudeOnly(&fakeResolver{st: st}))
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
	cmd := newStatuslineCmd(claudeOnly(res))
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
	cmd := newStatuslineCmd(claudeOnly(res))
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
	cmd := newStatuslineCmd(claudeOnly(&fakeResolver{st: st}))
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
	cmd := newStatuslineCmd(claudeOnly(engine.New(engine.Options{})))
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

func taggedState(provider string, fiveHour, sevenDay float64, resetsAt time.Time) *schema.State {
	st := snapshotState(fiveHour, sevenDay, resetsAt)
	st.Provider = provider
	return st
}

func TestParseProviderList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{"claude"}},
		{"  ", []string{"claude"}},
		{"claude", []string{"claude"}},
		{"claude, codex", []string{"claude", "codex"}},
		{"Codex,codex,CLAUDE", []string{"codex", "claude"}},
		{",codex,", []string{"codex"}},
	}
	for _, tc := range cases {
		got := parseProviderList(tc.in)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("parseProviderList(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNowUnknownProviderFailsLoudly(t *testing.T) {
	cmd := newNowCmd(claudeOnly(&fakeResolver{st: snapshotState(1, 2, time.Now().UTC().Add(time.Hour))}))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--provider", "nope"})

	err := cmd.Execute()
	if err == nil || errors.Is(err, errSilentExit) || !strings.Contains(err.Error(), `unknown provider "nope"`) {
		t.Fatalf("err = %v, want an unknown-provider error", err)
	}
	if out.Len() != 0 {
		t.Fatalf("nothing should be printed for an unknown provider, got %q", out.String())
	}
}

func TestNowRendersOneBlockPerProvider(t *testing.T) {
	reset := time.Now().UTC().Add(2 * time.Hour)
	ps := providers{
		schema.ProviderClaude: &fakeResolver{st: taggedState(schema.ProviderClaude, 63, 44, reset)},
		schema.ProviderCodex:  &fakeResolver{st: taggedState(schema.ProviderCodex, 18, 60, reset)},
	}
	cmd := newNowCmd(ps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--provider", "claude,codex"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	claudeAt, codexAt := strings.Index(got, "claude\n"), strings.Index(got, "codex\n")
	if claudeAt < 0 || codexAt < 0 || claudeAt > codexAt {
		t.Fatalf("blocks missing or out of order:\n%s", got)
	}
	if !strings.Contains(got, "63%") || !strings.Contains(got, "18%") {
		t.Fatalf("both providers' windows should render:\n%s", got)
	}
	if !strings.Contains(got, "source: live\n\ncodex\n") {
		t.Fatalf("blocks should be separated by a blank line:\n%s", got)
	}
}

func TestNowSingleProviderPrintsNoHeader(t *testing.T) {
	cmd := newNowCmd(claudeOnly(&fakeResolver{st: taggedState(schema.ProviderClaude, 63, 44, time.Now().UTC().Add(time.Hour))}))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.HasPrefix(out.String(), "claude\n") {
		t.Fatalf("single provider output must stay header-less:\n%s", out.String())
	}
}

func TestNowJSONEmitsOneLinePerProvider(t *testing.T) {
	reset := time.Now().UTC().Add(2 * time.Hour)
	ps := providers{
		schema.ProviderClaude: &fakeResolver{st: taggedState(schema.ProviderClaude, 63, 44, reset)},
		schema.ProviderCodex:  &fakeResolver{st: taggedState(schema.ProviderCodex, 18, 60, reset)},
	}
	cmd := newNowCmd(ps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json", "--provider", "codex,claude"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 NDJSON lines, got %d:\n%s", len(lines), out.String())
	}
	for i, want := range []string{schema.ProviderCodex, schema.ProviderClaude} {
		var st schema.State
		if err := json.Unmarshal([]byte(lines[i]), &st); err != nil {
			t.Fatalf("line %d is not a JSON State: %v", i, err)
		}
		if st.Provider != want {
			t.Fatalf("line %d provider = %q, want %q (flag order preserved)", i, st.Provider, want)
		}
	}
}

func TestNowExitsNonZeroWhenAnyProviderErrors(t *testing.T) {
	ps := providers{
		schema.ProviderClaude: &fakeResolver{st: taggedState(schema.ProviderClaude, 63, 44, time.Now().UTC().Add(time.Hour))},
		schema.ProviderCodex:  &fakeResolver{st: authErrorState()},
	}
	cmd := newNowCmd(ps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--provider", "claude,codex"})

	if err := cmd.Execute(); !errors.Is(err, errSilentExit) {
		t.Fatalf("err = %v, want errSilentExit", err)
	}
	if !strings.Contains(out.String(), "63%") || !strings.Contains(out.String(), "no credentials") {
		t.Fatalf("both the healthy block and the error block should print:\n%s", out.String())
	}
}

func TestRefreshResolvesOnlyTheNamedProvider(t *testing.T) {
	claude := &fakeResolver{st: snapshotState(1, 2, time.Now().UTC().Add(time.Hour))}
	codex := &fakeResolver{st: snapshotState(3, 4, time.Now().UTC().Add(time.Hour))}
	ps := providers{schema.ProviderClaude: claude, schema.ProviderCodex: codex}

	cmd := newRefreshCmd(ps)
	cmd.SetArgs([]string{"--provider", "codex"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if codex.resolved != 1 || claude.resolved != 0 {
		t.Fatalf("resolved claude=%d codex=%d, want 0/1", claude.resolved, codex.resolved)
	}

	cmd = newRefreshCmd(ps)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if claude.resolved != 1 {
		t.Fatalf("flag-less refresh should default to claude, resolved=%d", claude.resolved)
	}

	cmd = newRefreshCmd(ps)
	cmd.SetArgs([]string{"--provider", "claude,codex"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("refresh must reject a provider list")
	}
}

func TestStatuslineRoutesStdinToClaudeAndDetachedToOthers(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	reset := time.Now().UTC().Add(4 * time.Hour)
	claude := &fakeResolver{st: taggedState(schema.ProviderClaude, 72, 41, reset)}
	codex := &fakeResolver{st: taggedState(schema.ProviderCodex, 18, 60, reset)}
	cmd := newStatuslineCmd(providers{schema.ProviderClaude: claude, schema.ProviderCodex: codex})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(bytes.NewBufferString(`{"rate_limits":{"five_hour":{"used_percentage":72,"resets_at":"2099-01-01T00:00:00Z"}}}`))
	cmd.SetArgs([]string{"--provider", "claude,codex"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if claude.stdinRes != 1 || claude.detached != 0 || claude.stdin == nil {
		t.Fatalf("claude should be served via ResolveStdin with the piped snapshot: stdin=%d detached=%d snap=%v", claude.stdinRes, claude.detached, claude.stdin)
	}
	if codex.detached != 1 || codex.stdinRes != 0 || codex.resolved != 0 {
		t.Fatalf("codex should be served via ResolveDetached only: detached=%d stdin=%d resolved=%d", codex.detached, codex.stdinRes, codex.resolved)
	}
	line := strings.TrimSpace(out.String())
	if !strings.Contains(line, "5h ▮▮▮▮▯ 72%") || !strings.Contains(line, "│ codex ◷ 5h ▮▯▯▯▯ 18%") {
		t.Fatalf("both groups should render with the codex tag: %q", line)
	}
}

func TestStatuslineIgnoresUnknownProviderNames(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	cmd := newStatuslineCmd(claudeOnly(&fakeResolver{st: snapshotState(23, 41, time.Now().UTC().Add(time.Hour))}))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(bytes.NewBufferString("{}"))
	cmd.SetArgs([]string{"--provider", "claude,nope"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("statusline must never fail on an unknown provider: %v", err)
	}
	line := strings.TrimSpace(out.String())
	if !strings.HasPrefix(line, "◷ 5h") || strings.Contains(line, "│") {
		t.Fatalf("unknown provider should be skipped silently: %q", line)
	}

	cmd = newStatuslineCmd(claudeOnly(&fakeResolver{st: snapshotState(23, 41, time.Now().UTC().Add(time.Hour))}))
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetIn(bytes.NewBufferString("{}"))
	cmd.SetArgs([]string{"--provider", "nope"})
	if err := cmd.Execute(); err != nil || strings.TrimSpace(out.String()) != "⚠ ccx" {
		t.Fatalf("no known provider should degrade to ⚠ ccx (err=%v): %q", err, out.String())
	}
}
