package headers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

// wantCode fails the test unless err carries the S3 error code want.
func wantCode(t *testing.T, err error, want s3err.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s, got nil error", want)
	}
	if got := s3err.CodeOf(err); got != want {
		t.Fatalf("want %s, got %s (%v)", want, got, err)
	}
}

// wantNoError fails the test if err is not nil.
func wantNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// copyHeaders rewrites canonical request header names such as "If-Match" to
// their x-amz-copy-source variant, keeping the values.
func copyHeaders(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		out["X-Amz-Copy-Source-"+k] = v
	}
	return out
}

// repeatCSV returns n copies of unit joined by commas.
func repeatCSV(unit string, n int) string {
	return strings.TrimSuffix(strings.Repeat(unit+",", n), ",")
}
