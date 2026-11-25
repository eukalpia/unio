package console

import (
	"net/http"
	"path"
	"strings"
)

// contentSecurityPolicy is the Content-Security-Policy of every console
// response. The console has no inline scripts or style attributes, so it
// needs only same-origin resources.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; " +
	"base-uri 'none'; form-action 'self'"

// Cache policies of the asset kinds.
const (
	cacheNoCache   = "no-cache"
	cacheImmutable = "public, max-age=31536000, immutable"
)

// hashedDir is the directory of the build output whose files carry a content
// hash in their names. Files there never change, so they are cached forever.
const hashedDir = "assets/"

// contentTypes maps the extensions the console build produces to their media
// types. The table keeps the values fixed, whatever the host's MIME database.
var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".map":   "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".txt":   "text/plain; charset=utf-8",
}

// securityHeaders returns the headers every console response carries.
func securityHeaders() http.Header {
	h := make(http.Header, 6)
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	return h
}

// contentType returns the media type for the file name. An unknown extension
// is sent as application/octet-stream, which nosniff keeps from being run.
func contentType(name string) string {
	if t, ok := contentTypes[strings.ToLower(path.Ext(name))]; ok {
		return t
	}
	return "application/octet-stream"
}

// cacheControl returns the cache policy for the file name: hashed bundles are
// immutable, and everything else, index.html included, is revalidated.
func cacheControl(name string) string {
	if strings.HasPrefix(name, hashedDir) {
		return cacheImmutable
	}
	return cacheNoCache
}
