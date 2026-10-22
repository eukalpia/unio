package recordpb

import (
	"math"
	"testing"
	"time"
)

func TestTimeToNano(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want int64
	}{
		{"zero time is 0", time.Time{}, 0},
		{"unix epoch is 0", time.Unix(0, 0), 0},
		{"one nanosecond after epoch", time.Unix(0, 1), 1},
		{"one nanosecond before epoch", time.Unix(0, -1), -1},
		{"sub-second precision", time.Unix(1700000000, 123456789), 1700000000123456789},
		{"non-UTC location keeps the instant", time.Date(2025, 1, 2, 3, 4, 5, 6, time.FixedZone("CET", 3600)), time.Date(2025, 1, 2, 2, 4, 5, 6, time.UTC).UnixNano()},
		{"largest representable instant", time.Unix(0, math.MaxInt64), math.MaxInt64},
		{"smallest representable instant", time.Unix(0, math.MinInt64), math.MinInt64},
		{"after the range saturates", time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC), math.MaxInt64},
		{"before the range saturates", time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), math.MinInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TimeToNano(tt.in); got != tt.want {
				t.Errorf("TimeToNano = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTimeFromNano(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want time.Time
	}{
		{"0 is the zero time", 0, time.Time{}},
		{"positive instant", 1700000000123456789, time.Unix(0, 1700000000123456789)},
		{"negative instant", -1, time.Unix(0, -1)},
		{"largest instant", math.MaxInt64, time.Unix(0, math.MaxInt64)},
		{"smallest instant", math.MinInt64, time.Unix(0, math.MinInt64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TimeFromNano(tt.in)
			if !got.Equal(tt.want) {
				t.Errorf("TimeFromNano(%d) = %v, want instant %v", tt.in, got, tt.want)
			}
			if got.Location() != time.UTC {
				t.Errorf("TimeFromNano(%d) location = %v, want UTC", tt.in, got.Location())
			}
		})
	}
}

// TestTimeNanoRoundTrip checks that every stored value decodes and
// re-encodes to itself.
func TestTimeNanoRoundTrip(t *testing.T) {
	for _, n := range []int64{0, 1, -1, 999999999, -999999999, 1700000000123456789, math.MaxInt64, math.MinInt64} {
		if got := TimeToNano(TimeFromNano(n)); got != n {
			t.Errorf("TimeToNano(TimeFromNano(%d)) = %d", n, got)
		}
	}
}

// TestZeroTimeAndEpochCollide documents the schema limitation in time.go:
// the zero time and the Unix epoch both encode as 0, so both decode to the
// zero time.
func TestZeroTimeAndEpochCollide(t *testing.T) {
	if TimeToNano(time.Unix(0, 0)) != TimeToNano(time.Time{}) {
		t.Fatal("epoch and zero time should share the encoding 0")
	}
	if got := TimeFromNano(TimeToNano(time.Unix(0, 0))); !got.IsZero() {
		t.Errorf("epoch decoded to %v, want the zero time", got)
	}
	if got := TimeFromNano(TimeToNano(time.Time{})); !got.IsZero() {
		t.Errorf("zero time decoded to %v, want the zero time", got)
	}
}

// TestTimeToNanoChecked checks the range detection the converters rely on.
func TestTimeToNanoChecked(t *testing.T) {
	tests := []struct {
		name   string
		in     time.Time
		want   int64
		wantOK bool
	}{
		{"zero time", time.Time{}, 0, true},
		{"epoch", time.Unix(0, 0), 0, true},
		{"largest instant", time.Unix(0, math.MaxInt64), math.MaxInt64, true},
		{"one nanosecond past the largest instant", time.Unix(0, math.MaxInt64).Add(time.Nanosecond), math.MaxInt64, false},
		{"one nanosecond before the smallest instant", time.Unix(0, math.MinInt64).Add(-time.Nanosecond), math.MinInt64, false},
		{"far future", time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC), math.MaxInt64, false},
		{"far past", time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), math.MinInt64, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := timeToNano(tt.in)
			if ok != tt.wantOK {
				t.Errorf("timeToNano ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("timeToNano = %d, want %d", got, tt.want)
			}
		})
	}
}
