package admin

import (
	"testing"
	"time"
)

func TestRecorderTotalsOverTheWindow(t *testing.T) {
	clock := &fakeClock{now: testNow}
	r := NewRecorder(clock.Now)
	r.ObserveHTTPRequest("GetObject", 200, 10*time.Millisecond, 0, 100)
	r.ObserveHTTPRequest("PutObject", 200, 20*time.Millisecond, 50, 0)
	r.ObserveHTTPRequest("ListObjects", 503, 30*time.Millisecond, 0, 0)
	r.ObserveHTTPRequest("GetObject", 404, 40*time.Millisecond, -1, -1)

	s := r.Summary(testNow)
	if s.Requests != 4 || s.Errors != 1 {
		t.Errorf("requests=%d errors=%d, want 4 and 1", s.Requests, s.Errors)
	}
	if s.BytesIn != 50 || s.BytesOut != 100 {
		t.Errorf("bytesIn=%d bytesOut=%d, want 50 and 100 (negative counts ignored)", s.BytesIn, s.BytesOut)
	}
}

func TestRecorderIgnoresProbesAndScrapes(t *testing.T) {
	clock := &fakeClock{now: testNow}
	r := NewRecorder(clock.Now)
	r.ObserveHTTPRequest("Health", 200, time.Millisecond, 0, 2)
	r.ObserveHTTPRequest("Metrics", 200, time.Millisecond, 0, 2)
	if s := r.Summary(testNow); s.Requests != 0 {
		t.Errorf("probes counted: requests=%d", s.Requests)
	}
}

func TestRecorderSlotsExpireAfterTheWindow(t *testing.T) {
	clock := &fakeClock{now: testNow}
	r := NewRecorder(clock.Now)
	r.ObserveHTTPRequest("GetObject", 200, time.Millisecond, 0, 0)
	clock.Advance((WindowSeconds - 1) * time.Second)
	if s := r.Summary(clock.Now()); s.Requests != 1 {
		t.Errorf("a request 59 s old is gone: requests=%d", s.Requests)
	}
	clock.Advance(time.Second)
	if s := r.Summary(clock.Now()); s.Requests != 0 {
		t.Errorf("a request 60 s old is still counted: requests=%d", s.Requests)
	}
}

func TestRecorderSlotReuseClearsOldSeconds(t *testing.T) {
	clock := &fakeClock{now: testNow}
	r := NewRecorder(clock.Now)
	r.ObserveHTTPRequest("GetObject", 500, time.Millisecond, 7, 7)
	// The same slot index, one full window later.
	clock.Advance(WindowSeconds * time.Second)
	r.ObserveHTTPRequest("GetObject", 200, time.Millisecond, 0, 0)
	s := r.Summary(clock.Now())
	if s.Requests != 1 || s.Errors != 0 || s.BytesIn != 0 || s.BytesOut != 0 {
		t.Errorf("stale counters survived slot reuse: %+v", s)
	}
}

func TestRecorderP99(t *testing.T) {
	clock := &fakeClock{now: testNow}
	r := NewRecorder(clock.Now)
	if s := r.Summary(testNow); s.P99 != 0 {
		t.Errorf("empty window p99 = %v, want 0", s.P99)
	}
	// 99 fast requests and one slow one: the 99th percentile is still fast.
	for i := 0; i < 99; i++ {
		r.ObserveHTTPRequest("GetObject", 200, time.Millisecond, 0, 0)
	}
	r.ObserveHTTPRequest("GetObject", 200, 5*time.Second, 0, 0)
	if s := r.Summary(testNow); s.P99 < time.Millisecond || s.P99 > 1300*time.Microsecond {
		t.Errorf("p99 = %v, want about 1ms", s.P99)
	}
	// Two slow requests out of a hundred: the 99th percentile is the slow one.
	r.ObserveHTTPRequest("GetObject", 200, 5*time.Second, 0, 0)
	if s := r.Summary(testNow); s.P99 < 4*time.Second {
		t.Errorf("p99 = %v, want about 5s", s.P99)
	}
}

func TestRecorderSlowerThanEveryBound(t *testing.T) {
	clock := &fakeClock{now: testNow}
	r := NewRecorder(clock.Now)
	r.ObserveHTTPRequest("GetObject", 200, 10*time.Hour, 0, 0)
	if s := r.Summary(testNow); s.P99 != latencyBounds[len(latencyBounds)-1] {
		t.Errorf("overflow p99 = %v, want the last bound %v", s.P99, latencyBounds[len(latencyBounds)-1])
	}
}

func TestSlotIndexStaysInRange(t *testing.T) {
	cases := map[int64]int{0: 0, 59: 59, 60: 0, 61: 1, -1: 59, -60: 0, -61: 59}
	for sec, want := range cases {
		if got := slotIndex(sec); got != want {
			t.Errorf("slotIndex(%d) = %d, want %d", sec, got, want)
		}
	}
}

func TestLatencyBoundsAreIncreasing(t *testing.T) {
	for i := 1; i < len(latencyBounds); i++ {
		if latencyBounds[i] <= latencyBounds[i-1] {
			t.Fatalf("bound %d (%v) does not exceed bound %d (%v)", i, latencyBounds[i], i-1, latencyBounds[i-1])
		}
	}
	if latencyBounds[0] != 100*time.Microsecond || latencyBounds[len(latencyBounds)-1] != 2*time.Minute {
		t.Errorf("bounds run from %v to %v", latencyBounds[0], latencyBounds[len(latencyBounds)-1])
	}
}
