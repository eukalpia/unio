package object

import (
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/s3err"
)

func TestEvaluateRead(t *testing.T) {
	mod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	info := Info{ETag: "abc", ModTime: mod}
	cases := []struct {
		c    Conditions
		want s3err.Code
	}{
		{Conditions{}, ""},
		{Conditions{IfMatch: "abc"}, ""},
		{Conditions{IfMatch: "zzz"}, s3err.PreconditionFailed},
		{Conditions{IfNoneMatch: "abc"}, s3err.NotModified},
		{Conditions{IfNoneMatch: "zzz"}, ""},
		{Conditions{IfModifiedSince: mod}, s3err.NotModified},
		{Conditions{IfModifiedSince: mod.Add(-time.Hour)}, ""},
		{Conditions{IfUnmodifiedSince: mod.Add(-time.Hour)}, s3err.PreconditionFailed},
		// If-Match takes precedence over If-Unmodified-Since.
		{Conditions{IfMatch: "abc", IfUnmodifiedSince: mod.Add(-time.Hour)}, ""},
		// If-None-Match takes precedence over If-Modified-Since.
		{Conditions{IfNoneMatch: "zzz", IfModifiedSince: mod}, ""},
	}
	for i, c := range cases {
		if got := s3err.CodeOf(c.c.EvaluateRead(info)); got != c.want {
			t.Fatalf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

func TestEvaluateWrite(t *testing.T) {
	cases := []struct {
		c      Conditions
		exists bool
		want   s3err.Code
	}{
		{Conditions{}, false, ""},
		{Conditions{IfNoneMatch: "*"}, false, ""},
		{Conditions{IfNoneMatch: "*"}, true, s3err.PreconditionFailed},
		{Conditions{IfMatch: "abc"}, true, ""},
		{Conditions{IfMatch: "zzz"}, true, s3err.PreconditionFailed},
		{Conditions{IfMatch: "abc"}, false, s3err.NoSuchKey},
	}
	for i, c := range cases {
		if got := s3err.CodeOf(c.c.EvaluateWrite(c.exists, "abc")); got != c.want {
			t.Fatalf("case %d: got %q want %q", i, got, c.want)
		}
	}
}
