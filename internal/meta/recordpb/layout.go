package recordpb

import (
	"bytes"
	"fmt"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Layout conversions. Shard counts and the stripe size must be non-negative
// and fit in uint32. Drive entries are one per shard. A zero DriveID is a
// zero-length entry (a shard that was not written), and a non-zero DriveID is
// 16 bytes. Decoding accepts only those two lengths. An empty Drives list is
// equivalent to nil, and decodes to nil.

// LayoutToProto converts l to its message. It returns an error for negative
// or out-of-range shard counts and stripe size.
func LayoutToProto(l erasure.Layout) (*uniov1.ErasureLayout, error) {
	p, err := layoutToProto(l)
	return p, prefixErr(err)
}

// LayoutFromProto converts p to a layout. It returns an error for a nil
// message or a drive id whose length is neither 0 nor 16.
func LayoutFromProto(p *uniov1.ErasureLayout) (erasure.Layout, error) {
	l, err := layoutFromProto(p)
	return l, prefixErr(err)
}

func layoutToProto(l erasure.Layout) (*uniov1.ErasureLayout, error) {
	dataShards, err := uint32Of("data shards", l.DataShards)
	if err != nil {
		return nil, err
	}
	parityShards, err := uint32Of("parity shards", l.ParityShards)
	if err != nil {
		return nil, err
	}
	stripeSize, err := uint32Of("stripe size", l.StripeSize)
	if err != nil {
		return nil, err
	}
	var drives [][]byte
	if len(l.Drives) > 0 {
		drives = make([][]byte, len(l.Drives))
		for i, d := range l.Drives {
			drives[i] = driveIDBytes(d)
		}
	}
	return &uniov1.ErasureLayout{
		Set:          uint32(l.Set),
		DataShards:   dataShards,
		ParityShards: parityShards,
		StripeSize:   stripeSize,
		Drives:       drives,
	}, nil
}

func layoutFromProto(p *uniov1.ErasureLayout) (erasure.Layout, error) {
	if p == nil {
		return erasure.Layout{}, errNil("erasure layout")
	}
	dataShards, err := intOf("data shards", p.GetDataShards())
	if err != nil {
		return erasure.Layout{}, err
	}
	parityShards, err := intOf("parity shards", p.GetParityShards())
	if err != nil {
		return erasure.Layout{}, err
	}
	stripeSize, err := intOf("stripe size", p.GetStripeSize())
	if err != nil {
		return erasure.Layout{}, err
	}
	var drives []ids.DriveID
	if n := len(p.GetDrives()); n > 0 {
		drives = make([]ids.DriveID, n)
		for i, b := range p.GetDrives() {
			d, err := driveIDFromBytes(b)
			if err != nil {
				return erasure.Layout{}, fmt.Errorf("drive %d: %w", i, err)
			}
			drives[i] = d
		}
	}
	return erasure.Layout{
		Set:          ids.SetID(p.GetSet()),
		DataShards:   dataShards,
		ParityShards: parityShards,
		StripeSize:   stripeSize,
		Drives:       drives,
	}, nil
}

// layoutFromOptional decodes a nested layout. An absent message decodes as
// the zero layout.
func layoutFromOptional(p *uniov1.ErasureLayout) (erasure.Layout, error) {
	if p == nil {
		return erasure.Layout{}, nil
	}
	return layoutFromProto(p)
}

// driveIDBytes returns the wire form of d: no bytes for the zero DriveID,
// 16 bytes otherwise. The result is never nil, so the entry is always present.
func driveIDBytes(d ids.DriveID) []byte {
	if d.IsZero() {
		return []byte{}
	}
	return bytes.Clone(d[:])
}

// driveIDFromBytes decodes a drive entry. Zero-length gives the zero DriveID.
func driveIDFromBytes(b []byte) (ids.DriveID, error) {
	var d ids.DriveID
	if len(b) == 0 {
		return d, nil
	}
	if len(b) != len(d) {
		return d, fmt.Errorf("drive id must be 0 or %d bytes, got %d", len(d), len(b))
	}
	copy(d[:], b)
	return d, nil
}
