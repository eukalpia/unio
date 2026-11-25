package uniwire

import (
	"context"
	"errors"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/meta"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// Errors produced by the transport itself.
var (
	// ErrUnavailable reports that a peer cannot be reached or stopped
	// answering: a dial, handshake, read or write failed, or the peer's
	// identity did not match the node that was dialed. Callers should treat
	// the node as unreachable and retry elsewhere.
	ErrUnavailable = s3err.NewSentinel("uniwire: peer unavailable", s3err.ServiceUnavailable)
	// ErrClosed is returned by calls made after the transport was closed.
	ErrClosed = s3err.NewSentinel("uniwire: transport closed", s3err.ServiceUnavailable)
	// ErrUnknownMethod is returned by a peer that has no handler for a method.
	ErrUnknownMethod = s3err.NewSentinel("uniwire: unknown method", s3err.NotImplemented)
)

// namedSentinel pairs a sentinel with its wire name. Names are part of the
// protocol: they must never change once released.
type namedSentinel struct {
	name string
	err  error
}

// sentinels is the immutable registry of errors that keep their identity
// across the wire. A remote error whose chain contains one of these
// satisfies errors.Is with it on the caller.
var sentinels = []namedSentinel{
	{"uniwire.ErrUnavailable", ErrUnavailable},
	{"uniwire.ErrClosed", ErrClosed},
	{"uniwire.ErrUnknownMethod", ErrUnknownMethod},
	{"drive.ErrShardNotFound", drive.ErrShardNotFound},
	{"drive.ErrShardExists", drive.ErrShardExists},
	{"drive.ErrOffline", drive.ErrOffline},
	{"drive.ErrFaulty", drive.ErrFaulty},
	{"drive.ErrCorrupt", drive.ErrCorrupt},
	{"drive.ErrClosed", drive.ErrClosed},
	{"meta.ErrNotLeader", meta.ErrNotLeader},
	{"meta.ErrRevisionMismatch", meta.ErrRevisionMismatch},
	{"meta.ErrNotFound", meta.ErrNotFound},
	{"meta.ErrValueTooLarge", meta.ErrValueTooLarge},
	{"erasure.ErrWriteQuorum", erasure.ErrWriteQuorum},
	{"erasure.ErrReadQuorum", erasure.ErrReadQuorum},
	{"erasure.ErrCorrupt", erasure.ErrCorrupt},
	{"erasure.ErrUnknownSet", erasure.ErrUnknownSet},
	{"context.Canceled", context.Canceled},
	{"context.DeadlineExceeded", context.DeadlineExceeded},
}

// lookupSentinel returns the registered sentinel with the given wire name.
func lookupSentinel(name string) error {
	for _, ns := range sentinels {
		if ns.name == name {
			return ns.err
		}
	}
	return nil
}

// findSentinel returns the wire name of the outermost registered sentinel in
// the chain of err. The chain is searched depth first, so the sentinel an
// outer wrapper added wins over one of its causes.
func findSentinel(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	for _, ns := range sentinels {
		if err == ns.err {
			return ns.name, true
		}
	}
	switch x := err.(type) {
	case interface{ Unwrap() []error }:
		for _, c := range x.Unwrap() {
			if name, ok := findSentinel(c); ok {
				return name, true
			}
		}
	case interface{ Unwrap() error }:
		return findSentinel(x.Unwrap())
	}
	return "", false
}

// encodeError converts a handler error to its wire form. The S3 code and the
// client-safe message of the outermost *s3err.Error travel; internal causes
// never do. Errors without a code become InternalError with no message.
func encodeError(err error) *uniov1.RPCError {
	out := &uniov1.RPCError{Code: string(s3err.CodeOf(err))}
	if name, ok := findSentinel(err); ok {
		out.Sentinel = name
	}
	var se *s3err.Error
	if errors.As(err, &se) {
		out.Message = se.Message
	}
	return out
}

// remoteError is an error reported by a peer. It is an s3err.Coder, so the
// S3 layer classifies it like a local error, and it unwraps to the registered
// sentinel, so errors.Is works across the wire.
type remoteError struct {
	code     s3err.Code
	message  string
	sentinel error
}

// decodeError rebuilds a remote error from its wire form. Unknown sentinel
// names are ignored; the code still classifies the error.
func decodeError(e *uniov1.RPCError) error {
	re := &remoteError{code: s3err.Code(e.GetCode()), message: e.GetMessage()}
	if re.code == "" {
		re.code = s3err.InternalError
	}
	if name := e.GetSentinel(); name != "" {
		re.sentinel = lookupSentinel(name)
	}
	return re
}

// Error implements error.
func (e *remoteError) Error() string {
	switch {
	case e.sentinel != nil && e.message != "":
		return e.sentinel.Error() + ": " + e.message
	case e.sentinel != nil:
		return e.sentinel.Error()
	case e.message != "":
		return string(e.code) + ": " + e.message
	default:
		return string(e.code)
	}
}

// Unwrap returns the registered sentinel, or nil.
func (e *remoteError) Unwrap() error { return e.sentinel }

// S3Code implements s3err.Coder.
func (e *remoteError) S3Code() s3err.Code { return e.code }
