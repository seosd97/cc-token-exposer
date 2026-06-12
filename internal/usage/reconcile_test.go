package usage

import (
	"testing"
	"time"
)

func TestReconcile(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	future := now.Add(2 * time.Hour) // window not yet reset
	past := now.Add(-1 * time.Hour)  // window already elapsed

	win := func(util float64, reset time.Time) *Window {
		return &Window{Utilization: util, ResetsAt: reset}
	}

	t.Run("next nil returns nil", func(t *testing.T) {
		if got := Reconcile(&Snapshot{}, nil, now); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})

	t.Run("prev nil returns next", func(t *testing.T) {
		next := &Snapshot{FiveHour: win(10, future)}
		if got := Reconcile(nil, next, now); got != next {
			t.Fatalf("got %+v, want next unchanged", got)
		}
	})

	t.Run("suspect drop within same window keeps prev", func(t *testing.T) {
		prev := &Snapshot{FiveHour: win(80, future)}
		next := &Snapshot{FiveHour: win(45, future)} // 35pt drop, > threshold
		got := Reconcile(prev, next, now)
		if got.FiveHour.Utilization != 80 {
			t.Errorf("utilization = %v, want retained 80", got.FiveHour.Utilization)
		}
		if !got.FiveHour.Suspect {
			t.Errorf("expected Suspect=true")
		}
		// Inputs must not be mutated.
		if next.FiveHour.Suspect || next.FiveHour.Utilization != 45 {
			t.Errorf("next mutated: %+v", next.FiveHour)
		}
	})

	t.Run("small drop is accepted", func(t *testing.T) {
		prev := &Snapshot{FiveHour: win(80, future)}
		next := &Snapshot{FiveHour: win(60, future)} // 20pt drop, < threshold
		got := Reconcile(prev, next, now)
		if got.FiveHour.Utilization != 60 || got.FiveHour.Suspect {
			t.Errorf("got %+v, want 60 non-suspect", got.FiveHour)
		}
	})

	t.Run("exactly threshold is suspect", func(t *testing.T) {
		prev := &Snapshot{FiveHour: win(80, future)}
		next := &Snapshot{FiveHour: win(50, future)} // exactly 30pt
		got := Reconcile(prev, next, now)
		if got.FiveHour.Utilization != 80 || !got.FiveHour.Suspect {
			t.Errorf("got %+v, want retained 80 suspect", got.FiveHour)
		}
	})

	t.Run("window reset allows legitimate drop", func(t *testing.T) {
		prev := &Snapshot{FiveHour: win(80, past)}
		next := &Snapshot{FiveHour: win(5, future)} // boundary advanced
		got := Reconcile(prev, next, now)
		if got.FiveHour.Utilization != 5 || got.FiveHour.Suspect {
			t.Errorf("got %+v, want fresh 5 non-suspect", got.FiveHour)
		}
	})

	t.Run("increase is accepted", func(t *testing.T) {
		prev := &Snapshot{FiveHour: win(40, future)}
		next := &Snapshot{FiveHour: win(55, future)}
		got := Reconcile(prev, next, now)
		if got.FiveHour.Utilization != 55 || got.FiveHour.Suspect {
			t.Errorf("got %+v, want 55 non-suspect", got.FiveHour)
		}
	})

	t.Run("nil prev window passes next through", func(t *testing.T) {
		prev := &Snapshot{}
		next := &Snapshot{SevenDay: win(70, future)}
		got := Reconcile(prev, next, now)
		if got.SevenDay.Utilization != 70 || got.SevenDay.Suspect {
			t.Errorf("got %+v, want 70 non-suspect", got.SevenDay)
		}
	})

	t.Run("per-window independence", func(t *testing.T) {
		prev := &Snapshot{
			FiveHour: win(90, future),
			SevenDay: win(50, future),
		}
		next := &Snapshot{
			FiveHour: win(10, future), // suspect drop
			SevenDay: win(55, future), // normal increase
		}
		got := Reconcile(prev, next, now)
		if got.FiveHour.Utilization != 90 || !got.FiveHour.Suspect {
			t.Errorf("five_hour = %+v, want retained 90 suspect", got.FiveHour)
		}
		if got.SevenDay.Utilization != 55 || got.SevenDay.Suspect {
			t.Errorf("seven_day = %+v, want 55 non-suspect", got.SevenDay)
		}
	})
}
