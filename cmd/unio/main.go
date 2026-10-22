// Command unio is the Unio node binary and its administration CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	ctx, stop := signalContext(context.Background())
	code := execute(ctx, os.Args[1:], app{
		stdout: os.Stdout,
		stderr: os.Stderr,
		env:    environment(os.Environ()),
	})
	stop()
	os.Exit(code)
}

// signalContext returns a context that is cancelled on SIGINT or SIGTERM, so
// that a running node can drain its requests and shut down cleanly.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// environment turns os.Environ-style "KEY=value" entries into a map. Entries
// without a name are skipped. Windows keeps special "=C:" entries, which are
// ignored here.
func environment(entries []string) map[string]string {
	env := make(map[string]string, len(entries))
	for _, e := range entries {
		key, value, ok := strings.Cut(e, "=")
		if !ok || key == "" {
			continue
		}
		env[key] = value
	}
	return env
}
