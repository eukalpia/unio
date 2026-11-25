package console

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
)

// embedded holds the build output of the console. The all: prefix keeps files
// whose names start with a dot or an underscore.
//
//go:embed all:assets
var embedded embed.FS

// Prefix is the URL path the console is served under, without a trailing
// slash.
const Prefix = "/unio/console"

// indexName is the page served for the console root and for every path that
// names no file.
const indexName = "index.html"

// Asset is the response to one console request.
type Asset struct {
	// Status is 200, 404 or 405.
	Status int
	// Header holds the response headers, the security headers included.
	Header http.Header
	// Body is the content of a 200 response to GET. It is nil for HEAD and
	// for every other status. When it is set, the caller must Close it.
	Body io.ReadCloser
	// Size is the length of the content, or 0 when there is none.
	Size int64
}

// Handler serves the console assets. It is immutable after construction and
// safe for concurrent use.
type Handler struct {
	files fs.FS
}

// New returns a Handler over the assets embedded in the binary. It fails when
// the embedded tree has no index.html.
func New() (*Handler, error) {
	sub, err := fs.Sub(embedded, "assets")
	if err != nil {
		return nil, fmt.Errorf("console: embedded assets: %w", err)
	}
	return NewFS(sub)
}

// NewFS returns a Handler over files, whose root holds the console build. It
// fails when files has no index.html.
func NewFS(files fs.FS) (*Handler, error) {
	if files == nil {
		return nil, errors.New("console: nil file system")
	}
	info, err := fs.Stat(files, indexName)
	if err != nil {
		return nil, fmt.Errorf("console: %s: %w", indexName, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("console: %s is a directory", indexName)
	}
	return &Handler{files: files}, nil
}

// Open resolves one request. rawPath is the request path as sent, beginning
// with Prefix. Only GET and HEAD are served; any other method gets 405.
//
// A path that names a file gets that file. Any other path under the prefix
// gets the index page with status 200, so that the single-page app can route
// it. A path that could name something outside the asset tree gets 404.
func (h *Handler) Open(method, rawPath string) Asset {
	if method != http.MethodGet && method != http.MethodHead {
		a := plain(http.StatusMethodNotAllowed)
		a.Header.Set("Allow", "GET, HEAD")
		return a
	}
	name, ok := assetName(rawPath)
	if !ok {
		return plain(http.StatusNotFound)
	}
	if name == "" {
		name = indexName
	}
	if a, found := h.open(name, method); found {
		return a
	}
	if a, found := h.open(indexName, method); found {
		return a
	}
	return plain(http.StatusNotFound)
}

// open returns the 200 response for the file name, or false when the name is
// not a regular file of the tree.
func (h *Handler) open(name, method string) (Asset, bool) {
	f, err := h.files.Open(name)
	if err != nil {
		return Asset{}, false
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		_ = f.Close()
		return Asset{}, false
	}
	a := Asset{Status: http.StatusOK, Header: securityHeaders(), Size: info.Size()}
	a.Header.Set("Content-Type", contentType(name))
	a.Header.Set("Cache-Control", cacheControl(name))
	if method == http.MethodHead {
		_ = f.Close()
	} else {
		a.Body = f
	}
	return a, true
}

// plain returns a response with the given status and no body. It carries the
// security headers and does not let a cache keep the answer.
func plain(status int) Asset {
	a := Asset{Status: status, Header: securityHeaders()}
	a.Header.Set("Cache-Control", cacheNoCache)
	return a
}
