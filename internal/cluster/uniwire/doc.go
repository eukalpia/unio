// Package uniwire is the internode transport: request/response RPC and
// one-shot streams between nodes over mutual TLS 1.3 (ADR 0003).
//
// # Wire format
//
// Every connection starts with a 6-byte preamble: the magic "UNIW", the
// protocol version (1) and the kind (1 = RPC, 2 = Stream). Anything else is
// rejected before further parsing. All integers are big-endian.
//
// RPC connections carry frames:
//
//	length  u32   bytes that follow; at most Limits.MaxFrameSize
//	id      u64   request id chosen by the caller
//	type    u8    1 request, 2 response, 3 error, 4 cancel, 5 ping, 6 pong
//	mlen    u8    method length; non-zero only for requests
//	method  mlen bytes
//	payload rest
//
// A request payload begins with the caller's remaining time in nanoseconds
// (i64; 0 means none). The protobuf request follows. An error payload is a
// protobuf RPCError. A cancel frame names the request to abandon. Replies may
// arrive in any order. Ping and pong keep an idle connection alive.
//
// Stream connections carry one transfer at a time, with no multiplexing.
// The caller sends a length-prefixed protobuf StreamHeader (method, payload,
// body length, remaining time), then exactly that many raw bytes. The callee
// replies with a StreamHeader (payload, body length), that many raw bytes,
// and a StreamTrailer whose error field is the outcome. A connection whose
// body cannot be completed is closed, because its framing is lost.
//
// # Errors
//
// A failed call carries an S3 code, a client-safe message and the wire name
// of the outermost registered sentinel (see errors.go). The caller sees a
// value that satisfies errors.Is for that sentinel and s3err.CodeOf for the
// code. Causes that are not registered and not *s3err.Error arrive as
// InternalError with no detail.
//
// # Trust
//
// Each side verifies the peer's certificate chain and node identity during
// the handshake (see package pki). A client also checks that the peer is the
// node it dialed.
package uniwire
