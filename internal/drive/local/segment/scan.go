package segment

import (
	"errors"
	"fmt"
	"io"
)

// Record is one record found by Scan. When Scan verifies data, Data holds
// the record's bytes and is only valid until the callback returns; otherwise
// Data is nil.
type Record struct {
	Offset int64
	Header Header
	Data   []byte
}

// Scan walks the records of a segment of the given size from offset 0. It
// calls fn for each valid record in order and returns the offset just past
// the last valid record.
//
// Scan stops without error at the first record that is torn, has a bad
// header, or (when verifyData is set) has bad data. Everything after that
// point is not part of the segment. A non-nil error means an I/O failure or
// an error returned by fn.
func Scan(r io.ReaderAt, size int64, verifyData bool, fn func(Record) error) (int64, error) {
	var hdr [HeaderSize]byte
	var buf []byte
	var off int64
	for off+HeaderSize <= size {
		if err := readFull(r, hdr[:], off); err != nil {
			if errors.Is(err, errShort) {
				return off, nil
			}
			return off, fmt.Errorf("segment: scan at %d: %w", off, err)
		}
		h, err := DecodeHeader(hdr[:])
		if err != nil {
			return off, nil
		}
		end := off + RecordSize(int64(h.Length))
		if end > size {
			return off, nil // torn: the data is not all there
		}
		var data []byte
		if verifyData {
			if int64(cap(buf)) < int64(h.Length) {
				buf = make([]byte, h.Length)
			}
			data = buf[:h.Length]
			if err := readFull(r, data, off+HeaderSize); err != nil {
				if errors.Is(err, errShort) {
					return off, nil
				}
				return off, fmt.Errorf("segment: scan data at %d: %w", off, err)
			}
			if Checksum(data) != h.DataCRC {
				return off, nil
			}
		}
		if err := fn(Record{Offset: off, Header: h, Data: data}); err != nil {
			return off, err
		}
		off = end
	}
	return off, nil
}

// errShort reports that the reader ended before the requested bytes.
var errShort = errors.New("short read")

// readFull fills p from r at off. It returns errShort when r ends first and
// returns any other read error unchanged.
func readFull(r io.ReaderAt, p []byte, off int64) error {
	n, err := r.ReadAt(p, off)
	if n == len(p) {
		return nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		return errShort
	}
	return err
}
