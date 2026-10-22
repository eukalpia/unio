package bitrot

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/zeebo/xxh3"

	"github.com/eukalpia/unio/internal/ids"
)

// specDigest computes a block digest exactly as the format defines it: the
// xxh3-128 of the payload, seeded with the xxh3-64 of
// DataID || BE16(shard) || BE64(block).
func specDigest(data ids.DataID, shard uint16, block uint64, payload []byte) [HashSize]byte {
	var key bytes.Buffer
	key.Write(data[:])
	_ = binary.Write(&key, binary.BigEndian, shard)
	_ = binary.Write(&key, binary.BigEndian, block)
	return xxh3.Hash128Seed(payload, xxh3.Hash(key.Bytes())).Bytes()
}

// digestAt returns the digest that a writer at position p stamps on payload
// as its n-th block.
func digestAt(t *testing.T, p Position, n uint64, payload []byte) [HashSize]byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf, Position{Data: p.Data, Shard: p.Shard, FirstBlock: p.FirstBlock + n})
	if err := w.WriteBlock(payload); err != nil {
		t.Fatal(err)
	}
	var sum [HashSize]byte
	copy(sum[:], buf.Bytes())
	return sum
}

func TestBlockDigestFollowsSpecification(t *testing.T) {
	payload := []byte("block payload")
	tests := []struct {
		name string
		pos  Position
		n    uint64
	}{
		{"zero position", Position{}, 0},
		{"typical", Position{Data: ids.DataID{1, 2, 3}, Shard: 7, FirstBlock: 0}, 0},
		{"later block", Position{Data: ids.DataID{9}, Shard: 65535, FirstBlock: 1 << 40}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf, tt.pos)
			for range tt.n {
				if err := w.WriteBlock(payload); err != nil {
					t.Fatal(err)
				}
			}
			buf.Reset()
			if err := w.WriteBlock(payload); err != nil {
				t.Fatal(err)
			}
			want := specDigest(tt.pos.Data, tt.pos.Shard, tt.pos.FirstBlock+tt.n, payload)
			if got := buf.Bytes()[:HashSize]; !bytes.Equal(got, want[:]) {
				t.Fatalf("digest = %x, want %x", got, want)
			}
		})
	}
}

func TestDigestBindsEveryPositionField(t *testing.T) {
	base := Position{Data: ids.DataID{0xAA}, Shard: 3, FirstBlock: 10}
	variants := map[string]Position{
		"data id":     {Data: ids.DataID{0xAB}, Shard: 3, FirstBlock: 10},
		"shard index": {Data: ids.DataID{0xAA}, Shard: 4, FirstBlock: 10},
		"block index": {Data: ids.DataID{0xAA}, Shard: 3, FirstBlock: 11},
	}
	payload := []byte("the same bytes at every position")
	want := digestAt(t, base, 0, payload)
	for name, v := range variants {
		if digestAt(t, v, 0, payload) == want {
			t.Fatalf("changing the %s does not change the digest", name)
		}
	}
}

func TestPositionOfExtent(t *testing.T) {
	sid := ids.ShardID{Data: ids.DataID{5}, Index: 6}
	got := Extent{Offset: 99, Length: 10, FirstBlock: 4}.Position(sid)
	want := Position{Data: sid.Data, Shard: sid.Index, FirstBlock: 4}
	if got != want {
		t.Fatalf("Position = %+v, want %+v", got, want)
	}
}
