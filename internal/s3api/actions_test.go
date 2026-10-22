package s3api

import (
	"testing"

	"github.com/eukalpia/unio/internal/s3api/router"
)

// TestRequiresPreAuthorization checks which operations the dispatcher
// authorizes before the handler runs. DeleteObjects names its keys in the body,
// so its handler authorizes each key and reports per-key denials. A request-level
// check against an empty key would deny a caller that may delete some keys.
func TestRequiresPreAuthorization(t *testing.T) {
	cases := []struct {
		op   router.Op
		want bool
	}{
		{router.OpDeleteObjects, false},
		{router.OpDeleteObject, true},
		{router.OpPutObject, true},
		{router.OpGetObject, true},
		{router.OpListObjectsV2, true},
		{router.OpCopyObject, true},
	}
	for _, c := range cases {
		if got := requiresPreAuthorization(c.op); got != c.want {
			t.Fatalf("requiresPreAuthorization(%v) = %v, want %v", c.op, got, c.want)
		}
	}
}
