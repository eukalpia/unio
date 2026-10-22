// Package versionid generates object version identifiers.
//
// An identifier is 16 bytes, hex encoded to 32 lower-case digits: the first
// 8 bytes are the big-endian Unix time in nanoseconds, so identifiers sort
// by creation time, and the last 8 bytes are random, so identifiers created
// in the same nanosecond stay unique.
package versionid

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// New returns a new version identifier: 32 lower-case hex digits. It never
// returns the empty string and is safe for concurrent use.
func New() string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixNano()))
	// crypto/rand.Read crashes the program rather than returning an error on
	// supported platforms, so the error is ignored as in package ids.
	_, _ = rand.Read(b[8:])
	return hex.EncodeToString(b[:])
}
