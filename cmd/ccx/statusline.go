package main

import (
	"bytes"
	"context"
	"encoding/json"
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
across Claude Code versions, #40094), it is used opportunistically and no cache
or API access happens at all.

Install: add to ~/.claude/settings.json

    { "statusLine": { "type": "command", "command": "ccx statusline" } }`

func newStatuslineCmd(res resolver) *cobra.Command {
	return &cobra.Command{
		Use:   "statusline",
		Short: "Print a one-line plan-usage status for the Claude Code statusline",
		Long:  statuslineLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			now := time.Now().UTC()
			out := cmd.OutOrStdout()
			colored := os.Getenv("NO_COLOR") == ""

			stdin := cmd.InOrStdin()
			if !isTerminal(stdin) {
				if in, err := readStatuslineInput(stdin); err == nil {
					if st, ok := stateFromRateLimits(in.RateLimits, now); ok {
						fmt.Fprintln(out, formatStatusline(st, now, colored))
						return nil
					}
				}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), statuslineTimeout)
			defer cancel()

			fmt.Fprintln(out, formatStatusline(res.Resolve(ctx), now, colored))
			return nil
		},
	}
}

func formatStatusline(st *schema.State, now time.Time, colored bool) string {
	if st == nil {
		return "⚠ ccx"
	}
	authBroken := st.Auth == schema.AuthExpired || st.Auth == schema.AuthMissing
	paintWindows := colored && !st.Stale

	var parts []string
	if s := st.Snapshot; s != nil {
		for _, win := range []struct {
			icon  string
			label string
			w     *schema.Window
		}{{"◷", "5h", s.FiveHour}, {"◷", "7d", s.SevenDay}, {"✦", "opus", s.SevenDayOpus}} {
			if win.w == nil {
				continue
			}
			head := paint(win.icon+" "+win.label, ansiGray, paintWindows)
			gaugeAndPct := fmt.Sprintf("%s %d%%", gauge(win.w.Utilization), pct(win.w.Utilization))
			seg := paint(gaugeAndPct, utilColor(win.w.Utilization), paintWindows)
			if win.w.ResetsAt.After(now) {
				seg += " " + paint("↻ "+humanizeDuration(win.w.ResetsAt.Sub(now)), ansiGray, paintWindows)
			}
			parts = append(parts, head+" "+seg)
		}
	}

	if len(parts) == 0 {
		if authBroken {
			return paint("⚠ login", ansiYellow, colored)
		}
		if lh := st.LimitHit; lh != nil {
			if lh.ResetsAt != nil && lh.ResetsAt.After(now) {
				return paint("⛔ ↻ "+humanizeDuration(lh.ResetsAt.Sub(now)), ansiRed, colored)
			}
			return paint("⛔ limit", ansiRed, colored)
		}
		return "⚠ ccx"
	}

	if authBroken {
		parts = append(parts, paint("⚠ login", ansiYellow, paintWindows))
	}

	line := strings.Join(parts, " · ")
	if st.Stale {
		line = paint("≈ "+line, ansiGray, colored)
	}
	return line
}

type statuslineInput struct {
	RateLimits json.RawMessage `json:"rate_limits"`
}

// isTerminal reports whether r is an interactive terminal, so reads of an
// unpiped stdin can be skipped instead of blocking.
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

func readStatuslineInput(r io.Reader) (statuslineInput, error) {
	var in statuslineInput
	if r == nil {
		return in, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, maxStdinBytes))
	if err != nil {
		return in, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return in, nil
	}
	_ = json.Unmarshal(data, &in)
	return in, nil
}

type rlWindow struct {
	used     float64
	hasUsed  bool
	resetsAt time.Time
}

func (w *rlWindow) UnmarshalJSON(b []byte) error {
	var raw struct {
		UsedPercentage *float64        `json:"used_percentage"`
		Utilization    *float64        `json:"utilization"`
		ResetsAt       json.RawMessage `json:"resets_at"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	switch {
	case raw.UsedPercentage != nil:
		w.used, w.hasUsed = *raw.UsedPercentage, true
	case raw.Utilization != nil:
		w.used, w.hasUsed = *raw.Utilization, true
	}
	w.resetsAt = parseTolerantTime(raw.ResetsAt)
	return nil
}

func (w *rlWindow) toSchema() *schema.Window {
	if w == nil || !w.hasUsed {
		return nil
	}
	return &schema.Window{
		Utilization: w.used,
		ResetsAt:    w.resetsAt,
	}
}

// parseTolerantTime parses an ISO-8601 string or an epoch number (seconds, or
// milliseconds when large); returns the zero time on any failure.
func parseTolerantTime(raw json.RawMessage) time.Time {
	if len(bytes.TrimSpace(raw)) == 0 {
		return time.Time{}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
		return time.Time{}
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		if n > 1e12 {
			return time.UnixMilli(int64(n)).UTC()
		}
		return time.Unix(int64(n), 0).UTC()
	}
	return time.Time{}
}

func stateFromRateLimits(raw json.RawMessage, now time.Time) (*schema.State, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	var obj struct {
		FiveHour     *rlWindow `json:"five_hour"`
		SevenDay     *rlWindow `json:"seven_day"`
		SevenDayOpus *rlWindow `json:"seven_day_opus"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}

	snap := &schema.Snapshot{FetchedAt: now}
	n := 0
	if w := obj.FiveHour.toSchema(); w != nil {
		snap.FiveHour = w
		n++
	}
	if w := obj.SevenDay.toSchema(); w != nil {
		snap.SevenDay = w
		n++
	}
	if w := obj.SevenDayOpus.toSchema(); w != nil {
		snap.SevenDayOpus = w
		n++
	}
	if n == 0 {
		return nil, false
	}

	return &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeSnapshot,
		Source:        schema.SourceOAuth,
		Auth:          schema.AuthOK,
		Snapshot:      snap,
	}, true
}
