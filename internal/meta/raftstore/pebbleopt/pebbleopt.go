// Package pebbleopt builds the Pebble options shared by every database of the
// metadata plane, so that the logs and the state machines are opened the same
// way and log the same way.
package pebbleopt

import (
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"
)

// Tuning for the metadata workload. Inline objects (up to 64 KiB) are values
// in the Raft log and in the state machines, and with Pebble's defaults every
// compaction rewrote them level after level: a 4 MiB memtable flushed every
// few dozen 64 KiB writes, L0 filled up, and writes stalled behind
// compactions. On one CPU that turned into a p95 of 40-50 ms and falling
// throughput under steady small-object load.
const (
	// memTableSize lets a memtable absorb hundreds of inline writes per flush.
	// Pebble grows each memtable up to this size, so idle partitions stay small.
	memTableSize = 16 << 20
	// l0StopWrites leaves room for L0 to absorb a burst before writes stall.
	l0StopWrites = 24
	// blobMinimumSize sends values of 1 KiB and more to blob files, so that
	// compactions move references instead of rewriting the values.
	blobMinimumSize = 1 << 10
)

// Options returns the options of a metadata database that uses cache. The
// cache is owned by the caller and shared by all databases of a node.
// Informational Pebble messages are dropped; errors and fatal invariant
// violations go to Pebble's default logger.
func Options(cache *pebble.Cache) *pebble.Options {
	opts := &pebble.Options{
		Cache:                 cache,
		FS:                    vfs.Default,
		Logger:                quietLogger{},
		MemTableSize:          memTableSize,
		L0StopWritesThreshold: l0StopWrites,
		// Value separation needs this format. Opening an older database
		// ratchets it once; the same Pebble release reads both.
		FormatMajorVersion: pebble.FormatValueSeparation,
	}
	opts.Experimental.ValueSeparationPolicy = func() pebble.ValueSeparationPolicy {
		return pebble.ValueSeparationPolicy{
			Enabled:               true,
			MinimumSize:           blobMinimumSize,
			MaxBlobReferenceDepth: 10,
			RewriteMinimumAge:     5 * time.Minute,
			TargetGarbageRatio:    0.2,
		}
	}
	return opts
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
