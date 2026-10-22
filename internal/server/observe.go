package server

import (
	"time"

	"github.com/eukalpia/unio/internal/s3api"
)

// observers passes each request observation to every observer in turn. The
// S3 front end takes one observer, and the node has two: the Prometheus
// metrics and the statistics window of the admin API.
type observers []s3api.Observer

// ObserveHTTPRequest implements s3api.Observer.
func (o observers) ObserveHTTPRequest(op string, status int, d time.Duration, bytesIn, bytesOut int64) {
	for _, ob := range o {
		ob.ObserveHTTPRequest(op, status, d, bytesIn, bytesOut)
	}
}
