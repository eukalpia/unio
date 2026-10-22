package index

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/cockroachdb/pebble/v2"
)

// rawLocation encodes an index value field by field, so that tests can write
// values no valid Location can produce.
func rawLocation(kind byte, seg, off, length, created uint64) []byte {
	b := []byte{kind}
	for _, v := range []uint64{seg, off, length, created} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	return b
}

// TestGetReportsCorruptValuesAsErrCorrupt writes index values that cannot
// describe a location. Get must report each one as ErrCorrupt: never a panic and
// never a negative offset or length.
func TestGetReportsCorruptValuesAsErrCorrupt(t *testing.T) {
	x, _ := openIndex(t)
	cases := map[string][]byte{
		"offset-beyond-int64":  rawLocation(byte(KindPacked), 1, 1<<63, 10, 1),
		"length-beyond-int64":  rawLocation(byte(KindPacked), 1, 0, 1<<63, 1),
		"created-beyond-int64": rawLocation(byte(KindBlob), 0, 0, 10, 1<<63),
		"unknown-kind":         rawLocation(9, 1, 0, 10, 1),
		"short-value":          {1, 2, 3},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			id := testID(7)
			if err := x.db.Set(shardKey(id), raw, pebble.NoSync); err != nil {
				t.Fatalf("write raw value: %v", err)
			}
			if _, err := x.Get(id); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Get: err = %v, want ErrCorrupt", err)
			}
		})
	}
}

// TestPutShardRejectsNegativeFields checks the encode side: a negative offset,
// length or creation time has no on-disk form and must be refused rather than
// wrapped into a huge unsigned value.
func TestPutShardRejectsNegativeFields(t *testing.T) {
	x, _ := openIndex(t)
	b := x.Batch()
	defer func() { _ = b.Discard() }()
	for _, loc := range []Location{
		{Kind: KindPacked, Segment: 1, Offset: -1, Length: 10, Created: 1},
		{Kind: KindPacked, Segment: 1, Offset: 0, Length: -1, Created: 1},
		{Kind: KindBlob, Length: 10, Created: -1},
	} {
		if err := b.PutShard(testID(3), loc); err == nil {
			t.Fatalf("PutShard accepted %+v", loc)
		}
	}
}
