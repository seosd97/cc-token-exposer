package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

func win(util float64, reset time.Time) *schema.Window {
	return &schema.Window{Utilization: util, ResetsAt: reset}
}

func TestFormatStatusline(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	reset5h := now.Add(4*time.Hour + 12*time.Minute)
	reset7d := now.Add(3 * 24 * time.Hour)

	cases := []struct {
		name string
		st   *schema.State
		want string
	}{
		{
			name: "nil state",
			st:   nil,
			want: "⚠ ccx",
		},
		{
			name: "auth missing",
			st:   &schema.State{Auth: schema.AuthMissing, Type: schema.TypeError},
			want: "⚠ login",
		},
		{
			name: "auth expired",
			st:   &schema.State{Auth: schema.AuthExpired, Type: schema.TypeError},
			want: "⚠ login",
		},
		{
			name: "full snapshot with inline reset",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour: win(23, reset5h),
					SevenDay: win(41, reset7d),
				},
			},
			want: "◷ 5h ▮▯▯▯▯ 23% ↻ 4h12m · ◷ 7d ▮▮▯▯▯ 41% ↻ 3d0h",
		},
		{
			name: "includes opus window",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour:     win(10, reset5h),
					SevenDay:     win(20, reset7d),
					ScopedLimits: map[string]*schema.Window{"Opus": win(5, reset7d)},
				},
			},
			want: "◷ 5h ▮▯▯▯▯ 10% ↻ 4h12m · ◷ 7d ▮▯▯▯▯ 20% ↻ 3d0h · ✧ opus ▯▯▯▯▯ 5% ↻ 3d0h",
		},
		{
			name: "includes fable window",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour:     win(10, reset5h),
					SevenDay:     win(20, reset7d),
					ScopedLimits: map[string]*schema.Window{"Fable": win(94, reset7d)},
				},
			},
			want: "◷ 5h ▮▯▯▯▯ 10% ↻ 4h12m · ◷ 7d ▮▯▯▯▯ 20% ↻ 3d0h · ✧ fable ▮▮▮▮▮ 94% ↻ 3d0h",
		},
		{
			name: "multiple scoped models sorted alphabetically",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour: win(10, reset5h),
					SevenDay: win(20, reset7d),
					ScopedLimits: map[string]*schema.Window{
						"Opus":   win(30, reset7d),
						"Fable":  win(50, reset7d),
						"Sonnet": win(70, reset7d),
					},
				},
			},
			want: "◷ 5h ▮▯▯▯▯ 10% ↻ 4h12m · ◷ 7d ▮▯▯▯▯ 20% ↻ 3d0h · ✧ fable ▮▮▮▯▯ 50% ↻ 3d0h · ✧ opus ▮▮▯▯▯ 30% ↻ 3d0h · ✧ sonnet ▮▮▮▮▯ 70% ↻ 3d0h",
		},
		{
			name: "stale prefixes approx sign",
			st: &schema.State{
				Auth:  schema.AuthOK,
				Stale: true,
				Snapshot: &schema.Snapshot{
					FiveHour: win(50, reset5h),
				},
			},
			want: "≈ ◷ 5h ▮▮▮▯▯ 50% ↻ 4h12m",
		},
		{
			name: "auth expired but stale snapshot keeps last-known values",
			st: &schema.State{
				Auth:  schema.AuthExpired,
				Stale: true,
				Snapshot: &schema.Snapshot{
					FiveHour: win(23, reset5h),
					SevenDay: win(41, reset7d),
				},
			},
			want: "≈ ◷ 5h ▮▯▯▯▯ 23% ↻ 4h12m · ◷ 7d ▮▮▯▯▯ 41% ↻ 3d0h · ⚠ login",
		},
		{
			name: "window without any reset omits the reset part",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour: win(50, time.Time{}),
				},
			},
			want: "◷ 5h ▮▮▮▯▯ 50%",
		},
		{
			name: "all resets elapsed omits the reset part",
			st: &schema.State{
				Auth: schema.AuthOK,
				Snapshot: &schema.Snapshot{
					FiveHour: win(50, now.Add(-time.Hour)),
				},
			},
			want: "◷ 5h ▮▮▮▯▯ 50%",
		},
		{
			name: "limit hit with already-elapsed reset renders nothing",
			st: func() *schema.State {
				r := now.Add(-time.Hour)
				return &schema.State{Auth: schema.AuthOK, LimitHit: &schema.LimitHit{ResetsAt: &r}}
			}(),
			want: "⚠ ccx",
		},
		{
			name: "no windows but limit hit with reset",
			st: func() *schema.State {
				r := now.Add(2 * time.Hour)
				return &schema.State{Auth: schema.AuthOK, LimitHit: &schema.LimitHit{ResetsAt: &r}}
			}(),
			want: "⛔ ↻ 2h00m",
		},
		{
			name: "no windows, limit hit without reset",
			st:   &schema.State{Auth: schema.AuthOK, LimitHit: &schema.LimitHit{}},
			want: "⛔ limit",
		},
		{
			name: "auth ok but nothing to show",
			st:   &schema.State{Auth: schema.AuthOK},
			want: "⚠ ccx",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatStatusline(tc.st, now, false)
			if got != tc.want {
				t.Errorf("formatStatusline = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	if isTerminal(strings.NewReader("{}")) {
		t.Error("strings.Reader should not be reported as a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("regular file should not be reported as a terminal")
	}
}

func TestFormatStatuslineGroups(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	reset5h := now.Add(4*time.Hour + 12*time.Minute)
	reset7d := now.Add(3 * 24 * time.Hour)
	claudeOK := &schema.State{Auth: schema.AuthOK, Snapshot: &schema.Snapshot{FiveHour: win(23, reset5h), SevenDay: win(41, reset7d)}}
	codexOK := &schema.State{Auth: schema.AuthOK, Snapshot: &schema.Snapshot{FiveHour: win(18, reset5h), SevenDay: win(60, reset7d)}}

	cases := []struct {
		name   string
		groups []providerLine
		want   string
	}{
		{
			name:   "claude alone is untagged",
			groups: []providerLine{{schema.ProviderClaude, claudeOK}},
			want:   "◷ 5h ▮▯▯▯▯ 23% ↻ 4h12m · ◷ 7d ▮▮▯▯▯ 41% ↻ 3d0h",
		},
		{
			name:   "second provider is tagged and separated",
			groups: []providerLine{{schema.ProviderClaude, claudeOK}, {schema.ProviderCodex, codexOK}},
			want:   "◷ 5h ▮▯▯▯▯ 23% ↻ 4h12m · ◷ 7d ▮▮▯▯▯ 41% ↻ 3d0h │ codex ◷ 5h ▮▯▯▯▯ 18% ↻ 4h12m · ◷ 7d ▮▮▮▯▯ 60% ↻ 3d0h",
		},
		{
			name:   "codex alone is untagged",
			groups: []providerLine{{schema.ProviderCodex, codexOK}},
			want:   "◷ 5h ▮▯▯▯▯ 18% ↻ 4h12m · ◷ 7d ▮▮▮▯▯ 60% ↻ 3d0h",
		},
		{
			name: "the tag keys off configured providers, not rendered groups",
			groups: []providerLine{
				{schema.ProviderClaude, &schema.State{Auth: schema.AuthOK, Type: schema.TypeError, Error: "usage refresh in progress; no cache yet"}},
				{schema.ProviderCodex, codexOK},
			},
			want: "codex ◷ 5h ▮▯▯▯▯ 18% ↻ 4h12m · ◷ 7d ▮▮▮▯▯ 60% ↻ 3d0h",
		},
		{
			name: "stale and login markers stay per group",
			groups: []providerLine{
				{schema.ProviderClaude, &schema.State{Auth: schema.AuthOK, Stale: true, Snapshot: &schema.Snapshot{FiveHour: win(50, reset5h)}}},
				{schema.ProviderCodex, &schema.State{Auth: schema.AuthMissing, Type: schema.TypeError}},
			},
			want: "≈ ◷ 5h ▮▮▮▯▯ 50% ↻ 4h12m │ codex ⚠ login",
		},
		{
			name: "empty groups are dropped",
			groups: []providerLine{
				{schema.ProviderClaude, claudeOK},
				{schema.ProviderCodex, &schema.State{Auth: schema.AuthOK, Type: schema.TypeError, Error: "usage refresh in progress; no cache yet"}},
			},
			want: "◷ 5h ▮▯▯▯▯ 23% ↻ 4h12m · ◷ 7d ▮▮▯▯▯ 41% ↻ 3d0h",
		},
		{
			name: "an account without plan limits renders the no-plan marker",
			groups: []providerLine{
				{schema.ProviderClaude, claudeOK},
				{schema.ProviderCodex, &schema.State{Auth: schema.AuthNoPlan, Type: schema.TypeError, Error: "codex: logged in with an API key; plan limits do not apply"}},
			},
			want: "◷ 5h ▮▯▯▯▯ 23% ↻ 4h12m · ◷ 7d ▮▮▯▯▯ 41% ↻ 3d0h │ codex no plan",
		},
		{
			name:   "codex alone without plan limits is not the generic marker",
			groups: []providerLine{{schema.ProviderCodex, &schema.State{Auth: schema.AuthNoPlan, Type: schema.TypeError, Error: "codex: logged in with an API key; plan limits do not apply"}}},
			want:   "no plan",
		},
		{
			name:   "all groups empty falls back to the generic marker",
			groups: []providerLine{{schema.ProviderClaude, nil}, {schema.ProviderCodex, &schema.State{Auth: schema.AuthOK}}},
			want:   "⚠ ccx",
		},
		{
			name:   "no groups at all",
			groups: nil,
			want:   "⚠ ccx",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatStatuslineGroups(tc.groups, now, false); got != tc.want {
				t.Errorf("formatStatuslineGroups = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatStatuslineGroupsPaintsTheTagGray(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	claude := &schema.State{Auth: schema.AuthOK, Snapshot: &schema.Snapshot{FiveHour: win(23, now.Add(time.Hour))}}
	codex := &schema.State{Auth: schema.AuthOK, Snapshot: &schema.Snapshot{FiveHour: win(18, now.Add(time.Hour))}}
	line := formatStatuslineGroups([]providerLine{{schema.ProviderClaude, claude}, {schema.ProviderCodex, codex}}, now, true)
	if !strings.Contains(line, " │ "+ansiGray+"codex"+ansiReset+" ") {
		t.Fatalf("tag should be gray chrome: %q", line)
	}
	if single := formatStatuslineGroups([]providerLine{{schema.ProviderCodex, codex}}, now, true); strings.Contains(single, "codex") {
		t.Fatalf("a single provider must not be tagged: %q", single)
	}
}

func TestReadStdinDocument(t *testing.T) {
	t.Run("nil reader", func(t *testing.T) {
		if doc := readStdinDocument(nil); doc != nil {
			t.Fatalf("nil reader should yield no document, got %q", doc)
		}
	})

	t.Run("blank stdin", func(t *testing.T) {
		if doc := readStdinDocument(strings.NewReader("  \n")); doc != nil {
			t.Fatalf("blank stdin should yield no document, got %q", doc)
		}
	})

	t.Run("content is passed through untouched", func(t *testing.T) {
		const raw = `{"session_id":"x","rate_limits":{"five_hour":{"utilization":12}}}`
		if doc := readStdinDocument(strings.NewReader(raw)); string(doc) != raw {
			t.Fatalf("document = %q, want the raw stdin", doc)
		}
	})
}

func formatStatusline(st *schema.State, now time.Time, colored bool) string {
	return formatStatuslineGroups([]providerLine{{name: schema.ProviderClaude, state: st}}, now, colored)
}
