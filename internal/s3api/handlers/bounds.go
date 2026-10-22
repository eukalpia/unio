package handlers

import (
	"fmt"
	"strconv"

	"github.com/cloudwego/hertz/pkg/protocol"

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

// setContentLength writes a byte count, as the layer reports it, to the
// Content-Length response header without narrowing it to int, so objects of
// any size up to the protocol limit are described exactly. A negative count is
// an internal error: the layer broke its contract.
func setContentLength(h *protocol.ResponseHeader, n int64) error {
	if n < 0 {
		return s3err.Wrap(s3err.InternalError, fmt.Errorf("negative length %d", n))
	}
	h.SetContentLengthBytes(strconv.AppendInt(nil, n, 10))
	return nil
}
