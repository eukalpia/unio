package s3api

import "github.com/eukalpia/unio/internal/s3api/router"

// observedName returns the operation name that the Observer receives for op.
// Both health probes report as "Health", so that probe traffic is one series.
// Every other operation keeps the S3 API name of the router.
func observedName(op router.Op) string {
	switch op {
	case router.OpHealthLive, router.OpHealthReady:
		return "Health"
	}
	return op.String()
}
