package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/spf13/cobra"
)

const statuslineTimeout = 12 * time.Second

const maxStdinBytes = 1 << 20

const statuslineLong = `Print a single status line for the Claude Code statusline.

Output is one line, e.g.:

    ◔ 5h ▮▯▯▯▯ 23% 4h12m · ◫ 7d ▮▮▯▯▯ 41% 3d12h

Each window shows a five-cell gauge, percentage, and reset countdown inline.
Gauges stay uncolored below 60% utilization and turn muted yellow (≥60) or
red (>85); icons, labels, and reset countdowns are gray (set NO_COLOR to
disable ANSI). A leading "≈" marks data served stale from cache; "⚠ login"
means credentials are missing or expired. The command is cache-first: Claude
Code calls it every few seconds, so within the cache TTL it never touches the
usage API.

If the statusline stdin session JSON carries a "rate_limits" field (intermittent
across Claude Code versions, #40094), it is used directly; windows it lacks —
or carries without a usable reset time — are filled from the disk cache. A
window the cache knows but stdin lacks or carries reset-less triggers at most
one bounded usage API refresh per cache TTL, so it still tracks the real value.

--provider claude,codex appends a second group for the Codex plan, separated by
"│" and tagged with the provider name; a single provider, whichever it is, is
never tagged. Providers other than claude are served from their own cache and
healed by a detached background refresh, so the line never waits on the
network. A login without plan limits (a Codex API-key login) renders as
"no plan" instead of a login warning.

Install: add to ~/.claude/settings.json

    { "statusLine": { "type": "command", "command": "ccx statusline" } }`

func newStatuslineCmd(ps providers) *cobra.Command {
	var providerFlag string
	cmd := &cobra.Command{
		Use:           "statusline",
		Short:         "Print a one-line plan-usage status for the Claude Code statusline",
		Long:          statuslineLong,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			now := time.Now().UTC()
			out := cmd.OutOrStdout()
			colored := os.Getenv("NO_COLOR") == ""
			stdinDoc := readStdinDocument(cmd.InOrStdin())

			ctx, cancel := context.WithTimeout(cmd.Context(), statuslineTimeout)
			defer cancel()

			var entries []providerEntry
			for _, name := range parseProviderList(providerFlag) {
				if entry, ok := ps[name]; ok {
					entries = append(entries, entry)
				}
			}
			states := resolveEach(entries, func(e providerEntry) *schema.State { return e.resolveStatusline(ctx, stdinDoc, now) })
			groups := make([]providerLine, len(entries))
			for i, e := range entries {
				groups[i] = providerLine{name: e.spec.Name, state: states[i]}
			}

			fmt.Fprintln(out, formatStatuslineGroups(groups, now, colored))
			return nil
		},
	}
	addProviderFlag(cmd, &providerFlag, "provider(s) to show, comma-separated (claude, codex)")
	return cmd
}

type providerLine struct {
	name  string
	state *schema.State
}

func formatStatuslineGroups(groups []providerLine, now time.Time, colored bool) string {
	tagged := len(groups) > 1
	var rendered []string
	for _, g := range groups {
		line, ok := statuslineGroup(g.state, now, colored)
		if !ok {
			continue
		}
		if tagged && g.name != schema.ProviderClaude {
			line = paint(g.name, ansiGray, colored) + " " + line
		}
		rendered = append(rendered, line)
	}
	if len(rendered) == 0 {
		return "⚠ ccx"
	}
	return strings.Join(rendered, " │ ")
}

func statuslineGroup(st *schema.State, now time.Time, colored bool) (string, bool) {
	if st == nil {
		return "", false
	}
	authBroken := st.Auth == schema.AuthExpired || st.Auth == schema.AuthMissing
	paintWindows := colored && !st.Stale

	var parts []string
	if s := st.Snapshot; s != nil {
		for _, e := range []struct {
			icon  string
			label string
			w     *schema.Window
		}{
			{"◷", "5h", s.FiveHour},
			{"◷", "7d", s.SevenDay},
		} {
			if seg := statusSegment(e.icon, e.label, e.w, now, paintWindows); seg != "" {
				parts = append(parts, seg)
			}
		}
		for _, name := range sortedScopedNames(s.ScopedLimits) {
			if seg := statusSegment("✧", strings.ToLower(name), s.ScopedLimits[name], now, paintWindows); seg != "" {
				parts = append(parts, seg)
			}
		}
	}

	if len(parts) == 0 {
		if st.Auth == schema.AuthNoPlan {
			return paint("no plan", ansiGray, colored), true
		}
		if authBroken {
			return paint("⚠ login", ansiYellow, colored), true
		}
		if lh := st.LimitHit; lh.Active(now) {
			if lh.ResetsAt != nil {
				return paint("⛔ ↻ "+humanizeDuration(lh.ResetsAt.Sub(now)), ansiRed, colored), true
			}
			return paint("⛔ limit", ansiRed, colored), true
		}
		return "", false
	}

	if authBroken {
		parts = append(parts, paint("⚠ login", ansiYellow, paintWindows))
	}

	line := strings.Join(parts, " · ")
	if st.Stale {
		line = paint("≈ "+line, ansiGray, colored)
	}
	return line, true
}

func statusSegment(icon, label string, w *schema.Window, now time.Time, colored bool) string {
	if w == nil {
		return ""
	}
	head := paint(icon+" "+label, ansiGray, colored)
	gaugeAndPct := fmt.Sprintf("%s %d%%", gauge(w.Utilization), pct(w.Utilization))
	seg := paint(gaugeAndPct, utilColor(w.Utilization), colored)
	if w.ResetsAt.After(now) {
		seg += " " + paint("↻ "+humanizeDuration(w.ResetsAt.Sub(now)), ansiGray, colored)
	}
	return head + " " + seg
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func readStdinDocument(r io.Reader) []byte {
	if r == nil || isTerminal(r) {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(r, maxStdinBytes))
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return data
}
