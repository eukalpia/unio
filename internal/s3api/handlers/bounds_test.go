package handlers

import (
	"math"
	"strconv"
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

// TestBoundedInt checks the parser of numeric query parameters: digits only,
// parsed at 32 bits, and checked against the closed range before conversion.
func TestBoundedInt(t *testing.T) {
	cases := []struct {
		in     string
		lo, hi int
		want   int
		fail   bool
	}{
		{in: "1", lo: 1, hi: 10000, want: 1},
		{in: "10000", lo: 1, hi: 10000, want: 10000},
		{in: "0", lo: 0, hi: 1000, want: 0},
		{in: "0", lo: 1, hi: 10000, fail: true},
		{in: "10001", lo: 1, hi: 10000, fail: true},
		{in: "+1", lo: 1, hi: 10000, fail: true},
		{in: "-1", lo: 0, hi: 10000, fail: true},
		{in: " 1", lo: 1, hi: 10000, fail: true},
		{in: "1 ", lo: 1, hi: 10000, fail: true},
		{in: "0x10", lo: 1, hi: 10000, fail: true},
		{in: "", lo: 1, hi: 10000, fail: true},
		{in: "4294967296", lo: 0, hi: math.MaxInt32, fail: true},
		{in: "2147483647", lo: 0, hi: math.MaxInt32, want: math.MaxInt32},
		{in: "99999999999999999999999", lo: 0, hi: 10000, fail: true},
	}
	for _, c := range cases {
		got, err := boundedInt(c.in, c.lo, c.hi)
		if c.fail {
			if !s3err.Is(err, s3err.InvalidArgument) {
				t.Fatalf("boundedInt(%q, %d, %d) = %d, %v; want InvalidArgument", c.in, c.lo, c.hi, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("boundedInt(%q, %d, %d) = %d, %v; want %d", c.in, c.lo, c.hi, got, err, c.want)
		}
	}
}

// TestIntLength checks the conversion of an object length, which the layer
// reports as int64, to the int taken by the header setters. A negative length,
// or one that does not fit in int, must be an error and never a wrapped value.
func TestIntLength(t *testing.T) {
	for _, n := range []int64{0, 1, 10, 5 << 40} {
		got, err := intLength(n)
		if err != nil || int64(got) != n {
			t.Fatalf("intLength(%d) = %d, %v", n, got, err)
		}
	}
	if _, err := intLength(-1); err == nil {
		t.Fatal("intLength(-1) accepted a negative length")
	}
	if strconv.IntSize == 32 {
		if _, err := intLength(math.MaxInt32 + 1); err == nil {
			t.Fatal("intLength accepted a length that does not fit in a 32-bit int")
		}
	}
}
