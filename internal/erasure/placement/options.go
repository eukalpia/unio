package placement

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/erasure/rs"
)

// Defaults applied to zero-valued Options fields.
const (
	// DefaultFullRatio is the used fraction of a set at which it stops taking
	// new objects.
	DefaultFullRatio = 0.90
	// DefaultRefreshInterval is how often Run refreshes the capacity of the
	// sets.
	DefaultRefreshInterval = 10 * time.Second
)

// Options configures a Registry. The zero value is valid and selects defaults.
type Options struct {
	// Budget bounds the buffer memory of the registry. Every set shares it.
	// Nil selects one budget of rs.DefaultBudget bytes for the registry, not
	// one per set.
	Budget *bufpool.Budget
	// StripeSize is the data bytes per stripe of new writes. Zero selects the
	// rs default.
	StripeSize int
	// StallTimeout is how long a write waits for one drive. Zero selects the
	// rs default.
	StallTimeout time.Duration
	// HealQueueSize is the capacity of each set's heal queue. Zero selects the
	// rs default.
	HealQueueSize int
	// FullRatio is the used fraction at or above which a set takes no new
	// objects. Zero selects DefaultFullRatio. Otherwise it must be in (0, 1].
	FullRatio float64
	// RefreshInterval is how often Run refreshes capacity. Zero selects
	// DefaultRefreshInterval.
	RefreshInterval time.Duration
	// Logger receives warnings about drives that could not be opened. Nil
	// discards them.
	Logger *slog.Logger
}

// withDefaults returns o with defaults applied, or an error for values that
// cannot be defaulted.
func (o Options) withDefaults() (Options, error) {
	if o.FullRatio == 0 {
		o.FullRatio = DefaultFullRatio
	}
	// The negated comparison also rejects NaN.
	if !(o.FullRatio > 0 && o.FullRatio <= 1) {
		return o, fmt.Errorf("placement: full ratio %v must be in (0, 1]", o.FullRatio)
	}
	if o.RefreshInterval == 0 {
		o.RefreshInterval = DefaultRefreshInterval
	}
	if o.RefreshInterval < 0 {
		return o, fmt.Errorf("placement: refresh interval %v must be positive", o.RefreshInterval)
	}
	if o.Budget == nil {
		o.Budget = bufpool.NewBudget(rs.DefaultBudget)
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return o, nil
}
