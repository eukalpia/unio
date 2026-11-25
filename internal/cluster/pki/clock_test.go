package pki

import (
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)

func TestManualClockFiresInDeadlineOrder(t *testing.T) {
	clk := NewManualClock(t0)
	var order []int
	for _, d := range []int{3, 1, 2} {
		clk.AfterFunc(time.Duration(d)*time.Hour, func() { order = append(order, d) })
	}
	clk.Advance(5 * time.Hour)
	if want := []int{1, 2, 3}; !reflect.DeepEqual(order, want) {
		t.Fatalf("fired %v, want %v", order, want)
	}
	if got := clk.Now(); !got.Equal(t0.Add(5 * time.Hour)) {
		t.Fatalf("now = %v, want %v", got, t0.Add(5*time.Hour))
	}
}

func TestManualClockStopPreventsFiring(t *testing.T) {
	clk := NewManualClock(t0)
	fired := false
	tm := clk.AfterFunc(time.Hour, func() { fired = true })
	if !tm.Stop() {
		t.Fatal("Stop reported a pending timer as already run")
	}
	if tm.Stop() {
		t.Fatal("second Stop reported success")
	}
	clk.Advance(2 * time.Hour)
	if fired {
		t.Fatal("stopped timer fired")
	}
}

func TestManualClockFiredTimerCannotBeStopped(t *testing.T) {
	clk := NewManualClock(t0)
	tm := clk.AfterFunc(time.Hour, func() {})
	clk.Advance(time.Hour)
	if tm.Stop() {
		t.Fatal("Stop succeeded on a timer that already fired")
	}
}

func TestManualClockTimerCanScheduleAnotherDueTimer(t *testing.T) {
	clk := NewManualClock(t0)
	var at []time.Time
	clk.AfterFunc(time.Hour, func() {
		at = append(at, clk.Now())
		clk.AfterFunc(time.Hour, func() { at = append(at, clk.Now()) })
	})
	clk.Advance(3 * time.Hour)
	want := []time.Time{t0.Add(time.Hour), t0.Add(2 * time.Hour)}
	if len(at) != 2 || !at[0].Equal(want[0]) || !at[1].Equal(want[1]) {
		t.Fatalf("timers saw clock at %v, want %v", at, want)
	}
}

func TestManualClockCallbackSeesItsDeadline(t *testing.T) {
	clk := NewManualClock(t0)
	var seen time.Time
	clk.AfterFunc(90*time.Minute, func() { seen = clk.Now() })
	clk.Advance(5 * time.Hour)
	if want := t0.Add(90 * time.Minute); !seen.Equal(want) {
		t.Fatalf("callback saw %v, want its deadline %v", seen, want)
	}
	if got := clk.Now(); !got.Equal(t0.Add(5 * time.Hour)) {
		t.Fatalf("clock finished at %v", got)
	}
}
