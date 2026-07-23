package usage

import (
	"testing"
	"time"
)

var mergeBase = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

func owin(util float64) *Window {
	return &Window{Utilization: util, ResetsAt: mergeBase.Add(time.Hour)}
}

func TestOverlayNilFreshReturnsBase(t *testing.T) {
	base := &Snapshot{FiveHour: owin(10)}
	got, used := Overlay(nil, base)
	if got != base || !used {
		t.Fatalf("got %v used=%v, want base/true", got, used)
	}
	if got, used := Overlay(nil, nil); got != nil || used {
		t.Fatalf("nil/nil = %v/%v, want nil/false", got, used)
	}
}

func TestOverlayFreshCompleteIgnoresBase(t *testing.T) {
	fresh := &Snapshot{
		FiveHour:     owin(20),
		SevenDay:     owin(30),
		ScopedLimits: map[string]*Window{"Fable": owin(40), "Opus": owin(50)},
		ExtraUsage:   &ExtraUsage{Utilization: ptr(5.0)},
	}
	base := &Snapshot{
		FiveHour:     owin(99),
		SevenDay:     owin(99),
		ScopedLimits: map[string]*Window{"Fable": owin(99)},
	}
	got, used := Overlay(fresh, base)
	if used {
		t.Fatal("base should not contribute when fresh covers everything base has")
	}
	if got.FiveHour.Utilization != 20 || got.SevenDay.Utilization != 30 ||
		got.ScopedLimits["Fable"].Utilization != 40 || got.ScopedLimits["Opus"].Utilization != 50 {
		t.Fatalf("got %+v, want fresh values throughout", got)
	}
}

func TestOverlayFillsGapsFromBase(t *testing.T) {
	fresh := &Snapshot{FiveHour: owin(20)}
	base := &Snapshot{
		FiveHour:     owin(99),
		SevenDay:     owin(30),
		ScopedLimits: map[string]*Window{"Fable": owin(40), "Opus": owin(50)},
		ExtraUsage:   &ExtraUsage{Utilization: ptr(5.0)},
	}
	got, used := Overlay(fresh, base)
	if !used {
		t.Fatal("base should contribute")
	}
	if got.FiveHour.Utilization != 20 {
		t.Errorf("five_hour = %v, want fresh 20", got.FiveHour.Utilization)
	}
	if got.SevenDay.Utilization != 30 {
		t.Errorf("seven_day = %v, want base 30", got.SevenDay.Utilization)
	}
	if got.ScopedLimits["Fable"].Utilization != 40 || got.ScopedLimits["Opus"].Utilization != 50 {
		t.Errorf("scoped = %+v, want Fable 40 + Opus 50 from base", got.ScopedLimits)
	}
	if got.ExtraUsage != nil {
		t.Errorf("extra = %+v, want nil — ExtraUsage always comes from fresh", got.ExtraUsage)
	}
}

func TestOverlayScopedUnionFreshWinsPerName(t *testing.T) {
	fresh := &Snapshot{ScopedLimits: map[string]*Window{"Fable": owin(11)}}
	base := &Snapshot{ScopedLimits: map[string]*Window{"Fable": owin(99), "Opus": owin(50)}}
	got, used := Overlay(fresh, base)
	if !used {
		t.Fatal("base Opus should contribute")
	}
	if got.ScopedLimits["Fable"].Utilization != 11 {
		t.Errorf("Fable = %v, want fresh 11", got.ScopedLimits["Fable"].Utilization)
	}
	if got.ScopedLimits["Opus"].Utilization != 50 {
		t.Errorf("Opus = %v, want base 50", got.ScopedLimits["Opus"].Utilization)
	}
	if len(fresh.ScopedLimits) != 1 {
		t.Errorf("fresh map mutated: %+v", fresh.ScopedLimits)
	}
}

func TestOverlaySkipsNilBaseWindows(t *testing.T) {
	fresh := &Snapshot{FiveHour: owin(20)}
	base := &Snapshot{ScopedLimits: map[string]*Window{"Fable": nil}}
	got, used := Overlay(fresh, base)
	if used {
		t.Error("nil base windows should not count as contributing")
	}
	if got.ScopedLimits != nil {
		t.Errorf("scoped = %+v, want nil", got.ScopedLimits)
	}
}

func ptr(v float64) *float64 { return &v }
