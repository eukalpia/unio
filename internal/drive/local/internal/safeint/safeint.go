// Package safeint converts between the unsigned integers stored on disk and the
// signed sizes and offsets used in memory. Each conversion checks its range
// against a constant before it converts, so a value from a corrupt file can
// never wrap into a negative offset or a huge length.
package safeint

import "math"

// FromUint64 converts a value read from disk to int64. It reports false when
// the value is above math.MaxInt64, which would become negative.
func FromUint64(u uint64) (int64, bool) {
	if u > math.MaxInt64 {
		return 0, false
	}
	return int64(u), true
}

// ToUint64 converts a size or offset to its on-disk form. It reports false for
// a negative value, which has no unsigned form.
func ToUint64(v int64) (uint64, bool) {
	if v < 0 {
		return 0, false
	}
	return uint64(v), true
}
