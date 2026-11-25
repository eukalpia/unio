package uniwire

import (
	"context"
	"fmt"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
)

// Call invokes method on node to and decodes the reply into resp. The
// request's remaining deadline travels with it, so the callee stops work when
// the caller gives up. Cancelling ctx sends a cancel frame to the callee.
//
// Errors cross the wire as described in errors.go: an *s3err.Error keeps its
// code and message, and registered sentinels stay matchable with errors.Is.
// Unreachable peers yield an error wrapping ErrUnavailable.
func (t *Transport) Call(ctx context.Context, to ids.NodeID, method string, req, resp cluster.Message) error {
	if err := checkMethod(method); err != nil {
		return err
	}
	body, err := req.MarshalVT()
	if err != nil {
		return fmt.Errorf("uniwire: marshal %s: %w", method, err)
	}
	out, err := t.call(ctx, to, method, body)
	if err != nil {
		return err
	}
	if err := resp.UnmarshalVT(out); err != nil {
		return fmt.Errorf("uniwire: decode %s reply: %w", method, err)
	}
	return nil
}

// call acquires an RPC connection to to and performs one call on it.
func (t *Transport) call(ctx context.Context, to ids.NodeID, method string, body []byte) ([]byte, error) {
	if t.isClosed() {
		return nil, ErrClosed
	}
	if remainingNanos(ctx) < 0 {
		return nil, ctx.Err()
	}
	c, err := t.acquireRPC(ctx, t.peerFor(to))
	if err != nil {
		return nil, err
	}
	return c.call(ctx, method, body)
}
