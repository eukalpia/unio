// Package console serves the web console's static files from the node binary.
//
// The console is a single-page application built from web/console. Its build
// output is copied into assets/ by make console, and this package embeds that
// directory. Hashed bundles live under assets/ and are cached immutably; every
// other file, index.html among them, is served with no-cache.
//
// Every response carries the console security headers: a Content-Security-Policy
// that allows only same-origin scripts, styles, connections and fonts, no
// framing, no referrer and nosniff.
//
// Open resolves one request. A path that names a file serves that file. Any
// other path under the prefix serves index.html, so that the single-page app
// can route it. A path that could name something outside the asset tree gets
// 404 and never the fallback.
package console
