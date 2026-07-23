package usage

import "time"

const SuspectDropThreshold = 30

func Reconcile(prev, next *Snapshot, now time.Time) *Snapshot {
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

func reconcileScoped(prev, next map[string]*Window, now time.Time) map[string]*Window {
	if len(next) == 0 {
		return nil
	}
	out := make(map[string]*Window, len(next))
	for k, nw := range next {
		out[k] = reconcileWindow(prev[k], nw, now)
	}
	return out
}

func reconcileWindow(prev, next *Window, now time.Time) *Window {
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
