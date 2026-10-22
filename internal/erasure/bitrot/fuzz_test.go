package bitrot

import (
	"bytes"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// shardFixture frames one shard of an object of len(data) bytes at position
// p. Block s holds BlockLen bytes taken from data, so block contents differ
// per stripe.
func shardFixture(data []byte, k, stripe int, p Position) (blocks [][]byte, stream []byte) {
	size := int64(len(data))
	n := Stripes(size, stripe)
	var buf bytes.Buffer
	w := NewWriter(&buf, p)
	for s := range n {
		bl := BlockLen(size, k, stripe, s)
		block := make([]byte, bl)
		for i := range block {
			block[i] = data[(int(s)*stripe+i)%len(data)]
		}
		blocks = append(blocks, block)
		_ = w.WriteBlock(block)
	}
	return blocks, buf.Bytes()
}

// blockOffsets returns the framed offset of each block of blocks.
func blockOffsets(blocks [][]byte) []int {
	offs := make([]int, len(blocks))
	pos := 0
	for i, b := range blocks {
		offs[i] = pos
		pos += HashSize + len(b)
	}
	return offs
}

// readAll reads every block of stream at position p and reports the first
// verification error, plus the number of blocks verified before it.
func readAll(stream []byte, p Position, blocks [][]byte) (verified int, err error) {
	r := NewReader(bytes.NewReader(stream), p)
	for _, want := range blocks {
		dst := make([]byte, len(want))
		if err := r.ReadBlock(dst); err != nil {
			return verified, err
		}
		if !bytes.Equal(dst, want) {
			return verified, errors.New("verified block differs from original")
		}
		verified++
	}
	return verified, r.Done()
}

// otherPosition returns a position that differs from p in one field, chosen
// by sel. The delta is nonzero, so the result always differs from p.
func otherPosition(p Position, sel, delta byte) Position {
	q := p
	switch sel % 3 {
	case 0:
		q.Data[int(sel)%len(q.Data)] ^= delta
	case 1:
		q.Shard += uint16(delta)
	default:
		q.FirstBlock += uint64(delta)
	}
	return q
}

// FuzzCorruptionDetected checks that a single-byte flip, truncation,
// extension, block transplant or block swap of a framed shard is always
// detected as erasure.ErrCorrupt, and that no unverified bytes are returned.
func FuzzCorruptionDetected(f *testing.F) {
	f.Add([]byte("erasure coding with bitrot protection"), uint8(4), uint16(7), uint32(10), byte(0x01))
	f.Add([]byte{0}, uint8(1), uint16(1), uint32(0), byte(0x80))
	f.Add(bytes.Repeat([]byte{0xFF}, 300), uint8(3), uint16(64), uint32(999), byte(0xFF))
	f.Add(bytes.Repeat([]byte("transplant"), 40), uint8(2), uint16(16), uint32(3), byte(0x3C))
	f.Add(bytes.Repeat([]byte("swap me"), 40), uint8(5), uint16(9), uint32(8), byte(0x11))
	f.Fuzz(func(t *testing.T, data []byte, k uint8, stripe uint16, pos uint32, mask byte) {
		if len(data) == 0 || mask == 0 {
			t.Skip()
		}
		kk := int(k%8) + 1
		st := int(stripe%512) + 1
		p := testPos
		blocks, stream := shardFixture(data, kk, st, p)
		if verified, err := readAll(stream, p, blocks); err != nil {
			t.Fatalf("pristine shard failed after %d blocks: %v", verified, err)
		}
		n := len(blocks)
		offs := blockOffsets(blocks)
		corrupt := bytes.Clone(stream)

		mode := pos % 5
		switch mode {
		case 0: // flip one byte
			corrupt[int(pos)%len(corrupt)] ^= mask
		case 1: // truncate, leaving at least one byte missing
			corrupt = corrupt[:int(pos)%len(corrupt)]
		case 2: // extend with trailing garbage
			corrupt = append(corrupt, mask)
		case 3: // transplant a valid block framed at another position
			b := int(pos/5) % n
			q := otherPosition(p, byte(pos/5), mask)
			_, src := shardFixture(data, kk, st, q)
			// Framing lengths match, so the offsets of src equal those of the target.
			copy(corrupt[offs[b]:], src[offs[b]:offs[b]+HashSize+len(blocks[b])])
		case 4: // swap two blocks of equal length
			if n < 2 {
				t.Skip()
			}
			b := int(pos/5) % n
			c := -1
			for j := range n {
				if cand := (b + 1 + j) % n; cand != b && len(blocks[cand]) == len(blocks[b]) {
					c = cand
					break
				}
			}
			if c < 0 {
				t.Skip()
			}
			bf := corrupt[offs[b] : offs[b]+HashSize+len(blocks[b])]
			cf := corrupt[offs[c] : offs[c]+HashSize+len(blocks[c])]
			tmp := bytes.Clone(bf)
			copy(bf, cf)
			copy(cf, tmp)
		}
		if bytes.Equal(corrupt, stream) {
			t.Skip() // the mutation was a no-op
		}
		_, err := readAll(corrupt, p, blocks)
		if !errors.Is(err, erasure.ErrCorrupt) {
			t.Fatalf("mode %d (pos %d) not detected: err = %v", mode, pos, err)
		}
	})
}

// TestEveryBitFlipDetected flips every bit of a small framed shard, one at a
// time, and requires each flip to be detected.
func TestEveryBitFlipDetected(t *testing.T) {
	data := []byte("exhaustive bit flip coverage for bitrot")
	blocks, stream := shardFixture(data, 3, 8, testPos)
	for i := range stream {
		for bit := range 8 {
			corrupt := bytes.Clone(stream)
			corrupt[i] ^= 1 << bit
			_, err := readAll(corrupt, testPos, blocks)
			if !errors.Is(err, erasure.ErrCorrupt) {
				t.Fatalf("flip byte %d bit %d not detected: %v", i, bit, err)
			}
		}
	}
}

// TestEveryTransplantDetected copies each block of a shard, in turn, into
// each other block position, and into every position that differs from the
// shard's position in one field. Each copy must be detected.
func TestEveryTransplantDetected(t *testing.T) {
	data := []byte("every block of this shard is transplanted somewhere it does not belong")
	const k, stripe = 2, 9
	blocks, stream := shardFixture(data, k, stripe, testPos)
	offs := blockOffsets(blocks)
	alternatives := []Position{
		{Data: ids.DataID{0x99}, Shard: testPos.Shard, FirstBlock: testPos.FirstBlock},
		{Data: testPos.Data, Shard: testPos.Shard + 1, FirstBlock: testPos.FirstBlock},
		{Data: testPos.Data, Shard: testPos.Shard, FirstBlock: testPos.FirstBlock + 1},
	}
	for src := range blocks {
		for _, alt := range alternatives {
			_, srcStream := shardFixture(data, k, stripe, alt)
			for dst := range blocks {
				// A copy whose label (position) equals the destination label is
				// a valid block there, not a misdirected one.
				if alt.FirstBlock+uint64(src) == testPos.FirstBlock+uint64(dst) && alt.Data == testPos.Data && alt.Shard == testPos.Shard {
					continue
				}
				if len(blocks[dst]) != len(blocks[src]) {
					continue
				}
				corrupt := bytes.Clone(stream)
				framedLen := HashSize + len(blocks[src])
				copy(corrupt[offs[dst]:], srcStream[offs[src]:offs[src]+framedLen])
				if bytes.Equal(corrupt, stream) {
					continue
				}
				if _, err := readAll(corrupt, testPos, blocks); !errors.Is(err, erasure.ErrCorrupt) {
					t.Fatalf("block %d from %+v placed at %d: err = %v, want ErrCorrupt", src, alt, dst, err)
				}
			}
		}
	}
}
