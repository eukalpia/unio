package healer

import (
	"log/slog"
	"time"
)

// Defaults applied to zero-valued Options fields.
const (
	// DefaultWorkers is the number of heal goroutines for all sets together.
	DefaultWorkers = 4
	// DefaultTimeout bounds one heal.
	DefaultTimeout = 5 * time.Minute
)

// Options configures a Healer. The zero value is valid and selects defaults.
type Options struct {
	// Workers is the number of heal goroutines shared by all sets. Zero or a
	// negative value selects DefaultWorkers.
	Workers int
	// Timeout bounds one heal. Zero or a negative value selects DefaultTimeout.
	Timeout time.Duration
	// Logger receives heal failures and layout changes. Nil discards them.
	Logger *slog.Logger
}

// Stats reports the activity of a Healer.
type Stats struct {
	// Healed counts requests whose heal returned no error. That includes an
	// object that turned out to need no repair.
	Healed uint64
	// Failed counts requests whose heal returned an error.
	Failed uint64
	// Dropped counts damage reports that the sets' queues discarded because
	// they were full.
	Dropped uint64
	// Pending counts requests that are waiting in a queue, waiting for a
	// worker, or being healed.
	Pending int
}
