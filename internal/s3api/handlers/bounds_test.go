package handlers

import (
	"math"
	"strconv"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol"

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

// TestSetContentLength checks that object lengths, which the layer reports as
// int64, reach the Content-Length header exactly, including sizes above 4 GiB,
// and that a negative length is an internal error.
func TestSetContentLength(t *testing.T) {
	for _, n := range []int64{0, 1, 10, math.MaxInt32 + 1, 5 << 40} {
		var h protocol.ResponseHeader
		if err := setContentLength(&h, n); err != nil {
			t.Fatalf("setContentLength(%d): %v", n, err)
		}
		if got := string(h.Peek("Content-Length")); got != strconv.FormatInt(n, 10) {
			t.Fatalf("setContentLength(%d) wrote %q", n, got)
		}
	}
	var h protocol.ResponseHeader
	if err := setContentLength(&h, -1); !s3err.Is(err, s3err.InternalError) {
		t.Fatalf("setContentLength(-1) = %v, want InternalError", err)
	}
}
