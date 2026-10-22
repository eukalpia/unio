package handlers

import (
	"fmt"
	"math"
	"strconv"

	"github.com/eukalpia/unio/internal/s3err"
)

// boundedInt parses s as a decimal number in [lo, hi]. Only ASCII digits are
// accepted, so that no sign, space, radix prefix or fraction passes through the
// looser syntax of strconv. The value is parsed at 32 bits, which covers every
// bound the protocol defines: a larger value is out of range, never wrapped.
func boundedInt(s string, lo, hi int) (int, error) {
	bad := s3err.Errorf(s3err.InvalidArgument, "the value must be an integer between %d and %d", lo, hi)
	if s == "" {
		return 0, bad
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, bad
		}
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil || n < int64(lo) || n > int64(hi) {
		return 0, bad
	}
	return int(n), nil
}

// intLength converts a byte count, as the layer reports it, to the int that the
// header setters take. A negative count, or one that does not fit in int, is an
// internal error: the layer broke its contract, and a wrapped length would
// corrupt the response.
func intLength(n int64) (int, error) {
	if n < 0 || n > int64(math.MaxInt) {
		return 0, s3err.Wrap(s3err.InternalError, fmt.Errorf("length %d does not fit a response header", n))
	}
	return int(n), nil
}
