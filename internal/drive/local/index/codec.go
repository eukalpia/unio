package index

import (
	"encoding/binary"
	"fmt"

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

func encodeLocation(loc Location) []byte {
	b := make([]byte, 0, locationSize)
	b = append(b, byte(loc.Kind))
	b = binary.BigEndian.AppendUint64(b, loc.Segment)
	b = binary.BigEndian.AppendUint64(b, uint64(loc.Offset))
	b = binary.BigEndian.AppendUint64(b, uint64(loc.Length))
	return binary.BigEndian.AppendUint64(b, uint64(loc.Created))
}

func decodeLocation(b []byte) (Location, error) {
	if len(b) != locationSize {
		return Location{}, fmt.Errorf("index: location is %d bytes, want %d", len(b), locationSize)
	}
	kind := Kind(b[0])
	if kind != KindPacked && kind != KindBlob {
		return Location{}, fmt.Errorf("index: unknown kind %d", b[0])
	}
	loc := Location{
		Kind:    kind,
		Segment: binary.BigEndian.Uint64(b[1:9]),
		Offset:  int64(binary.BigEndian.Uint64(b[9:17])),
		Length:  int64(binary.BigEndian.Uint64(b[17:25])),
		Created: int64(binary.BigEndian.Uint64(b[25:33])),
	}
	if loc.Offset < 0 || loc.Length < 0 {
		return Location{}, fmt.Errorf("index: negative offset or length")
	}
	return loc, nil
}

func encodeSegmentMeta(m SegmentMeta) []byte {
	b := make([]byte, 0, segmentMetaSize)
	b = binary.BigEndian.AppendUint64(b, uint64(m.End))
	if m.Sealed {
		return append(b, 1)
	}
	return append(b, 0)
}

func decodeSegmentMeta(b []byte) (SegmentMeta, error) {
	if len(b) != segmentMetaSize {
		return SegmentMeta{}, fmt.Errorf("index: segment meta is %d bytes, want %d", len(b), segmentMetaSize)
	}
	end := int64(binary.BigEndian.Uint64(b[:8]))
	if end < 0 || b[8] > 1 {
		return SegmentMeta{}, fmt.Errorf("index: invalid segment meta")
	}
	return SegmentMeta{End: end, Sealed: b[8] == 1}, nil
}
