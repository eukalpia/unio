package segment

import (
	"bytes"
	"errors"
	"io"
)

// Range is a half-open byte range [Start, End) within a segment.
type Range struct {
	Start, End int64
}

// ResyncResult summarises a resynchronising scan.
type ResyncResult struct {
	// Records is the number of valid records yielded.
	Records int
	// End is the offset just past the last valid record, or 0 if there is none.
	End int64
	// Damaged lists, in order, the ranges between valid records that contain
	// no valid record start. Each one is followed by a valid record, so each
	// lies inside the segment.
	Damaged []Range
	// TailBytes counts the bytes after End. A torn append or an uncommitted
	// write leaves such bytes. They are not counted as damage.
	TailBytes int64
}

// resyncWindow is how many bytes are read at a time while searching for the
// next record start.
const resyncWindow = 64 << 10

// Resync scans the records of a segment of the given size. Every candidate is
// verified in full: header checksum, bounds, and data checksum. Unlike Scan,
// Resync does not stop at damage. After an invalid region it searches forward
// for the next offset where a valid record starts, and it resumes there. Bytes
// inside a skipped region are never yielded, even when they look like a
// header.
//
// The callback's Record.Data is valid only until it returns. A non-nil error
// means an I/O failure or an error returned by fn.
//
// The checksums detect damage. They do not make a record authentic: a writer
// who controls a payload can embed a complete, valid record in it. Such a
// record is yielded if the bytes around it are damaged.
func Resync(r io.ReaderAt, size int64, fn func(Record) error) (ResyncResult, error) {
	var res ResyncResult
	var body []byte
	win := make([]byte, resyncWindow)
	var lastEnd int64
	pos := int64(0)
	for pos+HeaderSize <= size {
		h, data, ok, err := validRecordAt(r, pos, size, &body)
		if err != nil {
			return res, err
		}
		if !ok {
			next, found, err := nextRecordMagic(r, pos+1, size, win)
			if err != nil {
				return res, err
			}
			if !found {
				break
			}
			pos = next
			continue
		}
		if pos > lastEnd {
			res.Damaged = append(res.Damaged, Range{Start: lastEnd, End: pos})
		}
		if err := fn(Record{Offset: pos, Header: h, Data: data}); err != nil {
			return res, err
		}
		res.Records++
		pos += RecordSize(int64(len(data)))
		lastEnd = pos
		res.End = pos
	}
	res.TailBytes = size - lastEnd
	return res, nil
}

// validRecordAt reads the candidate record at pos and reports whether it is
// complete and both its checksums verify. Damage is not an error: it reports
// false. I/O failures are returned.
func validRecordAt(r io.ReaderAt, pos, size int64, buf *[]byte) (Header, []byte, bool, error) {
	var hdr [HeaderSize]byte
	if err := readFull(r, hdr[:], pos); err != nil {
		if errors.Is(err, errShort) {
			return Header{}, nil, false, nil
		}
		return Header{}, nil, false, err
	}
	h, err := DecodeHeader(hdr[:])
	if err != nil {
		return Header{}, nil, false, nil
	}
	n, ok := h.DataLength()
	if !ok || pos+RecordSize(n) > size {
		return Header{}, nil, false, nil
	}
	// The bounds check above caps the length at the segment size, so the
	// allocation below is bounded by the file, not by the header.
	if int64(cap(*buf)) < n {
		*buf = make([]byte, n)
	}
	data := (*buf)[:n]
	if err := readFull(r, data, pos+HeaderSize); err != nil {
		if errors.Is(err, errShort) {
			return Header{}, nil, false, nil
		}
		return Header{}, nil, false, err
	}
	if Checksum(data) != h.DataCRC {
		return Header{}, nil, false, nil
	}
	return h, data, true, nil
}

// nextRecordMagic returns the offset of the first record magic at or after
// from. It reads the segment in windows and uses win as the window buffer. It
// overlaps the windows by len(magic)-1 bytes, so a magic that straddles a
// window edge is still found.
func nextRecordMagic(r io.ReaderAt, from, size int64, win []byte) (int64, bool, error) {
	for p := from; p+int64(len(magic)) <= size; {
		n := min(int64(len(win)), size-p)
		m, err := r.ReadAt(win[:n], p)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, false, err
		}
		if i := bytes.Index(win[:m], magic[:]); i >= 0 {
			return p + int64(i), true, nil
		}
		if int64(m) < n {
			return 0, false, nil
		}
		p += n - int64(len(magic)) + 1
	}
	return 0, false, nil
}
