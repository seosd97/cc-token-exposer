package claude

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/provider"
	"github.com/seosd97/cc-token-exposer/internal/schema"
)

func ParseStatuslineStdin(doc []byte, now time.Time) *schema.Snapshot {
	var in struct {
		RateLimits json.RawMessage `json:"rate_limits"`
	}
	if json.Unmarshal(doc, &in) != nil {
		return nil
	}
	snap, ok := snapshotFromRateLimits(in.RateLimits, now)
	if !ok {
		return nil
	}
	return snap
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
	w.resetsAt = provider.ParseTolerantTime(raw.ResetsAt)
	return nil
}

func (w *rlWindow) toWindow() *schema.Window {
	if w == nil || !w.hasUsed {
		return nil
	}
	return &schema.Window{
		Utilization: w.used,
		ResetsAt:    w.resetsAt,
	}
}

type rlScoped struct {
	DisplayName string          `json:"display_name"`
	Utilization *float64        `json:"utilization"`
	ResetsAt    json.RawMessage `json:"resets_at"`
}

func snapshotFromRateLimits(raw json.RawMessage, now time.Time) (*schema.Snapshot, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	var obj struct {
		FiveHour     *rlWindow  `json:"five_hour"`
		SevenDay     *rlWindow  `json:"seven_day"`
		SevenDayOpus *rlWindow  `json:"seven_day_opus"`
		ModelScoped  []rlScoped `json:"model_scoped"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}

	snap := &schema.Snapshot{FetchedAt: now}
	n := 0
	if w := obj.FiveHour.toWindow(); w != nil {
		snap.FiveHour = w
		n++
	}
	if w := obj.SevenDay.toWindow(); w != nil {
		snap.SevenDay = w
		n++
	}
	var scoped map[string]*schema.Window
	for _, ms := range obj.ModelScoped {
		if ms.DisplayName == "" || ms.Utilization == nil {
			continue
		}
		if scoped == nil {
			scoped = make(map[string]*schema.Window, len(obj.ModelScoped))
		}
		scoped[ms.DisplayName] = &schema.Window{
			Utilization: *ms.Utilization,
			ResetsAt:    provider.ParseTolerantTime(ms.ResetsAt),
		}
		n++
	}
	if m, added := backfillOpus(scoped, obj.SevenDayOpus.toWindow()); added {
		scoped = m
		n++
	}
	snap.ScopedLimits = scoped
	if n == 0 {
		return nil, false
	}
	return snap, true
}
