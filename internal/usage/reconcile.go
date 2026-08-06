package usage

import (
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

const SuspectDropThreshold = 30

func Reconcile(prev, next *schema.Snapshot, now time.Time) *schema.Snapshot {
	if next == nil {
		return nil
	}
	if prev == nil {
		return next
	}
	out := *next
	out.FiveHour = reconcileWindow(prev.FiveHour, next.FiveHour, now)
	out.SevenDay = reconcileWindow(prev.SevenDay, next.SevenDay, now)
	out.ScopedLimits = reconcileScoped(prev.ScopedLimits, next.ScopedLimits, now)
	return &out
}

func reconcileScoped(prev, next map[string]*schema.Window, now time.Time) map[string]*schema.Window {
	if len(next) == 0 {
		return nil
	}
	out := make(map[string]*schema.Window, len(next))
	for k, nw := range next {
		out[k] = reconcileWindow(prev[k], nw, now)
	}
	return out
}

func reconcileWindow(prev, next *schema.Window, now time.Time) *schema.Window {
	if prev == nil || next == nil {
		return next
	}
	sameWindow := next.ResetsAt.Equal(prev.ResetsAt) && next.ResetsAt.After(now)
	if sameWindow && prev.Utilization-next.Utilization >= SuspectDropThreshold {
		kept := *prev
		kept.Suspect = true
		return &kept
	}
	return next
}
