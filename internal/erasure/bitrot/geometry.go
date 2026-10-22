// Package bitrot frames erasure shards with per-block checksums and provides
// the offset arithmetic that maps object byte ranges onto framed shard
// ranges.
//
// A shard is a sequence of blocks. Block i holds the data of stripe i and is
// stored as [16-byte xxh3-128 digest of the block data][block data]. Every
// block is ceil(stripeSize/k) bytes except the last block of a shard, which
// covers the final, possibly shorter, stripe.
package bitrot

import (
	"errors"
	"fmt"
)

// HashSize is the length of the xxh3-128 digest that prefixes every block.
const HashSize = 16

// ErrRange is returned by Span for arguments that do not describe a valid
// byte range of an object.
var ErrRange = errors.New("bitrot: invalid object range or geometry")

// Range describes which framed bytes of each shard serve an object byte
// range.
type Range struct {
	// FirstStripe and EndStripe bound the stripes touched, as [First, End).
	FirstStripe, EndStripe int64
	// ShardOffset and ShardLength are the framed byte range of one shard
	// that covers every touched block.
	ShardOffset, ShardLength int64
	// Skip is the number of data bytes at the start of FirstStripe that
	// precede the requested range.
	Skip int64
}

// ShardBlockSize returns the data bytes of a full block: ceil(stripeSize/k).
func ShardBlockSize(stripeSize, k int) int {
	return (stripeSize + k - 1) / k
}

// Stripes returns the number of stripes that hold dataSize bytes.
func Stripes(dataSize int64, stripeSize int) int64 {
	s := int64(stripeSize)
	return (dataSize + s - 1) / s
}

// StripeLen returns the data bytes covered by stripe s of an object of
// dataSize bytes.
func StripeLen(dataSize int64, stripeSize int, s int64) int64 {
	rem := dataSize - s*int64(stripeSize)
	if rem > int64(stripeSize) {
		return int64(stripeSize)
	}
	return rem
}

// BlockLen returns the data bytes of the block of stripe s in each shard.
func BlockLen(dataSize int64, k, stripeSize int, s int64) int {
	n := StripeLen(dataSize, stripeSize, s)
	return int((n + int64(k) - 1) / int64(k))
}

// blockOffset returns the framed offset within a shard of the block of
// stripe s. Every earlier block is a full block.
func blockOffset(k, stripeSize int, s int64) int64 {
	return s * int64(HashSize+ShardBlockSize(stripeSize, k))
}

// blockEnd returns the framed offset just past the block of stripe s.
func blockEnd(dataSize int64, k, stripeSize int, s int64) int64 {
	return blockOffset(k, stripeSize, s) + HashSize + int64(BlockLen(dataSize, k, stripeSize, s))
}

// ShardSize returns the framed size of one shard of an object of dataSize
// bytes.
func ShardSize(dataSize int64, k, stripeSize int) int64 {
	n := Stripes(dataSize, stripeSize)
	if n == 0 {
		return 0
	}
	return blockEnd(dataSize, k, stripeSize, n-1)
}

// Span maps the object range [offset, offset+length) to the stripes that
// serve it and to the framed range of each shard that must be read.
// A zero length yields the zero Range.
func Span(dataSize int64, k, stripeSize int, offset, length int64) (Range, error) {
	if err := validate(dataSize, k, stripeSize, offset, length); err != nil {
		return Range{}, err
	}
	if length == 0 {
		return Range{}, nil
	}
	s := int64(stripeSize)
	first := offset / s
	end := (offset+length-1)/s + 1
	return Range{
		FirstStripe: first,
		EndStripe:   end,
		ShardOffset: blockOffset(k, stripeSize, first),
		ShardLength: blockEnd(dataSize, k, stripeSize, end-1) - blockOffset(k, stripeSize, first),
		Skip:        offset - first*s,
	}, nil
}

func validate(dataSize int64, k, stripeSize int, offset, length int64) error {
	switch {
	case dataSize < 0, k < 1, stripeSize < 1, offset < 0, length < 0:
		return fmt.Errorf("%w: size=%d k=%d stripe=%d offset=%d length=%d",
			ErrRange, dataSize, k, stripeSize, offset, length)
	case offset > dataSize || length > dataSize-offset:
		return fmt.Errorf("%w: [%d,%d) outside %d bytes", ErrRange, offset, offset+length, dataSize)
	}
	return nil
}

// StripeRange returns the framed byte range of one shard that covers stripes
// [first, end) of an object of dataSize bytes.
func StripeRange(dataSize int64, k, stripeSize int, first, end int64) (offset, length int64) {
	if end <= first {
		return blockOffset(k, stripeSize, first), 0
	}
	offset = blockOffset(k, stripeSize, first)
	return offset, blockEnd(dataSize, k, stripeSize, end-1) - offset
}
