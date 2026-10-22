// Package reqinfo carries the per-request identifiers that every response
// and log line of the S3 front end reports: the request id and the host id.
// The middleware stores them on the request context, and error rendering
// and access logging read them back.
package reqinfo

import "github.com/cloudwego/hertz/pkg/app"

// key is the request-context key under which Info is stored. It is an
// unexported string constant, so no other package can collide with it.
const key = "unio.s3api.reqinfo"

// Info identifies one request.
type Info struct {
	// RequestID is the value of x-amz-request-id.
	RequestID string
	// HostID is the value of x-amz-id-2, identifying the serving node.
	HostID string
}

// Set stores info on the request context.
func Set(c *app.RequestContext, info Info) {
	c.Set(key, info)
}

// Get returns the identifiers stored by Set, or the zero Info when the
// request did not pass through the middleware.
func Get(c *app.RequestContext) Info {
	v, ok := c.Get(key)
	if !ok {
		return Info{}
	}
	info, _ := v.(Info)
	return info
}
