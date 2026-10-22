package xmlenc

import (
	"testing"
	"time"
)

// testTime is the fixed timestamp used by the expected-output tables.
var testTime = time.Date(2026, 1, 2, 3, 4, 5, 600000000, time.UTC)

const (
	wantDecl = `<?xml version="1.0" encoding="UTF-8"?>`
	wantNS   = `xmlns="http://s3.amazonaws.com/doc/2006-03-01/"`
)

// checkAppend runs fn with a nil destination and with a non-empty one. The
// nil case must produce want exactly, and the non-empty case must produce the
// same bytes after the untouched prefix.
func checkAppend(t *testing.T, want string, fn func(dst []byte) []byte) {
	t.Helper()
	if got := string(fn(nil)); got != want {
		t.Errorf("output mismatch\n got: %s\nwant: %s", got, want)
	}
	const prefix = "keep:"
	if got := string(fn([]byte(prefix))); got != prefix+want {
		t.Errorf("append to non-empty dst\n got: %s\nwant: %s", got, prefix+want)
	}
}
