// Package pebbleopt builds the Pebble options shared by every database of the
// metadata plane, so that the logs and the state machines are opened the same
// way and log the same way.
package pebbleopt

import (
	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"
)

// Options returns the options of a metadata database that uses cache. The
// cache is owned by the caller and shared by all databases of a node.
// Informational Pebble messages are dropped; errors and fatal invariant
// violations go to Pebble's default logger.
func Options(cache *pebble.Cache) *pebble.Options {
	return &pebble.Options{
		Cache:  cache,
		FS:     vfs.Default,
		Logger: quietLogger{},
	}
}

// quietLogger is the pebble.Logger of every metadata database.
type quietLogger struct{}

// Infof implements pebble.Logger by dropping the message.
func (quietLogger) Infof(string, ...any) {}

// Errorf implements pebble.Logger.
func (quietLogger) Errorf(format string, args ...any) {
	pebble.DefaultLogger.Errorf(format, args...)
}

// Fatalf implements pebble.Logger. Pebble calls it on an invariant violation.
func (quietLogger) Fatalf(format string, args ...any) {
	pebble.DefaultLogger.Fatalf(format, args...)
}
