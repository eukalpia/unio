package logstore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/hashicorp/raft"
)

// ErrCorrupt reports a stored value that the encoder could not have written:
// it is truncated, has an unknown version or flag, holds an out-of-range field,
// or has trailing bytes. Every decoding error wraps it, so callers can tell
// damage on disk from a programming error.
var ErrCorrupt = errors.New("logstore: corrupt stored value")

// corruptf returns a decoding error that wraps ErrCorrupt.
func corruptf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}

// Log value encoding, version 1. All integers are big-endian.
//
//	version  1 byte   always codecVersion
//	index    8 bytes
//	term     8 bytes
//	type     1 byte
//	flags    1 byte   flagDataNil, flagExtNil, flagAppendedZero
//	at.sec   8 bytes  Unix seconds of AppendedAt, unsigned (0 when flagAppendedZero)
//	at.nsec  4 bytes  nanoseconds of AppendedAt (0 when flagAppendedZero)
//	dataLen  4 bytes  then dataLen bytes of Data
//	extLen   4 bytes  then extLen bytes of Extensions
//
// The flags keep nil and empty slices distinct, and keep the zero time. The
// seconds are stored unsigned: an append time before the Unix epoch cannot be
// encoded, and a stored value with the sign bit set is corrupt. Raft stamps
// entries with the current time, so the restriction never applies to Raft.
// AppendedAt comes back in UTC without its monotonic reading, so it is the
// same instant but not the same time.Time value.
const (
	codecVersion byte = 1

	flagDataNil      byte = 1 << 0
	flagExtNil       byte = 1 << 1
	flagAppendedZero byte = 1 << 2
	flagsKnown            = flagDataNil | flagExtNil | flagAppendedZero

	offsetFlags   = 18
	offsetSec     = 19
	offsetNsec    = 27
	headerLen     = 31
	lengthFieldLn = 4
)

// uint32Field converts n to the value of a 4-byte field. It fails when n is
// negative or does not fit, so the conversion never wraps.
func uint32Field(n int) (uint32, error) {
	if n < 0 || int64(n) > math.MaxUint32 {
		return 0, fmt.Errorf("value %d does not fit a 4-byte field", n)
	}
	return uint32(n), nil
}

// encodeLog returns the stored form of l. It fails when Data or Extensions do
// not fit their 4-byte length fields, or when AppendedAt is before the epoch.
func encodeLog(l *raft.Log) ([]byte, error) {
	dataLen, err := uint32Field(len(l.Data))
	if err != nil {
		return nil, fmt.Errorf("encode log %d: data: %w", l.Index, err)
	}
	extLen, err := uint32Field(len(l.Extensions))
	if err != nil {
		return nil, fmt.Errorf("encode log %d: extensions: %w", l.Index, err)
	}

	var flags byte
	if l.Data == nil {
		flags |= flagDataNil
	}
	if l.Extensions == nil {
		flags |= flagExtNil
	}
	var (
		sec  uint64
		nsec uint32
	)
	if l.AppendedAt.IsZero() {
		flags |= flagAppendedZero
	} else {
		unix := l.AppendedAt.Unix()
		if unix < 0 {
			return nil, fmt.Errorf("encode log %d: append time %v is before the Unix epoch", l.Index, l.AppendedAt)
		}
		sec = uint64(unix)
		nsec, err = uint32Field(l.AppendedAt.Nanosecond())
		if err != nil {
			return nil, fmt.Errorf("encode log %d: nanoseconds: %w", l.Index, err)
		}
	}

	buf := make([]byte, 0, headerLen+2*lengthFieldLn+len(l.Data)+len(l.Extensions))
	buf = append(buf, codecVersion)
	buf = binary.BigEndian.AppendUint64(buf, l.Index)
	buf = binary.BigEndian.AppendUint64(buf, l.Term)
	buf = append(buf, byte(l.Type), flags)
	buf = binary.BigEndian.AppendUint64(buf, sec)
	buf = binary.BigEndian.AppendUint32(buf, nsec)
	buf = binary.BigEndian.AppendUint32(buf, dataLen)
	buf = append(buf, l.Data...)
	buf = binary.BigEndian.AppendUint32(buf, extLen)
	buf = append(buf, l.Extensions...)
	return buf, nil
}

// decodeLog parses a value written by encodeLog. It returns an error wrapping
// ErrCorrupt for any input that encodeLog could not have produced, and it never
// panics. The returned slices are copies, so b may be reused.
func decodeLog(b []byte) (raft.Log, error) {
	var l raft.Log
	if len(b) < headerLen {
		return l, corruptf("%d bytes is shorter than the %d byte header", len(b), headerLen)
	}
	if b[0] != codecVersion {
		return l, corruptf("unsupported version %d", b[0])
	}
	flags := b[offsetFlags]
	if flags&^flagsKnown != 0 {
		return l, corruptf("unknown flags %#x", flags)
	}
	nsec := binary.BigEndian.Uint32(b[offsetNsec : offsetNsec+4])
	if nsec >= 1_000_000_000 {
		return l, corruptf("nanoseconds %d out of range", nsec)
	}

	l.Index = binary.BigEndian.Uint64(b[1:9])
	l.Term = binary.BigEndian.Uint64(b[9:17])
	l.Type = raft.LogType(b[17])

	rest := b[headerLen:]
	data, rest, err := readField(rest, flags&flagDataNil != 0)
	if err != nil {
		return raft.Log{}, fmt.Errorf("decode log: data: %w", err)
	}
	ext, rest, err := readField(rest, flags&flagExtNil != 0)
	if err != nil {
		return raft.Log{}, fmt.Errorf("decode log: extensions: %w", err)
	}
	if len(rest) != 0 {
		return raft.Log{}, corruptf("%d trailing bytes", len(rest))
	}

	l.Data, l.Extensions = data, ext
	if flags&flagAppendedZero == 0 {
		raw := binary.BigEndian.Uint64(b[offsetSec : offsetSec+8])
		if raw > math.MaxInt64 {
			return raft.Log{}, corruptf("append seconds %d are before the Unix epoch", raw)
		}
		l.AppendedAt = time.Unix(int64(raw), int64(nsec)).UTC()
	}
	return l, nil
}

// readField reads one length-prefixed field from b and returns it with the
// bytes after it. A field marked nil must have length zero and decodes as nil.
func readField(b []byte, isNil bool) ([]byte, []byte, error) {
	if len(b) < lengthFieldLn {
		return nil, nil, corruptf("length prefix truncated after %d bytes", len(b))
	}
	n := binary.BigEndian.Uint32(b[:lengthFieldLn])
	b = b[lengthFieldLn:]
	if int64(n) > int64(len(b)) {
		return nil, nil, corruptf("length %d exceeds the %d bytes left", n, len(b))
	}
	if isNil {
		if n != 0 {
			return nil, nil, corruptf("nil field has length %d", n)
		}
		return nil, b, nil
	}
	field := make([]byte, n)
	copy(field, b[:n])
	return field, b[n:], nil
}
