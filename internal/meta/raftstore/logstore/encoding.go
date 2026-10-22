package logstore

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/hashicorp/raft"
)

// Log value encoding, version 1. All integers are big-endian.
//
//	version  1 byte   always codecVersion
//	index    8 bytes
//	term     8 bytes
//	type     1 byte
//	flags    1 byte   flagDataNil, flagExtNil, flagAppendedZero
//	at.sec   8 bytes  Unix seconds of AppendedAt (0 when flagAppendedZero)
//	at.nsec  4 bytes  nanoseconds of AppendedAt (0 when flagAppendedZero)
//	dataLen  4 bytes  then dataLen bytes of Data
//	extLen   4 bytes  then extLen bytes of Extensions
//
// The flags keep nil and empty slices distinct, and keep the zero time.
// AppendedAt comes back in UTC without its monotonic reading, so it is the
// same instant but not the same time.Time value.
const (
	codecVersion byte = 1

	flagDataNil      byte = 1 << 0
	flagExtNil       byte = 1 << 1
	flagAppendedZero byte = 1 << 2
	flagsKnown            = flagDataNil | flagExtNil | flagAppendedZero

	offsetFlags   = 18
	headerLen     = 31
	lengthFieldLn = 4
)

// encodeLog returns the stored form of l. It fails only when Data or
// Extensions do not fit their 4-byte length fields.
func encodeLog(l *raft.Log) ([]byte, error) {
	if uint64(len(l.Data)) > math.MaxUint32 || uint64(len(l.Extensions)) > math.MaxUint32 {
		return nil, fmt.Errorf("encode log %d: data of %d bytes and extensions of %d bytes exceed the 4 GiB field limit",
			l.Index, len(l.Data), len(l.Extensions))
	}

	var flags byte
	if l.Data == nil {
		flags |= flagDataNil
	}
	if l.Extensions == nil {
		flags |= flagExtNil
	}
	var (
		sec  int64
		nsec uint32
	)
	if l.AppendedAt.IsZero() {
		flags |= flagAppendedZero
	} else {
		sec = l.AppendedAt.Unix()
		nsec = uint32(l.AppendedAt.Nanosecond())
	}

	buf := make([]byte, 0, headerLen+2*lengthFieldLn+len(l.Data)+len(l.Extensions))
	buf = append(buf, codecVersion)
	buf = binary.BigEndian.AppendUint64(buf, l.Index)
	buf = binary.BigEndian.AppendUint64(buf, l.Term)
	buf = append(buf, byte(l.Type), flags)
	buf = binary.BigEndian.AppendUint64(buf, uint64(sec))
	buf = binary.BigEndian.AppendUint32(buf, nsec)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(l.Data)))
	buf = append(buf, l.Data...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(l.Extensions)))
	buf = append(buf, l.Extensions...)
	return buf, nil
}

// decodeLog parses a value written by encodeLog. It returns an error for any
// input that encodeLog could not have produced, and it never panics. The
// returned slices are copies, so b may be reused.
func decodeLog(b []byte) (raft.Log, error) {
	var l raft.Log
	if len(b) < headerLen {
		return l, fmt.Errorf("decode log: %d bytes is shorter than the %d byte header", len(b), headerLen)
	}
	if b[0] != codecVersion {
		return l, fmt.Errorf("decode log: unsupported version %d", b[0])
	}
	flags := b[offsetFlags]
	if flags&^flagsKnown != 0 {
		return l, fmt.Errorf("decode log: unknown flags %#x", flags)
	}
	nsec := binary.BigEndian.Uint32(b[27:31])
	if nsec >= 1_000_000_000 {
		return l, fmt.Errorf("decode log: nanoseconds %d out of range", nsec)
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
		return raft.Log{}, fmt.Errorf("decode log: %d trailing bytes", len(rest))
	}

	l.Data, l.Extensions = data, ext
	if flags&flagAppendedZero == 0 {
		sec := int64(binary.BigEndian.Uint64(b[19:27]))
		l.AppendedAt = time.Unix(sec, int64(nsec)).UTC()
	}
	return l, nil
}

// readField reads one length-prefixed field from b and returns it with the
// bytes after it. A field marked nil must have length zero and decodes as nil.
func readField(b []byte, isNil bool) ([]byte, []byte, error) {
	if len(b) < lengthFieldLn {
		return nil, nil, fmt.Errorf("length prefix truncated after %d bytes", len(b))
	}
	n := uint64(binary.BigEndian.Uint32(b[:lengthFieldLn]))
	b = b[lengthFieldLn:]
	if n > uint64(len(b)) {
		return nil, nil, fmt.Errorf("length %d exceeds the %d bytes left", n, len(b))
	}
	if isNil {
		if n != 0 {
			return nil, nil, fmt.Errorf("nil field has length %d", n)
		}
		return nil, b, nil
	}
	field := make([]byte, int(n))
	copy(field, b[:n])
	return field, b[n:], nil
}
