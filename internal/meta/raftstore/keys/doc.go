// Package keys defines the order-preserving byte encoding of the metadata
// state machine's Pebble keyspace, for one Raft partition.
//
// Every key starts with a one-byte kind prefix (see the Prefix constants), so
// the kinds never interleave. Integers are big-endian, which keeps their
// numeric order in byte order. Object names and upload ids are escaped so
// that arbitrary byte strings can be used as components:
//
//   - Escaping replaces each 0x00 with 0x00 0xFF and leaves other bytes
//     unchanged. A terminated component appends the terminator 0x00 0x01.
//     An escaped 0x00 is always followed by 0xFF, so 0x00 0x01 can only be
//     the terminator. The terminated code is therefore prefix-free and keeps
//     byte order for arbitrary input.
//
// Layouts:
//
//	object version  O | bucketID(8) | esc(key) 00 01 | ^ts(8)
//	upload          U | bucketID(8) | esc(key) 00 01 | uploadID
//	part            P | bucketID(8) | esc(uploadID) 00 01 | partNumber(4)
//	bucket record   B | name
//	system KV       K | key
//	control record  F | bucketID(8)
//	partition meta  M | name
//
// ^ts is the bitwise NOT of the timestamp, so newer versions sort first. The
// null version uses ts 0, which sorts after every real version of its key.
//
// The package validates structure only. Key validity (UTF-8, length, NUL) is
// the object layer's job. Parse functions return errors wrapping ErrMalformed
// for malformed input and never panic. Slices returned by the Parse functions
// may alias their input, so callers that keep them must copy.
package keys
