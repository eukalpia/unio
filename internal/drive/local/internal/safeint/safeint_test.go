package safeint

import (
	"math"
	"testing"
)

func TestFromUint64(t *testing.T) {
	cases := []struct {
		in   uint64
		want int64
		ok   bool
	}{
		{0, 0, true},
		{1, 1, true},
		{math.MaxInt64, math.MaxInt64, true},
		{math.MaxInt64 + 1, 0, false},
		{math.MaxUint64, 0, false},
	}
	for _, tc := range cases {
		got, ok := FromUint64(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("FromUint64(%d) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestToUint64(t *testing.T) {
	cases := []struct {
		in   int64
		want uint64
		ok   bool
	}{
		{0, 0, true},
		{math.MaxInt64, math.MaxInt64, true},
		{-1, 0, false},
		{math.MinInt64, 0, false},
	}
	for _, tc := range cases {
		got, ok := ToUint64(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("ToUint64(%d) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
