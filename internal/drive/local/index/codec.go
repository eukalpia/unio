package index

import (
	"encoding/binary"
	"fmt"

	"github.com/eukalpia/unio/internal/drive/local/internal/safeint"
	"github.com/eukalpia/unio/internal/ids"
)

// Key prefixes. Shard keys and segment keys live in disjoint ranges so that
// iterating shards never sees segment metadata.
const (
	prefixShard   byte = 0x01
	prefixSegment byte = 0x02
)

// locationSize is the encoded size of a Location.
const locationSize = 1 + 8 + 8 + 8 + 8

// segmentMetaSize is the encoded size of a SegmentMeta.
const segmentMetaSize = 8 + 1

func shardKey(id ids.ShardID) []byte {
	k := make([]byte, 0, 1+ids.ShardIDSize)
	k = append(k, prefixShard)
	return id.AppendBinary(k)
}

func segmentKey(seg uint64) []byte {
	k := make([]byte, 0, 1+8)
	k = append(k, prefixSegment)
	return binary.BigEndian.AppendUint64(k, seg)
}

// encodeLocation returns the index value of loc. Offsets, lengths and creation
// times must be non-negative: a negative value has no unsigned form, and
// writing one would store a huge number that reads back as corrupt.
func encodeLocation(loc Location) ([]byte, error) {
	if loc.Kind != KindPacked && loc.Kind != KindBlob {
		return nil, fmt.Errorf("index: unknown kind %d", loc.Kind)
	}
	offset, okOff := safeint.ToUint64(loc.Offset)
	length, okLen := safeint.ToUint64(loc.Length)
	created, okCre := safeint.ToUint64(loc.Created)
	if !okOff || !okLen || !okCre {
		return nil, fmt.Errorf("index: negative offset, length or creation time in %+v", loc)
	}
	b := make([]byte, 0, locationSize)
	b = append(b, byte(loc.Kind))
	b = binary.BigEndian.AppendUint64(b, loc.Segment)
	b = binary.BigEndian.AppendUint64(b, offset)
	b = binary.BigEndian.AppendUint64(b, length)
	return binary.BigEndian.AppendUint64(b, created), nil
}

// decodeLocation parses an index value. Every failure wraps ErrCorrupt, because
// an index value is only ever written by this package.
func decodeLocation(b []byte) (Location, error) {
	if len(b) != locationSize {
		return Location{}, fmt.Errorf("%w: value is %d bytes, want %d", ErrCorrupt, len(b), locationSize)
	}
	kind := Kind(b[0])
	if kind != KindPacked && kind != KindBlob {
		return Location{}, fmt.Errorf("%w: unknown kind %d", ErrCorrupt, b[0])
	}
	offset, okOff := safeint.FromUint64(binary.BigEndian.Uint64(b[9:17]))
	length, okLen := safeint.FromUint64(binary.BigEndian.Uint64(b[17:25]))
	created, okCre := safeint.FromUint64(binary.BigEndian.Uint64(b[25:33]))
	if !okOff || !okLen || !okCre {
		return Location{}, fmt.Errorf("%w: a field exceeds the signed range", ErrCorrupt)
	}
	return Location{
		Kind:    kind,
		Segment: binary.BigEndian.Uint64(b[1:9]),
		Offset:  offset,
		Length:  length,
		Created: created,
	}, nil
}

// encodeSegmentMeta returns the index value of m. The committed end must be
// non-negative.
func encodeSegmentMeta(m SegmentMeta) ([]byte, error) {
	end, ok := safeint.ToUint64(m.End)
	if !ok {
		return nil, fmt.Errorf("index: negative segment end %d", m.End)
	}
	b := make([]byte, 0, segmentMetaSize)
	b = binary.BigEndian.AppendUint64(b, end)
	if m.Sealed {
		return append(b, 1), nil
	}
	return append(b, 0), nil
}

func decodeSegmentMeta(b []byte) (SegmentMeta, error) {
	if len(b) != segmentMetaSize {
		return SegmentMeta{}, fmt.Errorf("%w: segment value is %d bytes, want %d", ErrCorrupt, len(b), segmentMetaSize)
	}
	end, ok := safeint.FromUint64(binary.BigEndian.Uint64(b[:8]))
	if !ok || b[8] > 1 {
		return SegmentMeta{}, fmt.Errorf("%w: invalid segment value", ErrCorrupt)
	}
	return SegmentMeta{End: end, Sealed: b[8] == 1}, nil
}

func parseShardKey(k []byte) (ids.ShardID, error) {
	if len(k) != 1+ids.ShardIDSize || k[0] != prefixShard {
		return ids.ShardID{}, fmt.Errorf("%w: key of %d bytes", ErrCorrupt, len(k))
	}
	return ids.ParseShardIDBinary(k[1:])
}
