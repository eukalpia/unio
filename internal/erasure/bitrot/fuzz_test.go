package bitrot

import (
	"bytes"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
)

// shardFixture frames one shard of an object of len(data) bytes. Block i
// holds BlockLen bytes taken from data, so block contents differ per stripe.
func shardFixture(data []byte, k, stripe int) (blocks [][]byte, stream []byte) {
	size := int64(len(data))
	n := Stripes(size, stripe)
	var buf bytes.Buffer
	w := NewWriter(&buf)
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

// readAll reads every block of stream and reports the first verification
// error, plus the bytes that were returned before it.
func readAll(stream []byte, blocks [][]byte) (verified int, err error) {
	r := NewReader(bytes.NewReader(stream))
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

// FuzzCorruptionDetected checks that any single-byte flip, truncation or
// extension of a framed shard is detected as erasure.ErrCorrupt, and that
// no unverified bytes are returned.
func FuzzCorruptionDetected(f *testing.F) {
	f.Add([]byte("erasure coding with bitrot protection"), uint8(4), uint16(7), uint32(10), byte(0x01))
	f.Add([]byte{0}, uint8(1), uint16(1), uint32(0), byte(0x80))
	f.Add(bytes.Repeat([]byte{0xFF}, 300), uint8(3), uint16(64), uint32(999), byte(0xFF))
	f.Fuzz(func(t *testing.T, data []byte, k uint8, stripe uint16, pos uint32, mask byte) {
		if len(data) == 0 || mask == 0 {
			t.Skip()
		}
		kk := int(k%8) + 1
		st := int(stripe%512) + 1
		blocks, stream := shardFixture(data, kk, st)
		if verified, err := readAll(stream, blocks); err != nil {
			t.Fatalf("pristine shard failed after %d blocks: %v", verified, err)
		}

		corrupt := make([]byte, len(stream))
		copy(corrupt, stream)
		var wantCorrupt bool
		switch pos % 3 {
		case 0: // flip one byte
			i := int(pos) % len(stream)
			corrupt[i] ^= mask
			wantCorrupt = true
		case 1: // truncate, leaving at least one byte missing
			corrupt = corrupt[:int(pos)%len(stream)]
			wantCorrupt = true
		default: // extend with trailing garbage
			corrupt = append(corrupt, mask)
			wantCorrupt = true
		}
		_, err := readAll(corrupt, blocks)
		if wantCorrupt && !errors.Is(err, erasure.ErrCorrupt) {
			t.Fatalf("mutation mode %d at %d not detected: err = %v", pos%3, pos, err)
		}
	})
}

// TestEveryBitFlipDetected flips every bit of a small framed shard, one at a
// time, and requires each flip to be detected.
func TestEveryBitFlipDetected(t *testing.T) {
	data := []byte("exhaustive bit flip coverage for bitrot")
	blocks, stream := shardFixture(data, 3, 8)
	for i := range stream {
		for bit := range 8 {
			corrupt := bytes.Clone(stream)
			corrupt[i] ^= 1 << bit
			_, err := readAll(corrupt, blocks)
			if !errors.Is(err, erasure.ErrCorrupt) {
				t.Fatalf("flip byte %d bit %d not detected: %v", i, bit, err)
			}
		}
	}
}
