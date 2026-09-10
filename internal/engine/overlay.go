package engine

import (
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
)

func overlay(fresh, base *schema.Snapshot, now time.Time) (*schema.Snapshot, bool) {
	if fresh == nil {
		return base, base != nil
	}
	if base == nil {
		return fresh, false
	}
	out := *fresh
	used := false
	if w, ok := overlayWindow(fresh.FiveHour, base.FiveHour, now); ok {
		out.FiveHour, used = w, true
	}
	if w, ok := overlayWindow(fresh.SevenDay, base.SevenDay, now); ok {
		out.SevenDay, used = w, true
	}
	var scoped map[string]*schema.Window
	for name, bw := range base.ScopedLimits {
		w, ok := overlayWindow(fresh.ScopedLimits[name], bw, now)
		if !ok {
			continue
		}
		if scoped == nil {
			scoped = make(map[string]*schema.Window, len(fresh.ScopedLimits)+len(base.ScopedLimits))
			for n, fw := range fresh.ScopedLimits {
				scoped[n] = fw
			}
		}
		scoped[name], used = w, true
	}
	if scoped != nil {
		out.ScopedLimits = scoped
	}
	return &out, used
}

const resetJitterTolerance = time.Minute

func overlayWindow(fresh, base *schema.Window, now time.Time) (*schema.Window, bool) {
	if fresh == nil {
		return base, base != nil
	}
	if base == nil || !base.ResetsAt.After(now) || !base.ResetsAt.After(fresh.ResetsAt) {
		return fresh, false
	}
	if elapsed(fresh, now) && base.ResetsAt.Sub(fresh.ResetsAt) > resetJitterTolerance {
		return base, true
	}
	w := *fresh
	w.ResetsAt = base.ResetsAt
	return &w, true
}

func elapsed(w *schema.Window, now time.Time) bool {
	return !w.ResetsAt.IsZero() && !w.ResetsAt.After(now)
}
