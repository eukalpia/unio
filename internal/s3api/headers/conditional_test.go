package headers

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

var conditionDate = time.Date(1994, time.November, 6, 8, 49, 37, 0, time.UTC)

type conditionCase struct {
	name string
	h    http.Header
	want object.Conditions
	code s3err.Code
}

func conditionCases() []conditionCase {
	return []conditionCase{
		{name: "none", h: http.Header{}, want: object.Conditions{}},
		{name: "if-match", h: http.Header{"If-Match": {`"abc"`}}, want: object.Conditions{IfMatch: "abc"}},
		{name: "if-match wildcard", h: http.Header{"If-Match": {"*"}}, want: object.Conditions{IfMatch: "*"}},
		{name: "if-match weak marker", h: http.Header{"If-Match": {`W/"abc"`}}, want: object.Conditions{IfMatch: "abc"}},
		{name: "if-match unquoted", h: http.Header{"If-Match": {"abc"}}, want: object.Conditions{IfMatch: "abc"}},
		{name: "if-none-match", h: http.Header{"If-None-Match": {`"d41d8cd9"`}}, want: object.Conditions{IfNoneMatch: "d41d8cd9"}},
		{name: "if-none-match wildcard", h: http.Header{"If-None-Match": {"*"}}, want: object.Conditions{IfNoneMatch: "*"}},
		{
			name: "if-modified-since imf-fixdate",
			h:    http.Header{"If-Modified-Since": {"Sun, 06 Nov 1994 08:49:37 GMT"}},
			want: object.Conditions{IfModifiedSince: conditionDate},
		},
		{
			name: "if-modified-since rfc850",
			h:    http.Header{"If-Modified-Since": {"Sunday, 06-Nov-94 08:49:37 GMT"}},
			want: object.Conditions{IfModifiedSince: conditionDate},
		},
		{
			name: "if-modified-since asctime",
			h:    http.Header{"If-Modified-Since": {"Sun Nov  6 08:49:37 1994"}},
			want: object.Conditions{IfModifiedSince: conditionDate},
		},
		{
			name: "if-unmodified-since",
			h:    http.Header{"If-Unmodified-Since": {"Sun, 06 Nov 1994 08:49:37 GMT"}},
			want: object.Conditions{IfUnmodifiedSince: conditionDate},
		},
		{name: "invalid if-modified-since is ignored", h: http.Header{"If-Modified-Since": {"yesterday"}}},
		{name: "invalid if-unmodified-since is ignored", h: http.Header{"If-Unmodified-Since": {"2026-10-22"}}},
		{
			name: "repeated date lines are ignored",
			h:    http.Header{"If-Modified-Since": {"Sun, 06 Nov 1994 08:49:37 GMT", "Sun, 06 Nov 1994 08:49:37 GMT"}},
		},
		{name: "list of etags", h: http.Header{"If-Match": {`"a", "b"`}}, code: s3err.InvalidArgument},
		{name: "list with wildcard", h: http.Header{"If-None-Match": {`"a", *`}}, code: s3err.InvalidArgument},
		{name: "repeated etag lines", h: http.Header{"If-Match": {`"a"`, `"b"`}}, code: s3err.InvalidArgument},
		{name: "empty quoted etag", h: http.Header{"If-Match": {`""`}}, code: s3err.InvalidArgument},
		{name: "blank etag", h: http.Header{"If-None-Match": {""}}, code: s3err.InvalidArgument},
		{name: "etag with space", h: http.Header{"If-Match": {`"a b"`}}, code: s3err.InvalidArgument},
		{name: "etag with embedded quote", h: http.Header{"If-Match": {`"a"b"`}}, code: s3err.InvalidArgument},
		{name: "etag with control byte", h: http.Header{"If-Match": {"\"a\x01\""}}, code: s3err.InvalidArgument},
	}
}

func TestParseConditions(t *testing.T) {
	for _, tc := range conditionCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseConditions(tc.h)
			checkConditions(t, got, err, tc)
		})
	}
}

func TestParseCopyConditions(t *testing.T) {
	for _, tc := range conditionCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCopyConditions(copyHeaders(tc.h))
			checkConditions(t, got, err, tc)
		})
	}
}

func TestConditionsIgnoreTheOtherFamily(t *testing.T) {
	h := http.Header{"If-Match": {`"abc"`}, "X-Amz-Copy-Source-If-None-Match": {"*"}}
	got, err := ParseConditions(h)
	wantNoError(t, err)
	if diff := cmp.Diff(object.Conditions{IfMatch: "abc"}, got); diff != "" {
		t.Fatalf("ParseConditions mismatch (-want +got):\n%s", diff)
	}
	got, err = ParseCopyConditions(h)
	wantNoError(t, err)
	if diff := cmp.Diff(object.Conditions{IfNoneMatch: "*"}, got); diff != "" {
		t.Fatalf("ParseCopyConditions mismatch (-want +got):\n%s", diff)
	}
}

func checkConditions(t *testing.T, got object.Conditions, err error, tc conditionCase) {
	t.Helper()
	if tc.code != "" {
		wantCode(t, err, tc.code)
		return
	}
	wantNoError(t, err)
	if diff := cmp.Diff(tc.want, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
	for _, ts := range []time.Time{got.IfModifiedSince, got.IfUnmodifiedSince} {
		if !ts.IsZero() && ts.Location() != time.UTC {
			t.Fatalf("time %v is not UTC", ts)
		}
	}
}
