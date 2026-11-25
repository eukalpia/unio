package admin

import (
	"math"
	"sort"
	"sync"
	"time"
)

// WindowSeconds is the span of the request statistics: one minute.
const WindowSeconds = 60

// latencyBounds are the upper bounds of the latency histogram. They start at
// 100 microseconds, grow by a quarter each step, and end at two minutes. A
// quarter keeps a percentile within about 25% of the true value.
var latencyBounds = buildLatencyBounds()

// buildLatencyBounds returns the histogram bounds. It runs once, at start.
func buildLatencyBounds() []time.Duration {
	var b []time.Duration
	for d := 100 * time.Microsecond; d < 2*time.Minute; d = time.Duration(math.Ceil(float64(d) * 1.25)) {
		b = append(b, d)
	}
	return append(b, 2*time.Minute)
}

// slot holds the totals of one second of traffic.
type slot struct {
	second   int64
	requests uint64
	errors   uint64
	bytesIn  uint64
	bytesOut uint64
	// latency counts requests by bucket. The last bucket holds the requests
	// slower than the last bound.
	latency []uint64
}

// Recorder keeps the request statistics of the last WindowSeconds. It satisfies
// the S3 front end's Observer. Its memory is fixed: one slot per second, each
// with a histogram of fixed size. Health probes and metrics scrapes are not
// counted, because they are not traffic. It is safe for concurrent use.
type Recorder struct {
	clock func() time.Time
	mu    sync.Mutex
	slots [WindowSeconds]slot
}

// Summary is the total of the statistics window.
type Summary struct {
	// Requests counts every request, and Errors those answered with 5xx.
	Requests, Errors uint64
	// BytesIn and BytesOut count request and response body bytes.
	BytesIn, BytesOut uint64
	// P99 is the 99th percentile of the request latency, as the upper bound of
	// its histogram bucket. It is zero when there were no requests.
	P99 time.Duration
}

// NewRecorder returns an empty Recorder that reads the time from clock. A nil
// clock selects time.Now.
func NewRecorder(clock func() time.Time) *Recorder {
	if clock == nil {
		clock = time.Now
	}
	r := &Recorder{clock: clock}
	for i := range r.slots {
		r.slots[i].latency = make([]uint64, len(latencyBounds)+1)
	}
	return r
}

// ObserveHTTPRequest records one completed request. It runs on the request
// goroutine, so it only takes a short lock and adds to a few counters.
func (r *Recorder) ObserveHTTPRequest(op string, status int, d time.Duration, bytesIn, bytesOut int64) {
	if op == "Health" || op == "Metrics" {
		return
	}
	sec := r.clock().Unix()
	bucket := sort.Search(len(latencyBounds), func(i int) bool { return latencyBounds[i] >= d })

	r.mu.Lock()
	defer r.mu.Unlock()
	sl := &r.slots[slotIndex(sec)]
	if sl.second != sec {
		sl.reset(sec)
	}
	sl.requests++
	if status >= 500 {
		sl.errors++
	}
	if bytesIn > 0 {
		sl.bytesIn += uint64(bytesIn)
	}
	if bytesOut > 0 {
		sl.bytesOut += uint64(bytesOut)
	}
	sl.latency[bucket]++
}

// slotIndex returns the slot of a Unix second. A clock before 1970 gives a
// negative remainder, which is moved into range.
func slotIndex(sec int64) int {
	i := sec % WindowSeconds
	if i < 0 {
		i += WindowSeconds
	}
	return int(i)
}

// reset clears a slot and makes it hold second sec.
func (sl *slot) reset(sec int64) {
	sl.second = sec
	sl.requests, sl.errors, sl.bytesIn, sl.bytesOut = 0, 0, 0, 0
	clear(sl.latency)
}

// Summary totals the slots that fall inside the window ending at now.
func (r *Recorder) Summary(now time.Time) Summary {
	cur := now.Unix()
	hist := make([]uint64, len(latencyBounds)+1)
	var sum Summary

	r.mu.Lock()
	for i := range r.slots {
		sl := &r.slots[i]
		age := cur - sl.second
		if sl.second == 0 || age < 0 || age >= WindowSeconds {
			continue
		}
		sum.Requests += sl.requests
		sum.Errors += sl.errors
		sum.BytesIn += sl.bytesIn
		sum.BytesOut += sl.bytesOut
		for j, n := range sl.latency {
			hist[j] += n
		}
	}
	r.mu.Unlock()

	sum.P99 = percentile(hist, 0.99)
	return sum
}

// percentile returns the upper bound of the bucket that holds the q-quantile of
// the histogram. It returns zero for an empty histogram.
func percentile(hist []uint64, q float64) time.Duration {
	var total uint64
	for _, n := range hist {
		total += n
	}
	if total == 0 {
		return 0
	}
	rank := uint64(math.Ceil(q * float64(total)))
	if rank < 1 {
		rank = 1
	}
	var cum uint64
	for i, n := range hist {
		cum += n
		if cum >= rank {
			if i < len(latencyBounds) {
				return latencyBounds[i]
			}
			return latencyBounds[len(latencyBounds)-1]
		}
	}
	return latencyBounds[len(latencyBounds)-1]
}
