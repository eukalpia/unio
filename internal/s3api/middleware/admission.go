package middleware

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync/atomic"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/s3api/apierr"
	"github.com/eukalpia/unio/internal/s3err"
)

// errOverloaded reports that the wait queue is full.
var errOverloaded = errors.New("middleware: admission queue full")

// Limiter bounds the number of requests in flight and the number waiting for
// a slot. Requests beyond both bounds are refused at once, so that queues
// cannot grow without limit. A Limiter is safe for concurrent use.
type Limiter struct {
	slots    chan struct{}
	queued   atomic.Int64
	maxQueue int64
}

// NewLimiter returns a limiter admitting maxActive concurrent requests with
// up to maxQueued more waiting. Both bounds must be at least 1 and 0
// respectively; maxActive must be positive.
func NewLimiter(maxActive, maxQueued int) (*Limiter, error) {
	if maxActive < 1 || maxQueued < 0 {
		return nil, errors.New("middleware: concurrency limit must be positive and queue size non-negative")
	}
	return &Limiter{slots: make(chan struct{}, maxActive), maxQueue: int64(maxQueued)}, nil
}

// acquire takes a slot, waiting while the queue has room. It returns
// errOverloaded when the queue is full, or ctx.Err() when the caller gives up.
func (l *Limiter) acquire(ctx context.Context) error {
	select {
	case l.slots <- struct{}{}:
		return nil
	default:
	}
	if l.queued.Add(1) > l.maxQueue {
		l.queued.Add(-1)
		return errOverloaded
	}
	defer l.queued.Add(-1)
	select {
	case l.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release frees the slot taken by acquire.
func (l *Limiter) release() {
	<-l.slots
}

// Admission applies l to every request. A refused request receives 503
// SlowDown with a Retry-After hint; a request that gives up while waiting
// receives the error for its context.
func Admission(l *Limiter, logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if err := l.acquire(ctx); err != nil {
			if errors.Is(err, errOverloaded) {
				c.Response.Header.Set("Retry-After", strconv.Itoa(1))
				apierr.Write(c, logger, s3err.Errorf(s3err.SlowDown, "Please reduce your request rate."), "")
			} else {
				apierr.Write(c, logger, s3err.Wrap(s3err.RequestTimeout, err), "")
			}
			c.Abort()
			return
		}
		defer l.release()
		c.Next(ctx)
	}
}
