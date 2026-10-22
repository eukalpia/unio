package uniwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// RPC frames carry one message on an RPC connection:
//
//	length  u32   bytes that follow (at most MaxFrameSize)
//	id      u64   request id, chosen by the caller
//	type    u8    frameRequest, frameResponse, frameError, frameCancel, framePing, framePong
//	mlen    u8    method length (non-empty only for frameRequest)
//	method  [mlen]byte
//	payload       the rest
//
// A request payload starts with the caller's remaining time as an i64 of
// nanoseconds (0 means no deadline), followed by the protobuf request. A
// frameError payload is a protobuf RPCError. All integers are big-endian.
type frameType uint8

// Frame types.
const (
	frameRequest  frameType = 1
	frameResponse frameType = 2
	frameError    frameType = 3
	frameCancel   frameType = 4
	framePing     frameType = 5
	framePong     frameType = 6
)

// rpcFixedLen is the length of the frame body before the method name.
const rpcFixedLen = 8 + 1 + 1

// maxMethodLen is the longest method name a frame can carry.
const maxMethodLen = 255

// errMalformedFrame is returned for any frame that violates the layout.
var errMalformedFrame = errors.New("uniwire: malformed frame")

// errFrameTooLarge is returned when a frame would exceed MaxFrameSize. Nothing
// is written in that case, so the connection stays usable.
var errFrameTooLarge = errors.New("uniwire: frame too large")

// rpcFrame is a decoded RPC frame. Payload aliases a buffer owned by the
// frame, so the caller may keep it.
type rpcFrame struct {
	typ     frameType
	id      uint64
	method  string
	payload []byte
}

// lengthU32 converts a length to the uint32 field that carries it on the wire.
// It refuses a negative length, a length above limit, and a length that the
// field cannot hold, so an encoder can never truncate a length.
func lengthU32(n, limit int) (uint32, error) {
	if n < 0 {
		return 0, fmt.Errorf("uniwire: negative length %d", n)
	}
	if n > limit || int64(n) > math.MaxUint32 {
		return 0, fmt.Errorf("%w: length %d exceeds limit %d", errFrameTooLarge, n, limit)
	}
	return uint32(n), nil
}

// lengthU8 converts a length to the uint8 field that carries it on the wire. It
// refuses a negative length and a length above limit or above 255.
func lengthU8(n, limit int) (uint8, error) {
	if n < 0 || n > limit || n > math.MaxUint8 {
		return 0, fmt.Errorf("uniwire: length %d outside [0, %d]", n, min(limit, math.MaxUint8))
	}
	return uint8(n), nil
}

// appendRPCFrame appends one complete frame to dst. The parts are
// concatenated as the payload. It fails, without writing anything to dst's
// existing bytes, when the method name or the body does not fit its field or
// exceeds max.
func appendRPCFrame(dst []byte, max int, typ frameType, id uint64, method string, parts ...[]byte) ([]byte, error) {
	mlen, err := lengthU8(len(method), maxMethodLen)
	if err != nil {
		return dst, fmt.Errorf("uniwire: method name: %w", err)
	}
	body := rpcFixedLen + len(method)
	for _, p := range parts {
		body += len(p)
	}
	blen, err := lengthU32(body, max)
	if err != nil {
		return dst, err
	}
	dst = binary.BigEndian.AppendUint32(dst, blen)
	dst = binary.BigEndian.AppendUint64(dst, id)
	dst = append(dst, byte(typ), mlen)
	dst = append(dst, method...)
	for _, p := range parts {
		dst = append(dst, p...)
	}
	return dst, nil
}

// readRPCFrame reads one frame from r. The body length is checked against
// max before anything is allocated.
func readRPCFrame(r io.Reader, max int) (rpcFrame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return rpcFrame{}, err
	}
	n := int(binary.BigEndian.Uint32(hdr[:]))
	if n < rpcFixedLen || n > max {
		return rpcFrame{}, fmt.Errorf("%w: body length %d outside [%d, %d]", errMalformedFrame, n, rpcFixedLen, max)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return rpcFrame{}, err
	}
	f := rpcFrame{
		id:  binary.BigEndian.Uint64(body[:8]),
		typ: frameType(body[8]),
	}
	mlen := int(body[9])
	if rpcFixedLen+mlen > n {
		return rpcFrame{}, fmt.Errorf("%w: method length %d overruns body", errMalformedFrame, mlen)
	}
	switch f.typ {
	case frameRequest:
	case frameResponse, frameError, frameCancel, framePing, framePong:
		if mlen != 0 {
			return rpcFrame{}, fmt.Errorf("%w: type %d carries a method", errMalformedFrame, f.typ)
		}
	default:
		return rpcFrame{}, fmt.Errorf("%w: unknown type %d", errMalformedFrame, f.typ)
	}
	f.method = string(body[rpcFixedLen : rpcFixedLen+mlen])
	f.payload = body[rpcFixedLen+mlen:]
	return f, nil
}

// vtMessage is a protobuf message with vtprotobuf fast paths. It has the same
// method set as cluster.Message.
type vtMessage interface {
	MarshalVT() ([]byte, error)
	UnmarshalVT([]byte) error
}

// appendMessageFrame appends msg as a length-prefixed protobuf frame. It is
// used for stream headers and trailers, which are not multiplexed.
func appendMessageFrame(dst []byte, max int, msg vtMessage) ([]byte, error) {
	b, err := msg.MarshalVT()
	if err != nil {
		return dst, fmt.Errorf("uniwire: marshal frame: %w", err)
	}
	blen, err := lengthU32(len(b), max)
	if err != nil {
		return dst, fmt.Errorf("uniwire: message: %w", err)
	}
	dst = binary.BigEndian.AppendUint32(dst, blen)
	return append(dst, b...), nil
}

// readMessageFrame reads a length-prefixed protobuf frame into msg. The
// length is checked against max before anything is allocated.
func readMessageFrame(r io.Reader, max int, msg vtMessage) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := int(binary.BigEndian.Uint32(hdr[:]))
	// n is negative when int is 32 bits and the length has its top bit set.
	if n < 0 || n > max {
		return fmt.Errorf("%w: message length %d outside [0, %d]", errMalformedFrame, n, max)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	if err := msg.UnmarshalVT(b); err != nil {
		return fmt.Errorf("%w: %w", errMalformedFrame, err)
	}
	return nil
}
