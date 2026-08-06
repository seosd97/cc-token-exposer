package usage

import "github.com/seosd97/cc-token-exposer/internal/schema"

// Overlay layers fresh over base per window: anything fresh provides wins,
// anything it lacks falls back to base. ExtraUsage always comes from fresh
// (Reconcile does the same). Reports whether base contributed.
func Overlay(fresh, base *schema.Snapshot) (*schema.Snapshot, bool) {
	if fresh == nil {
		return base, base != nil
	}
	if base == nil {
		return fresh, false
	}
	out := *fresh
	used := false
	if out.FiveHour == nil && base.FiveHour != nil {
		out.FiveHour = base.FiveHour
		used = true
	}
	if out.SevenDay == nil && base.SevenDay != nil {
		out.SevenDay = base.SevenDay
		used = true
	}
	if scopedMissing(out.ScopedLimits, base.ScopedLimits) {
		m := make(map[string]*schema.Window, len(out.ScopedLimits)+len(base.ScopedLimits))
		for name, w := range out.ScopedLimits {
			m[name] = w
		}
		for name, w := range base.ScopedLimits {
			if w == nil {
				continue
			}
			if _, ok := m[name]; !ok {
				m[name] = w
				used = true
			}
		}
		out.ScopedLimits = m
	}
	return &out, used
}

func scopedMissing(fresh, base map[string]*schema.Window) bool {
	for name, w := range base {
		if w == nil {
			continue
		}
		if _, ok := fresh[name]; !ok {
			return true
		}
	}
	return false
}
