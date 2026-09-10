package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

func pct(u float64) int { return int(math.Round(u)) }

func sortedScopedNames(m map[string]*schema.Window) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func renderHuman(st *schema.State, now time.Time) string {
	if st == nil {
		return "⚠ no state\n"
	}
	if st.Type == schema.TypeError {
		return "⚠ " + errorText(st) + "\n"
	}

	var b strings.Builder
	if s := st.Snapshot; s != nil {
		writeWindow(&b, "5h   ", s.FiveHour, now)
		writeWindow(&b, "7d   ", s.SevenDay, now)
		for _, name := range sortedScopedNames(s.ScopedLimits) {
			writeWindow(&b, fmt.Sprintf("%-5s", name), s.ScopedLimits[name], now)
		}
		if s.ExtraUsage != nil && s.ExtraUsage.Utilization != nil {
			fmt.Fprintf(&b, "extra %d%%\n", int(math.Round(*s.ExtraUsage.Utilization)))
		}
	}
	if lh := st.LimitHit; lh != nil {
		b.WriteString("⛔ limit hit")
		if lh.ResetsAt != nil {
			fmt.Fprintf(&b, " · resets in %s", humanizeDuration(lh.ResetsAt.Sub(now)))
		}
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		b.WriteString("no usage data\n")
	}
	if len(st.Drift) > 0 {
		b.WriteString("drift: " + strings.Join(st.Drift, " · ") + "\n")
	}

	b.WriteString(freshnessFooter(st))
	return b.String()
}

func errorText(st *schema.State) string {
	if st.Error != "" {
		return st.Error
	}
	switch st.Auth {
	case schema.AuthMissing:
		return "no credentials found"
	case schema.AuthExpired:
		return "token expired"
	default:
		return "unknown error"
	}
}

func writeWindow(b *strings.Builder, label string, w *schema.Window, now time.Time) {
	if w == nil {
		return
	}
	marker := ""
	if w.Suspect {
		marker = " (suspect)"
	}
	reset := ""
	if !w.ResetsAt.IsZero() {
		reset = " · resets in " + humanizeDuration(w.ResetsAt.Sub(now))
	}
	fmt.Fprintf(b, "%s %3d%%%s%s\n", label, int(math.Round(w.Utilization)), reset, marker)
}

func freshnessFooter(st *schema.State) string {
	switch st.Source {
	case schema.SourceOAuth:
		return "source: live\n"
	case schema.SourceCache:
		if st.Stale {
			return "source: cache · stale" + staleSuffix(st) + "\n"
		}
		return "source: cache\n"
	case schema.SourceStdin:
		if st.Stale {
			return "source: claude code · stale" + staleSuffix(st) + "\n"
		}
		return "source: claude code\n"
	case schema.SourceTranscript:
		return "source: transcript fallback\n"
	default:
		return ""
	}
}

func staleSuffix(st *schema.State) string {
	if st.StaleAge == nil {
		return ""
	}
	return " (" + humanizeDuration(time.Duration(*st.StaleAge)) + " old)"
}

func humanizeDuration(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	d = d.Round(time.Minute)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	mins := d / time.Minute

	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, mins)
	default:
		if mins == 0 {
			return "<1m"
		}
		return fmt.Sprintf("%dm", mins)
	}
}
