package recordpb

import (
	"bytes"
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Part conversions. Number must fit in uint32 and Size must be non-negative.
// Data is always exactly 16 bytes on the wire, including the zero DataID, and
// any other length on decode is an error. Absent nested messages decode as
// zero values (see doc.go).

// PartToProto converts p to its message. It returns an error for a negative
// or out-of-range Number, a negative Size, an invalid checksum or layout, or a
// time outside the nanosecond range.
func PartToProto(p meta.PartRecord) (*uniov1.PartRecord, error) {
	m, err := partToProto(p)
	return m, prefixErr(err)
}

// PartFromProto converts p to a part record. It returns an error for a nil
// message, a negative size, a Data field that is not 16 bytes, or an invalid
// checksum or layout.
func PartFromProto(p *uniov1.PartRecord) (meta.PartRecord, error) {
	r, err := partFromProto(p)
	return r, prefixErr(err)
}

func partToProto(p meta.PartRecord) (*uniov1.PartRecord, error) {
	number, err := uint32Of("part number", p.Number)
	if err != nil {
		return nil, err
	}
	if err := nonNegative("part size", p.Size); err != nil {
		return nil, err
	}
	checksum, err := checksumToProto(p.Checksum)
	if err != nil {
		return nil, fmt.Errorf("part %d checksum: %w", p.Number, err)
	}
	modTime, err := encodeTime("part mod time", p.ModTime)
	if err != nil {
		return nil, err
	}
	layout, err := layoutToProto(p.Layout)
	if err != nil {
		return nil, fmt.Errorf("part %d layout: %w", p.Number, err)
	}
	return &uniov1.PartRecord{
		Number:          number,
		Size:            p.Size,
		Etag:            p.ETag,
		Checksum:        checksum,
		ModTimeUnixNano: modTime,
		Data:            dataIDBytes(p.Data),
		Layout:          layout,
	}, nil
}

func partFromProto(p *uniov1.PartRecord) (meta.PartRecord, error) {
	if p == nil {
		return meta.PartRecord{}, errNil("part")
	}
	number, err := intOf("part number", p.GetNumber())
	if err != nil {
		return meta.PartRecord{}, err
	}
	if err := nonNegative("part size", p.GetSize()); err != nil {
		return meta.PartRecord{}, err
	}
	checksum, err := checksumFromOptional(p.GetChecksum())
	if err != nil {
		return meta.PartRecord{}, fmt.Errorf("part %d checksum: %w", number, err)
	}
	data, err := dataIDFromBytes(p.GetData())
	if err != nil {
		return meta.PartRecord{}, fmt.Errorf("part %d data: %w", number, err)
	}
	layout, err := layoutFromOptional(p.GetLayout())
	if err != nil {
		return meta.PartRecord{}, fmt.Errorf("part %d layout: %w", number, err)
	}
	return meta.PartRecord{
		Number:   number,
		Size:     p.GetSize(),
		ETag:     p.GetEtag(),
		Checksum: checksum,
		ModTime:  TimeFromNano(p.GetModTimeUnixNano()),
		Data:     data,
		Layout:   layout,
	}, nil
}

// dataIDBytes returns the 16-byte wire form of id.
func dataIDBytes(id ids.DataID) []byte {
	return bytes.Clone(id[:])
}

// dataIDFromBytes decodes the 16-byte wire form of a DataID.
func dataIDFromBytes(b []byte) (ids.DataID, error) {
	var id ids.DataID
	if len(b) != len(id) {
		return id, fmt.Errorf("data id must be %d bytes, got %d", len(id), len(b))
	}
	copy(id[:], b)
	return id, nil
}
