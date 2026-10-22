package headers

import (
	"math"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestParseRange(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want *object.HTTPRange // nil: serve the whole object
		code s3err.Code        // expected error code, "" for success
	}{
		{name: "absent", in: "", want: nil},
		{name: "blank", in: "   ", want: nil},
		{name: "closed", in: "bytes=0-9", want: &object.HTTPRange{Start: 0, End: 9}},
		{name: "open ended", in: "bytes=90-", want: &object.HTTPRange{Start: 90, End: -1}},
		{name: "suffix", in: "bytes=-10", want: &object.HTTPRange{Suffix: 10}},
		{name: "outer spaces", in: "  bytes=1-2  ", want: &object.HTTPRange{Start: 1, End: 2}},
		{name: "spaces around set", in: "bytes= 1-2 ", want: &object.HTTPRange{Start: 1, End: 2}},
		{name: "empty trailing element", in: "bytes=1-2,", want: &object.HTTPRange{Start: 1, End: 2}},
		{name: "upper case unit", in: "BYTES=1-2", want: &object.HTTPRange{Start: 1, End: 2}},
		{name: "leading zeros", in: "bytes=007-0009", want: &object.HTTPRange{Start: 7, End: 9}},
		{name: "max int64 end", in: "bytes=0-9223372036854775807", want: &object.HTTPRange{Start: 0, End: math.MaxInt64}},
		{name: "other unit serves whole object", in: "items=0-5", want: nil},
		{name: "several ranges serve whole object", in: "bytes=0-1, 4-5", want: nil},
		{name: "several ranges with suffix", in: "bytes=0-1,-5", want: nil},
		{name: "several open ranges", in: "bytes=0-,5-", want: nil},
		{name: "64 ranges is the maximum", in: "bytes=" + repeatCSV("0-0", 64), want: nil},

		{name: "missing equals", in: "bytes0-1", code: s3err.InvalidRange},
		{name: "empty unit", in: "=0-1", code: s3err.InvalidRange},
		{name: "unit with space", in: "bytes =0-1", code: s3err.InvalidRange},
		{name: "unit with invalid character", in: "items with space=0-1", code: s3err.InvalidRange},
		{name: "empty set", in: "bytes=", code: s3err.InvalidRange},
		{name: "only commas", in: "bytes=,,", code: s3err.InvalidRange},
		{name: "bare dash", in: "bytes=-", code: s3err.InvalidRange},
		{name: "zero suffix", in: "bytes=-0", code: s3err.InvalidRange},
		{name: "last before first", in: "bytes=5-2", code: s3err.InvalidRange},
		{name: "signed start", in: "bytes=+1-2", code: s3err.InvalidRange},
		{name: "signed suffix", in: "bytes=-+1", code: s3err.InvalidRange},
		{name: "negative start", in: "bytes=-1-2", code: s3err.InvalidRange},
		{name: "double dash", in: "bytes=1--2", code: s3err.InvalidRange},
		{name: "letters", in: "bytes=a-b", code: s3err.InvalidRange},
		{name: "hexadecimal", in: "bytes=0x1-2", code: s3err.InvalidRange},
		{name: "space inside number", in: "bytes=1 2-3", code: s3err.InvalidRange},
		{name: "nul byte", in: "bytes=0-1\x00", code: s3err.InvalidRange},
		{name: "start overflows int64", in: "bytes=9223372036854775808-", code: s3err.InvalidRange},
		{name: "suffix overflows int64", in: "bytes=-99999999999999999999", code: s3err.InvalidRange},
		{name: "malformed part in set", in: "bytes=0-1,x", code: s3err.InvalidRange},
		{name: "inverted part in set", in: "bytes=5-2,0-1", code: s3err.InvalidRange},
		{name: "65 ranges exceed the limit", in: "bytes=" + repeatCSV("0-0", 65), code: s3err.InvalidRange},
		{name: "value longer than the limit", in: "bytes=" + strings.Repeat("1", 1100), code: s3err.InvalidRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRange(tc.in)
			if tc.code != "" {
				wantCode(t, err, tc.code)
				if got != nil {
					t.Fatalf("want nil range on error, got %+v", *got)
				}
				return
			}
			wantNoError(t, err)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("ParseRange(%q) mismatch (-want +got):\n%s", tc.in, diff)
			}
		})
	}
}

func TestParseCopySourceRange(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want *object.HTTPRange
		fail bool
	}{
		{name: "absent", in: "", want: nil},
		{name: "blank", in: "  ", want: nil},
		{name: "closed", in: "bytes=0-9", want: &object.HTTPRange{Start: 0, End: 9}},
		{name: "single byte", in: "bytes=5-5", want: &object.HTTPRange{Start: 5, End: 5}},
		{name: "upper case unit", in: "BYTES=1-2", want: &object.HTTPRange{Start: 1, End: 2}},
		{name: "outer spaces", in: "  bytes=1-2  ", want: &object.HTTPRange{Start: 1, End: 2}},
		{name: "last before first", in: "bytes=10-5", fail: true},
		{name: "suffix form", in: "bytes=-5", fail: true},
		{name: "open ended", in: "bytes=5-", fail: true},
		{name: "bare dash", in: "bytes=-", fail: true},
		{name: "several ranges", in: "bytes=0-1,2-3", fail: true},
		{name: "other unit", in: "items=0-1", fail: true},
		{name: "missing unit", in: "0-1", fail: true},
		{name: "letters", in: "bytes=a-b", fail: true},
		{name: "overflow", in: "bytes=0-9223372036854775808", fail: true},
		{name: "space inside set", in: "bytes= 0-1", fail: true},
		{name: "trailing junk", in: "bytes=0-1 x", fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCopySourceRange(tc.in)
			if tc.fail {
				wantCode(t, err, s3err.InvalidArgument)
				if got != nil {
					t.Fatalf("want nil range on error, got %+v", *got)
				}
				return
			}
			wantNoError(t, err)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("ParseCopySourceRange(%q) mismatch (-want +got):\n%s", tc.in, diff)
			}
		})
	}
}
