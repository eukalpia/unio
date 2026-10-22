package compact

import (
	"context"
	"sync"
	"time"
)

// Limiter is a token bucket that bounds the byte rate of background work. A
// nil *Limiter is unlimited.
type Limiter struct {
	mu     sync.Mutex
	rate   float64 // bytes per second
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

// NewLimiter returns a limiter that admits bytesPerSec on average with bursts
// of up to burst bytes. A non-positive rate returns nil, which is unlimited.
func NewLimiter(bytesPerSec, burst int64) *Limiter {
	if bytesPerSec <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = bytesPerSec
	}
	return &Limiter{
		rate:   float64(bytesPerSec),
		burst:  float64(burst),
		tokens: float64(burst),
		now:    time.Now,
	}
}

// Wait blocks until n bytes may proceed, or until ctx is done.
func (l *Limiter) Wait(ctx context.Context, n int64) error {
	if l == nil {
		return nil
	}
	d := l.reserve(n)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// reserve takes n bytes from the bucket and returns how long the caller must
// wait before proceeding. The bucket may go into debt, which later waits repay.
func (l *Limiter) reserve(n int64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.last.IsZero() {
		l.tokens += now.Sub(l.last).Seconds() * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
	}
	l.last = now
	l.tokens -= float64(n)
	if l.tokens >= 0 {
		return 0
	}
	return time.Duration(-l.tokens / l.rate * float64(time.Second))
}
