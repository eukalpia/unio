package uniwire

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

// TestSplitRequestDeadlines feeds the decoder the edges of the peer-supplied
// deadline. A value above the largest int64 must clamp, never wrap negative.
func TestSplitRequestDeadlines(t *testing.T) {
	cases := []struct {
		name string
		raw  uint64
		want int64
	}{
		{name: "no deadline", raw: 0, want: 0},
		{name: "one nanosecond", raw: 1, want: 1},
		{name: "largest int64", raw: math.MaxInt64, want: math.MaxInt64},
		{name: "first value past int64", raw: math.MaxInt64 + 1, want: math.MaxInt64},
		{name: "top bit set", raw: 1 << 63, want: math.MaxInt64},
		{name: "uint64 maximum", raw: math.MaxUint64, want: math.MaxInt64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := binary.BigEndian.AppendUint64(nil, tc.raw)
			payload = append(payload, 'b', 'o', 'd', 'y')
			timeout, body, err := splitRequest(payload)
			if err != nil {
				t.Fatal(err)
			}
			if timeout != tc.want {
				t.Fatalf("deadline = %d, want %d", timeout, tc.want)
			}
			if string(body) != "body" {
				t.Fatalf("body = %q", body)
			}
			if timeout < 0 {
				t.Fatal("deadline wrapped negative")
			}
		})
	}
}

// TestSplitRequestNeedsPrefix refuses a payload too short to hold the deadline.
func TestSplitRequestNeedsPrefix(t *testing.T) {
	if _, _, err := splitRequest(make([]byte, 7)); !errors.Is(err, errMalformedFrame) {
		t.Fatalf("seven-byte payload = %v, want errMalformedFrame", err)
	}
}
