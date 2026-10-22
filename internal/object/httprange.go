package object

import "github.com/eukalpia/unio/internal/s3err"

// HTTPRange is a parsed byte range request. Exactly one form is used:
// Start/End (End < 0 means "to the end") or Suffix > 0 (the last Suffix
// bytes).
type HTTPRange struct {
	Start  int64
	End    int64
	Suffix int64
}

// Resolve converts r to an absolute [offset, offset+length) window for an
// object of the given size. It returns InvalidRange when the range does not
// overlap the object, as S3 does.
func (r HTTPRange) Resolve(size int64) (offset, length int64, err error) {
	switch {
	case r.Suffix > 0:
		if size == 0 {
			return 0, 0, s3err.Errorf(s3err.InvalidRange, "range not satisfiable")
		}
		if r.Suffix > size {
			return 0, size, nil
		}
		return size - r.Suffix, r.Suffix, nil
	case r.Start < 0 || (r.End >= 0 && r.End < r.Start):
		return 0, 0, s3err.Errorf(s3err.InvalidRange, "invalid range")
	case r.Start >= size:
		return 0, 0, s3err.Errorf(s3err.InvalidRange, "range not satisfiable")
	}
	end := r.End
	if end < 0 || end >= size {
		end = size - 1
	}
	return r.Start, end - r.Start + 1, nil
}
