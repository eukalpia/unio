package cluster

import (
	"context"

	"github.com/eukalpia/unio/internal/ids"
)

// Message is a protobuf message with vtprotobuf fast paths.
type Message interface {
	MarshalVT() ([]byte, error)
	UnmarshalVT([]byte) error
}

// Handler serves one RPC method. from is the authenticated caller.
type Handler func(ctx context.Context, from ids.NodeID, req []byte) (resp []byte, err error)

// RPC is request/response messaging between nodes over UniWire.
//
// Errors cross the wire faithfully: an *s3err.Error keeps its code and
// message, and registered sentinel errors (drive.ErrShardNotFound,
// meta.ErrNotLeader, ...) remain matchable with errors.Is on the caller.
type RPC interface {
	// Call invokes method on node to and decodes the reply into resp.
	Call(ctx context.Context, to ids.NodeID, method string, req, resp Message) error
	// Register installs the handler for method. Method names are
	// "<module>.<Name>", for example "meta.Apply".
	Register(method string, h Handler) error
}
