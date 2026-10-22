package obs

import (
	"net/http"
	"net/http/pprof"
)

// pprofPrefix is the public path prefix of the profiling endpoints.
const pprofPrefix = "/unio/debug/pprof/"

// PprofHandler serves the net/http/pprof endpoints under /unio/debug/pprof/,
// behind BearerAuth. Index, Cmdline, Profile, Symbol and Trace are mounted,
// and named profiles such as heap are reached through Index.
//
// The caller mounts the handler only when pprof is enabled, which is off by
// default. Importing net/http/pprof registers its routes on
// http.DefaultServeMux as a side effect, so nothing should serve that mux.
func PprofHandler(token []byte) (http.Handler, error) {
	// pprof.Index resolves named profiles from a hard-coded "/debug/pprof/"
	// prefix, so the public "/unio" prefix is stripped before dispatch.
	inner := http.NewServeMux()
	inner.HandleFunc("/debug/pprof/", pprof.Index)
	inner.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	inner.HandleFunc("/debug/pprof/profile", pprof.Profile)
	inner.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	inner.HandleFunc("/debug/pprof/trace", pprof.Trace)

	root := http.NewServeMux()
	root.Handle(pprofPrefix, http.StripPrefix("/unio", inner))
	return BearerAuth(token, root)
}
