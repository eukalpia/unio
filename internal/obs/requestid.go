package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// RequestIDHeader is the S3 response header that carries the request id.
const RequestIDHeader = "X-Amz-Request-Id"

// requestIDKey is the unexported context key for the request id.
type requestIDKey struct{}

// WithRequestID returns a copy of ctx that carries id. A nil ctx is treated
// as context.Background.
func WithRequestID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFrom returns the request id stored in ctx, or "" when absent.
func RequestIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// NewRequestID returns 16 random bytes from crypto/rand, hex encoded (32
// characters).
func NewRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("obs: request id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
