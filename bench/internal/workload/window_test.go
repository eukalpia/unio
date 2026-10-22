package workload

import (
	"errors"
	"testing"
	"time"
)

func TestWindowVerdict(t *testing.T) {
	base := time.Unix(1000, 0)
	win := window{start: base.Add(time.Second), end: base.Add(3 * time.Second)}
	boom := errors.New("boom")

	tests := []struct {
		name      string
		start     time.Time
		end       time.Time
		err       error
		cancelled bool
		want      verdict
	}{
		{
			name:  "completed inside the window",
			start: base.Add(2 * time.Second), end: base.Add(2*time.Second + time.Millisecond),
			want: verdictSuccess,
		},
		{
			name:  "started at the window start",
			start: base.Add(time.Second), end: base.Add(time.Second + time.Millisecond),
			want: verdictSuccess,
		},
		{
			name:  "finished exactly at the window end",
			start: base.Add(2 * time.Second), end: base.Add(3 * time.Second),
			want: verdictSuccess,
		},
		{
			name:  "started during warmup is discarded",
			start: base.Add(500 * time.Millisecond), end: base.Add(2 * time.Second),
			want: verdictDiscard,
		},
		{
			name:  "started during warmup and failed is still discarded",
			start: base.Add(500 * time.Millisecond), end: base.Add(2 * time.Second),
			err: boom, want: verdictDiscard,
		},
		{
			name:  "finished after the window end is discarded",
			start: base.Add(2 * time.Second), end: base.Add(3*time.Second + time.Nanosecond),
			want: verdictDiscard,
		},
		{
			name:  "real failure inside the window is counted",
			start: base.Add(2 * time.Second), end: base.Add(2*time.Second + time.Millisecond),
			err: boom, want: verdictFailure,
		},
		{
			name:  "failure caused by cancellation is discarded",
			start: base.Add(2 * time.Second), end: base.Add(2*time.Second + time.Millisecond),
			err: boom, cancelled: true, want: verdictDiscard,
		},
		{
			name:  "success while the context is cancelled is kept",
			start: base.Add(2 * time.Second), end: base.Add(2*time.Second + time.Millisecond),
			cancelled: true, want: verdictSuccess,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := win.verdict(tc.start, tc.end, tc.err, tc.cancelled); got != tc.want {
				t.Fatalf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWindowElapsed(t *testing.T) {
	base := time.Unix(1000, 0)
	win := window{start: base.Add(time.Second), end: base.Add(3 * time.Second)}
	tests := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{name: "before the window starts", now: base, want: 0},
		{name: "inside the window", now: base.Add(2 * time.Second), want: time.Second},
		{name: "after the window closes is capped", now: base.Add(10 * time.Second), want: 2 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := win.elapsed(tc.now); got != tc.want {
				t.Fatalf("elapsed = %v, want %v", got, tc.want)
			}
		})
	}
}
