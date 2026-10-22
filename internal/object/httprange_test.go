package object

import (
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

func TestHTTPRangeResolve(t *testing.T) {
	cases := []struct {
		r           HTTPRange
		size        int64
		off, length int64
		wantErr     bool
	}{
		{HTTPRange{Start: 0, End: 9}, 100, 0, 10, false},
		{HTTPRange{Start: 90, End: -1}, 100, 90, 10, false},
		{HTTPRange{Start: 90, End: 1000}, 100, 90, 10, false},
		{HTTPRange{Suffix: 10}, 100, 90, 10, false},
		{HTTPRange{Suffix: 500}, 100, 0, 100, false},
		{HTTPRange{Start: 100, End: -1}, 100, 0, 0, true},
		{HTTPRange{Start: 5, End: 2}, 100, 0, 0, true},
		{HTTPRange{Suffix: 1}, 0, 0, 0, true},
	}
	for _, c := range cases {
		off, n, err := c.r.Resolve(c.size)
		if (err != nil) != c.wantErr {
			t.Fatalf("%+v size %d: err = %v, wantErr %v", c.r, c.size, err, c.wantErr)
		}
		if err != nil {
			if !s3err.Is(err, s3err.InvalidRange) {
				t.Fatalf("want InvalidRange, got %v", err)
			}
			continue
		}
		if off != c.off || n != c.length {
			t.Fatalf("%+v size %d: got (%d,%d) want (%d,%d)", c.r, c.size, off, n, c.off, c.length)
		}
	}
}
