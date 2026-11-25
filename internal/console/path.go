package console

import (
	"io/fs"
	"net/url"
	"strings"
)

// maxPathLen bounds the raw request path. Longer paths are refused.
const maxPathLen = 4096

// assetName maps the path of a console request to a name in the asset tree.
// The empty name stands for the index page. ok is false when the request must
// get 404 and not the fallback: the path is outside the prefix or malformed,
// or its decoded form could name a location outside the tree.
func assetName(rawPath string) (name string, ok bool) {
	if len(rawPath) > maxPathLen {
		return "", false
	}
	rest, found := strings.CutPrefix(rawPath, Prefix)
	if !found {
		return "", false
	}
	switch {
	case rest == "" || rest == "/":
		return "", true
	case rest[0] != '/':
		// A sibling such as /unio/consoles is not the console.
		return "", false
	}
	decoded, err := url.PathUnescape(rest)
	if err != nil {
		return "", false
	}
	if !plainPath(decoded) {
		return "", false
	}
	name = strings.TrimSuffix(strings.TrimPrefix(decoded, "/"), "/")
	if name == "" || name == "." {
		// "." is the root of the tree, which is the index page.
		return "", true
	}
	// fs.ValidPath refuses "." and ".." elements, empty elements and
	// rooted paths, so a name that passes cannot leave the tree.
	if !fs.ValidPath(name) {
		return "", false
	}
	return name, true
}

// plainPath reports whether s holds only bytes that a file name in the asset
// tree can contain. A backslash, a percent sign (a second level of escaping)
// and control characters are refused.
func plainPath(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f || c == '\\' || c == '%' {
			return false
		}
	}
	return true
}
