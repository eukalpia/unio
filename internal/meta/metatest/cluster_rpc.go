package metatest

import (
	"context"
	"fmt"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
)

// nodeRPC is the cluster.RPC of one node: calls made through it come from
// self, and Register installs handlers on self.
type nodeRPC struct {
	c    *Cluster
	self ids.NodeID
}

var _ cluster.RPC = (*nodeRPC)(nil)

// RPC returns the cluster.RPC of node. Calls made through it carry node as the
// sender, and Register installs handlers on node.
func (c *Cluster) RPC(node ids.NodeID) cluster.RPC {
	return &nodeRPC{c: c, self: node}
}

// Register installs h as the handler of method on the node. A later Register
// for the same method replaces the earlier handler and returns nil, so a
// restarted node can register its methods again. A nil handler, or an unknown
// node, is an error.
func (r *nodeRPC) Register(method string, h cluster.Handler) error {
	if h == nil {
		return fmt.Errorf("metatest: nil handler for %s", method)
	}
	r.c.mu.Lock()
	defer r.c.mu.Unlock()
	nd, err := r.c.lookupLocked(r.self)
	if err != nil {
		return err
	}
	nd.handlers[method] = h
	return nil
}

// Call marshals req with MarshalVT, runs the handler of method on node to
// with the bytes, and unmarshals the reply into resp with UnmarshalVT, so the
// wire encoding is exercised. An error returned by the handler is returned
// unchanged, so errors.Is and s3err codes still match on the caller.
//
// Calls fail with a plain error, which s3err classifies as an internal
// error, when the caller or the target is down, when the target is unknown,
// or when no handler is registered for method. A cancelled context fails the
// call before it is sent.
func (r *nodeRPC) Call(ctx context.Context, to ids.NodeID, method string, req, resp cluster.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var body []byte
	if req != nil {
		b, err := req.MarshalVT()
		if err != nil {
			return fmt.Errorf("metatest: marshal %s request: %w", method, err)
		}
		body = b
	}
	h, err := r.c.handlerFor(r.self, to, method)
	if err != nil {
		return err
	}
	reply, err := h(ctx, r.self, body)
	if err != nil {
		return err
	}
	if resp == nil {
		return nil
	}
	if err := resp.UnmarshalVT(reply); err != nil {
		return fmt.Errorf("metatest: unmarshal %s reply: %w", method, err)
	}
	return nil
}

// handlerFor returns the handler of method on to, for a call from from. It
// fails when either node is down or unknown, or when no handler exists.
func (c *Cluster) handlerFor(from, to ids.NodeID, method string) (cluster.Handler, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	src, err := c.lookupLocked(from)
	if err != nil {
		return nil, err
	}
	if !src.up {
		return nil, fmt.Errorf("metatest: caller %s is down", from)
	}
	dst, err := c.lookupLocked(to)
	if err != nil {
		return nil, err
	}
	if !dst.up {
		return nil, fmt.Errorf("metatest: node %s is down", to)
	}
	h, ok := dst.handlers[method]
	if !ok {
		return nil, fmt.Errorf("metatest: no handler for %s on %s", method, to)
	}
	return h, nil
}
