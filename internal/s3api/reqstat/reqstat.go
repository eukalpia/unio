// Package reqstat records what one request did, for the Observer: its S3
// operation, the request body bytes read and the body bytes a streamed GET
// wrote. The observe middleware creates one record per request, and the S3
// front end and the handlers fill it in. Records are pooled, so an observed
// request does not allocate one.
package reqstat

import (
	"sync"

	"github.com/cloudwego/hertz/pkg/app"
)

// UnknownOp names a request that was rejected before it was routed.
const UnknownOp = "Unknown"

// key is the request-context key under which the record is stored. It is an
// unexported string constant, so no other package can collide with it.
const key = "unio.s3api.reqstat"

// Stats is what one request did. The goroutine serving the request is the only
// writer, and the observer reads the record only after the handler chain has
// returned. A nil *Stats is valid and ignores every update.
type Stats struct {
	// Op is the S3 API name of the request.
	Op string
	// BytesIn counts the request body bytes read from the connection.
	BytesIn int64
	// BytesStreamed counts the body bytes a streamed GET wrote. A streamed
	// response leaves the response body empty, so this is the only count.
	BytesStreamed int64
}

var pool = sync.Pool{New: func() any { return new(Stats) }}

// Acquire returns a cleared record, reusing a pooled one when possible. Pass it
// to Release once the request is done.
func Acquire() *Stats {
	st := pool.Get().(*Stats)
	*st = Stats{Op: UnknownOp}
	return st
}

// Release returns st to the pool. st must not be used afterwards.
func Release(st *Stats) {
	pool.Put(st)
}

// Set stores st on the request context.
func Set(c *app.RequestContext, st *Stats) {
	c.Set(key, st)
}

// Get returns the record stored by Set, or nil when the request has none.
func Get(c *app.RequestContext) *Stats {
	v, ok := c.Get(key)
	if !ok {
		return nil
	}
	st, _ := v.(*Stats)
	return st
}

// SetOp records the S3 API name of the request.
func (s *Stats) SetOp(op string) {
	if s != nil {
		s.Op = op
	}
}

// AddBytesIn adds n request body bytes read from the connection.
func (s *Stats) AddBytesIn(n int) {
	if s != nil && n > 0 {
		s.BytesIn += int64(n)
	}
}

// AddBytesStreamed adds n body bytes written by a streamed response.
func (s *Stats) AddBytesStreamed(n int64) {
	if s != nil && n > 0 {
		s.BytesStreamed += n
	}
}
