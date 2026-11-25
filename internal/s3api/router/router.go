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
	if _, virtual := r.bucketFromHost(req.Host); !virtual && isReservedPath(req.Path) && !isReservedBucketCreate(req.Method, req.Path) {
		op, err := serviceOp(req.Method, req.Path)
		if err != nil {
			return Route{}, err
		}
		return Route{Op: op, Query: query}, nil
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

// reservedPrefix is the namespace reserved for service endpoints and future
// administrative APIs. Path-style requests under it are never bucket lookups:
// bucket "unio" cannot be created, so the namespace cannot collide with data.
const reservedPrefix = "/unio"

// isReservedPath reports whether rawPath is inside the reserved namespace.
func isReservedPath(rawPath string) bool {
	return rawPath == reservedPrefix || strings.HasPrefix(rawPath, reservedPrefix+"/")
}

// adminPrefix and consolePrefix are the reserved paths of the admin API and
// of the web console. They are recognised only for path-style and bare-domain
// requests, because a virtual-hosted request is always an object request.
const (
	adminPrefix   = reservedPrefix + "/admin/v1"
	consolePrefix = reservedPrefix + "/console"
)

// isAdminPath reports whether rawPath is the admin API or inside it.
func isAdminPath(rawPath string) bool {
	return rawPath == adminPrefix || strings.HasPrefix(rawPath, adminPrefix+"/")
}

// isConsolePath reports whether rawPath is the console or inside it.
func isConsolePath(rawPath string) bool {
	return rawPath == consolePrefix || strings.HasPrefix(rawPath, consolePrefix+"/")
}

// serviceOp routes a request in the reserved namespace. Health and metrics
// endpoints accept GET and HEAD (metrics only GET); anything else in the
// namespace is reported as NoSuchKey, so that probes cannot tell it apart
// from a missing object.
func serviceOp(method, rawPath string) (Op, error) {
	// The admin API and the console take every method and route it themselves,
	// so that they can answer 405 with their own error format.
	if isAdminPath(rawPath) {
		return OpAdminAPI, nil
	}
	if isConsolePath(rawPath) {
		return OpConsole, nil
	}
	switch rawPath {
	case "/unio/health/live":
		return serviceMethod(method, OpHealthLive, true)
	case "/unio/health/ready":
		return serviceMethod(method, OpHealthReady, true)
	case "/unio/metrics":
		return serviceMethod(method, OpMetrics, false)
	}
	return OpUnknown, s3err.Errorf(s3err.NoSuchKey, "the specified key does not exist")
}

// serviceMethod checks the method of a service endpoint.
func serviceMethod(method string, op Op, allowHead bool) (Op, error) {
	if method == "GET" || (allowHead && method == "HEAD") {
		return op, nil
	}
	return OpUnknown, methodNotAllowed()
}

// isReservedBucketCreate reports whether a request creates the bucket named
// like the reserved namespace. Such a request is routed as an ordinary
// CreateBucket, and the handler rejects the name with InvalidBucketName.
func isReservedBucketCreate(method, rawPath string) bool {
	return method == "PUT" && (rawPath == reservedPrefix || rawPath == reservedPrefix+"/")
}
