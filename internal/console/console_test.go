package console

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// testFiles is a small build of the console: an index page, a hashed bundle,
// an unhashed icon and a nested directory.
func testFiles() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                {Data: []byte("<!doctype html><title>index</title>")},
		"assets/index-Ab3dE_9k.js":  {Data: []byte("console.log('bundle')")},
		"assets/index-Ab3dE_9k.css": {Data: []byte("body{margin:0}")},
		"favicon.ico":               {Data: []byte("icon")},
		"nested/deeper/page.txt":    {Data: []byte("plain text")},
		"assets/logo.svg":           {Data: []byte("<svg></svg>")},
		"assets/unknown.bin":        {Data: []byte{0, 1, 2}},
		"assets/dir/inside.json":    {Data: []byte(`{"ok":true}`)},
	}
}

// newTestHandler returns a Handler over testFiles.
func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	h, err := NewFS(testFiles())
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	return h
}

// readAll returns the body of a response and closes it.
func readAll(t *testing.T, a Asset) string {
	t.Helper()
	if a.Body == nil {
		return ""
	}
	defer a.Body.Close()
	b, err := io.ReadAll(a.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// checkSecurityHeaders fails unless a carries the exact console headers.
func checkSecurityHeaders(t *testing.T, a Asset) {
	t.Helper()
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	}
	for k, v := range want {
		if got := a.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestOpenIndexAtRoot(t *testing.T) {
	h := newTestHandler(t)
	for _, p := range []string{"/unio/console", "/unio/console/"} {
		a := h.Open(http.MethodGet, p)
		if a.Status != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", p, a.Status)
		}
		checkSecurityHeaders(t, a)
		if got := a.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", p, got)
		}
		if got := a.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control %q, want no-cache", p, got)
		}
		if body := readAll(t, a); !strings.Contains(body, "<title>index</title>") {
			t.Errorf("%s: body %q is not the index page", p, body)
		}
		if a.Size != int64(len("<!doctype html><title>index</title>")) {
			t.Errorf("%s: Size %d", p, a.Size)
		}
	}
}

func TestOpenServesFilesWithCachePolicy(t *testing.T) {
	h := newTestHandler(t)
	tests := []struct {
		path, body, ctype, cache string
	}{
		{"/unio/console/assets/index-Ab3dE_9k.js", "console.log('bundle')", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/unio/console/assets/index-Ab3dE_9k.css", "body{margin:0}", "text/css; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/unio/console/favicon.ico", "icon", "image/x-icon", "no-cache"},
		{"/unio/console/nested/deeper/page.txt", "plain text", "text/plain; charset=utf-8", "no-cache"},
		{"/unio/console/assets/logo.svg", "<svg></svg>", "image/svg+xml", "public, max-age=31536000, immutable"},
		{"/unio/console/assets/unknown.bin", "\x00\x01\x02", "application/octet-stream", "public, max-age=31536000, immutable"},
		{"/unio/console/index.html", "<!doctype html><title>index</title>", "text/html; charset=utf-8", "no-cache"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			a := h.Open(http.MethodGet, tc.path)
			if a.Status != http.StatusOK {
				t.Fatalf("status %d, want 200", a.Status)
			}
			checkSecurityHeaders(t, a)
			if got := readAll(t, a); got != tc.body {
				t.Errorf("body %q, want %q", got, tc.body)
			}
			if got := a.Header.Get("Content-Type"); got != tc.ctype {
				t.Errorf("Content-Type %q, want %q", got, tc.ctype)
			}
			if got := a.Header.Get("Cache-Control"); got != tc.cache {
				t.Errorf("Cache-Control %q, want %q", got, tc.cache)
			}
		})
	}
}

func TestOpenFallsBackToIndexForUnknownPaths(t *testing.T) {
	h := newTestHandler(t)
	paths := []string{
		"/unio/console/buckets/photos",
		"/unio/console/users/alice/keys",
		"/unio/console/assets",         // a directory, not a file
		"/unio/console/assets/",        // directory with a trailing slash
		"/unio/console/missing.js",     // no such file
		"/unio/console/nested/deeper/", // directory
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			a := h.Open(http.MethodGet, p)
			if a.Status != http.StatusOK {
				t.Fatalf("status %d, want the index page with 200", a.Status)
			}
			checkSecurityHeaders(t, a)
			if got := readAll(t, a); !strings.Contains(got, "<title>index</title>") {
				t.Errorf("body %q is not the index page", got)
			}
			if got := a.Header.Get("Cache-Control"); got != "no-cache" {
				t.Errorf("Cache-Control %q, want no-cache", got)
			}
		})
	}
}

func TestOpenRefusesTraversal(t *testing.T) {
	h := newTestHandler(t)
	paths := []string{
		"/unio/console/../../etc/passwd",
		"/unio/console/../../../etc/passwd",
		"/unio/console/a/../../../etc/passwd",
		"/unio/console/%2e%2e/%2e%2e/etc/passwd",
		"/unio/console/%2E%2E%2F%2E%2E%2Fetc%2Fpasswd",
		"/unio/console/..%2f..%2fetc%2fpasswd",
		"/unio/console/..%2F..%2Fetc%2Fpasswd",
		"/unio/console/%252e%252e/%252e%252e/etc/passwd",
		"/unio/console/%252e%252e%252f",
		"/unio/console/..\\..\\etc\\passwd",
		"/unio/console/%5c..%5c..%5cetc",
		"/unio/console/%2e%2e",
		"/unio/console/..",
		"/unio/console/.%2e/",
		"/unio/console/assets/%2e%2e/%2e%2e/secret",
		"/unio/console/%00",
		"/unio/console/a%00b",
		"/unio/console/%0a",
		"/unio/console/%ZZ",
		"/unio/console/a//b",
		"/unio/console/a/./b",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			a := h.Open(http.MethodGet, p)
			if a.Status != http.StatusNotFound {
				t.Fatalf("status %d, want 404", a.Status)
			}
			if a.Body != nil {
				t.Fatal("a refused path must not have a body")
			}
			checkSecurityHeaders(t, a)
		})
	}
}

func TestOpenRefusesPathsOutsideThePrefix(t *testing.T) {
	h := newTestHandler(t)
	for _, p := range []string{
		"/unio/consoles",
		"/unio/consoleX/",
		"/unio/console.html",
		"/unio/metrics",
		"/bucket/unio/console/",
		"/",
		"",
	} {
		if a := h.Open(http.MethodGet, p); a.Status != http.StatusNotFound {
			t.Errorf("%q: status %d, want 404", p, a.Status)
		}
	}
}

func TestOpenRefusesOverlongPaths(t *testing.T) {
	h := newTestHandler(t)
	long := Prefix + "/" + strings.Repeat("a", maxPathLen)
	if a := h.Open(http.MethodGet, long); a.Status != http.StatusNotFound {
		t.Fatalf("status %d for an overlong path, want 404", a.Status)
	}
}

func TestOpenMethods(t *testing.T) {
	h := newTestHandler(t)

	head := h.Open(http.MethodHead, "/unio/console/assets/index-Ab3dE_9k.js")
	if head.Status != http.StatusOK {
		t.Fatalf("HEAD status %d, want 200", head.Status)
	}
	if head.Body != nil {
		t.Error("HEAD must not carry a body")
	}
	if head.Size != int64(len("console.log('bundle')")) {
		t.Errorf("HEAD Size %d", head.Size)
	}
	checkSecurityHeaders(t, head)

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		a := h.Open(m, "/unio/console/")
		if a.Status != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", m, a.Status)
		}
		if got := a.Header.Get("Allow"); got != "GET, HEAD" {
			t.Errorf("%s: Allow %q", m, got)
		}
		checkSecurityHeaders(t, a)
	}
}

func TestPlaceholderIsServedFromTheBinary(t *testing.T) {
	h, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := h.Open(http.MethodGet, "/unio/console/")
	if a.Status != http.StatusOK {
		t.Fatalf("status %d", a.Status)
	}
	body := readAll(t, a)
	if !strings.Contains(body, "Console not built") {
		t.Errorf("placeholder does not say the console is not built: %q", body)
	}
	// The placeholder must run under the console CSP: no inline script or
	// style.
	for _, banned := range []string{"<script", "<style", " style="} {
		if strings.Contains(body, banned) {
			t.Errorf("placeholder contains %q, which the CSP refuses", banned)
		}
	}
	checkSecurityHeaders(t, a)
}

func TestNewFSRejectsBadTrees(t *testing.T) {
	if _, err := NewFS(nil); err == nil {
		t.Error("nil file system accepted")
	}
	if _, err := NewFS(fstest.MapFS{"app.js": {Data: []byte("x")}}); err == nil {
		t.Error("tree without index.html accepted")
	}
	dirIndex := fstest.MapFS{"index.html/x": {Data: []byte("x")}}
	if _, err := NewFS(dirIndex); err == nil {
		t.Error("index.html that is a directory accepted")
	}
}

func TestContentTypeAndCacheControl(t *testing.T) {
	if got := contentType("unknown.zzz"); got != "application/octet-stream" {
		t.Errorf("unknown extension: %q", got)
	}
	if got := contentType("APP.JS"); got != "text/javascript; charset=utf-8" {
		t.Errorf("upper-case extension: %q", got)
	}
	if got := cacheControl("index.html"); got != "no-cache" {
		t.Errorf("index cache: %q", got)
	}
	if got := cacheControl("assets/x.js"); got != cacheImmutable {
		t.Errorf("hashed cache: %q", got)
	}
	if got := cacheControl("assetsX/x.js"); got != "no-cache" {
		t.Errorf("sibling of assets must not be immutable: %q", got)
	}
}
