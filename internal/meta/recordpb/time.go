package recordpb

import (
	"fmt"
	"math"
	"time"
)

// Times are stored as int64 Unix nanoseconds. The zero time.Time is stored as
// 0, because time.Time.UnixNano is undefined for it. As a consequence, the
// instant time.Unix(0, 0) is also stored as 0 and decodes to the zero time.
// This is a known limitation of the existing schema, and TestZeroTimeAndEpochCollide
// pins it down.
//
// Times decode in UTC. A round trip keeps the instant, not the location or the
// monotonic clock reading.

// TimeToNano returns the stored form of t: t.UnixNano(), or 0 for the zero
// time. Times outside the int64 nanosecond range (before 1678 or after 2262)
// cannot be stored, and TimeToNano saturates them to math.MinInt64 or
// math.MaxInt64. The ToProto functions return an error for them instead.
func TimeToNano(t time.Time) int64 {
	n, _ := timeToNano(t)
	return n
}

// TimeFromNano returns the time stored as n, in UTC. 0 decodes to the zero
// time.Time, so the epoch does not survive a round trip (see above).
func TimeFromNano(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// timeToNano returns the stored form of t, and ok is false if t is outside
// the int64 nanosecond range. The result is then saturated.
func timeToNano(t time.Time) (n int64, ok bool) {
	if t.IsZero() {
		return 0, true
	}
	n = t.UnixNano()
	if !time.Unix(0, n).Equal(t) {
		// UnixNano wrapped around, so t is not representable. The wrapped
		// value has no useful sign, so decide the direction from t itself.
		if t.Before(time.Unix(0, 0)) {
			return math.MinInt64, false
		}
		return math.MaxInt64, false
	}
	return n, true
}

// encodeTime returns the stored form of t for the named field. Unlike
// TimeToNano it fails for times outside the representable range, so that a
// converter never writes a saturated time.
func encodeTime(field string, t time.Time) (int64, error) {
	n, ok := timeToNano(t)
	if !ok {
		return 0, fmt.Errorf("%s %v is outside the int64 nanosecond range (1678-2262)", field, t)
	}
	return n, nil
}
