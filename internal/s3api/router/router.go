// Package router maps an incoming S3 HTTP request to one S3 operation, the
// bucket and the key it targets. It supports path-style and virtual-hosted
// style addressing, and dispatches on the method and the query subresources.
// It performs no authentication or authorization and touches no storage.
package router

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/eukalpia/unio/internal/s3err"
)

// Request is the transport-independent view of an S3 request that routing
// needs. Path and Query are passed still percent-encoded, exactly as sent.
type Request struct {
	Method string
	// Host is the Host header value, which may include a port.
	Host string
	// Path is the raw request path, beginning with "/".
	Path string
	// Query is the raw query string without the leading "?".
	Query string
	// CopySource reports whether the x-amz-copy-source header is present.
	CopySource bool
}

// Route is the outcome of a successful match.
type Route struct {
	Op Op
	// Bucket is empty for service-level operations.
	Bucket string
	// Key is the decoded object key, empty for bucket-level operations.
	Key string
	// Query is the parsed query string.
	Query url.Values
}

// Router routes requests for a fixed set of virtual-hosted domains. It is
// immutable after New and safe for concurrent use.
type Router struct {
	// domains are lower-case, without leading or trailing dots, longest first.
	domains []string
}

// New returns a Router. domains are the DNS suffixes under which buckets are
// addressed virtually, for example "s3.unio.example" so that
// "photos.s3.unio.example" addresses bucket "photos". An empty list selects
// path-style addressing only.
func New(domains []string) (*Router, error) {
	r := &Router{domains: make([]string, 0, len(domains))}
	for _, d := range domains {
		d = strings.ToLower(d)
		if err := validateDomain(d); err != nil {
			return nil, err
		}
		r.domains = append(r.domains, d)
	}
	sort.SliceStable(r.domains, func(i, j int) bool { return len(r.domains[i]) > len(r.domains[j]) })
	return r, nil
}

func validateDomain(d string) error {
	if d == "" || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return fmt.Errorf("router: invalid domain %q", d)
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return fmt.Errorf("router: invalid character in domain %q", d)
		}
	}
	return nil
}

// Match routes req. Client errors are returned as *s3err.Error:
// NotImplemented for valid S3 operations outside the v0.1 scope,
// MethodNotAllowed for methods an operation does not support, and
// InvalidArgument or InvalidRequest for malformed input.
func (r *Router) Match(req Request) (Route, error) {
	query, err := url.ParseQuery(req.Query)
	if err != nil {
		return Route{}, s3err.Errorf(s3err.InvalidRequest, "malformed query string")
	}
	bucket, key, err := r.splitTarget(req.Host, req.Path)
	if err != nil {
		return Route{}, err
	}
	op, err := dispatch(req.Method, bucket, key, query, req.CopySource)
	if err != nil {
		return Route{}, err
	}
	return Route{Op: op, Bucket: bucket, Key: key, Query: query}, nil
}

// splitTarget extracts and percent-decodes the bucket and key.
func (r *Router) splitTarget(host, rawPath string) (bucket, key string, err error) {
	if !strings.HasPrefix(rawPath, "/") {
		return "", "", s3err.Errorf(s3err.InvalidRequest, "request path must start with /")
	}
	var rawBucket, rawKey string
	if b, ok := r.bucketFromHost(host); ok {
		rawBucket, rawKey = b, rawPath[1:]
		// The bucket came from the Host header, which is already a plain name.
	} else {
		rest := rawPath[1:]
		if rest == "" {
			return "", "", nil
		}
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			rawBucket, rawKey = rest[:i], rest[i+1:]
		} else {
			rawBucket = rest
		}
		if rawBucket == "" {
			return "", "", s3err.Errorf(s3err.InvalidRequest, "request path has an empty bucket name")
		}
		if rawBucket, err = unescape(rawBucket); err != nil {
			return "", "", err
		}
	}
	if rawKey, err = unescape(rawKey); err != nil {
		return "", "", err
	}
	return rawBucket, rawKey, nil
}

// bucketFromHost returns the bucket named by a virtual-hosted Host header.
func (r *Router) bucketFromHost(host string) (string, bool) {
	h := strings.ToLower(stripPort(host))
	for _, d := range r.domains {
		if h == d {
			return "", false
		}
		if len(h) > len(d)+1 && strings.HasSuffix(h, d) && h[len(h)-len(d)-1] == '.' {
			return h[:len(h)-len(d)-1], true
		}
	}
	return "", false
}

// stripPort removes a trailing :port, handling bracketed IPv6 literals.
func stripPort(host string) string {
	if strings.HasPrefix(host, "[") {
		if i := strings.IndexByte(host, ']'); i >= 0 {
			return host[1:i]
		}
		return host
	}
	if strings.Count(host, ":") == 1 {
		return host[:strings.IndexByte(host, ':')]
	}
	return host
}

// unescape decodes one path segment. Plus signs are literal in paths.
func unescape(s string) (string, error) {
	if strings.IndexByte(s, '%') < 0 {
		return s, nil
	}
	v, err := url.PathUnescape(s)
	if err != nil {
		return "", s3err.Errorf(s3err.InvalidArgument, "invalid percent-encoding in request path")
	}
	return v, nil
}
