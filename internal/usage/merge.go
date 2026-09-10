package usage

import "github.com/seosd97/cc-token-exposer/internal/schema"

func Overlay(fresh, base *schema.Snapshot) (*schema.Snapshot, bool) {
	if fresh == nil {
		return base, base != nil
	}
	if base == nil {
		return fresh, false
	}
	out := *fresh
	used := false
	if w, ok := overlayWindow(fresh.FiveHour, base.FiveHour); ok {
		out.FiveHour, used = w, true
	}
	if w, ok := overlayWindow(fresh.SevenDay, base.SevenDay); ok {
		out.SevenDay, used = w, true
	}
	scopedOwned := false
	for name, bw := range base.ScopedLimits {
		w, ok := overlayWindow(out.ScopedLimits[name], bw)
		if !ok {
			continue
		}
		if out.ScopedLimits == nil {
			out.ScopedLimits = make(map[string]*schema.Window, len(base.ScopedLimits))
			scopedOwned = true
		} else if !scopedOwned {
			m := make(map[string]*schema.Window, len(fresh.ScopedLimits)+len(base.ScopedLimits))
			for n, v := range fresh.ScopedLimits {
				m[n] = v
			}
			out.ScopedLimits = m
			scopedOwned = true
		}
		out.ScopedLimits[name] = w
		used = true
	}
	return &out, used
}

func overlayWindow(fresh, base *schema.Window) (*schema.Window, bool) {
	if fresh == nil {
		return base, base != nil
	}
	if base == nil || !base.ResetsAt.After(fresh.ResetsAt) {
		return fresh, false
	}
	w := *fresh
	w.ResetsAt = base.ResetsAt
	return &w, true
}
