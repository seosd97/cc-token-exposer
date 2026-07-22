package engine

import (
	"time"

	"github.com/seosd97/cc-token-exposer/internal/schema"
	"github.com/seosd97/cc-token-exposer/internal/usage"
)

func snapshotState(snap *usage.Snapshot, source string, stale bool, staleAge time.Duration, auth schema.AuthStatus) *schema.State {
	st := &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeSnapshot,
		Source:        source,
		Stale:         stale,
		Auth:          auth,
		Snapshot:      mapSnapshot(snap),
	}
	if stale && staleAge > 0 {
		st.StaleAge = schema.NewDuration(staleAge)
	}
	return st
}

func transcriptState(lh *schema.LimitHit, auth schema.AuthStatus) *schema.State {
	return &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeSnapshot,
		Source:        schema.SourceTranscript,
		Stale:         true,
		Auth:          auth,
		LimitHit:      lh,
	}
}

func errorState(auth schema.AuthStatus, msg string) *schema.State {
	return &schema.State{
		SchemaVersion: schema.Version,
		Type:          schema.TypeError,
		Auth:          auth,
		Error:         msg,
	}
}

func mapSnapshot(s *usage.Snapshot) *schema.Snapshot {
	if s == nil {
		return nil
	}
	snap := &schema.Snapshot{
		FetchedAt:    s.FetchedAt,
		FiveHour:     mapWindow(s.FiveHour),
		SevenDay:     mapWindow(s.SevenDay),
		ScopedLimits: mapScoped(s.ScopedLimits),
		ExtraUsage:   mapExtra(s.ExtraUsage),
	}
	if w, ok := snap.ScopedLimits["Opus"]; ok {
		snap.SevenDayOpus = w
	}
	if w, ok := snap.ScopedLimits["Fable"]; ok {
		snap.SevenDayFable = w
	}
	return snap
}

func mapScoped(m map[string]*usage.Window) map[string]*schema.Window {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]*schema.Window, len(m))
	for k, w := range m {
		out[k] = mapWindow(w)
	}
	return out
}

func mapWindow(w *usage.Window) *schema.Window {
	if w == nil {
		return nil
	}
	return &schema.Window{
		Utilization: w.Utilization,
		ResetsAt:    w.ResetsAt,
		Suspect:     w.Suspect,
	}
}

func mapExtra(x *usage.ExtraUsage) *schema.ExtraUsage {
	if x == nil || x.Utilization == nil {
		return nil
	}
	v := *x.Utilization
	return &schema.ExtraUsage{Utilization: &v}
}
