package segment

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/eukalpia/unio/internal/drive/local/internal/syncerr"
)

// File is one segment file. Appends (Append, SetEnd, Truncate) are not safe
// for concurrent use: the drive's single committer performs them. Reads
// (ReadAt, ReadHeader, ReadRecord) are safe to run concurrently with each
// other.
type File struct {
	f        *os.File
	path     string
	capacity int64
	end      int64
}

// Create makes a new, empty segment file of the given capacity. Where the
// platform supports it, the file's space is preallocated so that appends do
// not allocate. The caller is responsible for syncing the parent directory.
func Create(path string, capacity int64) (*File, error) {
	if capacity <= HeaderSize {
		return nil, fmt.Errorf("segment: capacity %d too small", capacity)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("segment: create %s: %w", path, err)
	}
	if err := preallocate(f, capacity); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("segment: preallocate %s: %w", path, err)
	}
	return &File{f: f, path: path, capacity: capacity}, nil
}

// Open opens an existing segment file whose committed data ends at end. The
// file may be longer than end (preallocated, or holding uncommitted bytes),
// so the committed end cannot be inferred from the file size.
func Open(path string, capacity, end int64) (*File, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("segment: open %s: %w", path, err)
	}
	return &File{f: f, path: path, capacity: capacity, end: end}, nil
}

// Path returns the file path.
func (f *File) Path() string { return f.path }

// Capacity returns the maximum end offset accepted by Append.
func (f *File) Capacity() int64 { return f.capacity }

// End returns the offset where the next record will be appended. Bytes at or
// after End are not part of the segment.
func (f *File) End() int64 { return f.end }

// SetEnd sets the append offset. It is used on open and after a failed
// commit to discard bytes that were never committed.
func (f *File) SetEnd(end int64) { f.end = end }

// Append writes rec at End and advances End. It returns the offset of the
// record. On error End is unchanged, so a later append overwrites any partial
// bytes.
func (f *File) Append(rec []byte) (int64, error) {
	if f.end+int64(len(rec)) > f.capacity {
		return 0, ErrFull
	}
	off := f.end
	if _, err := f.f.WriteAt(rec, off); err != nil {
		return 0, fmt.Errorf("segment: append to %s: %w", f.path, err)
	}
	f.end += int64(len(rec))
	return off, nil
}

// Sync flushes appended data to stable storage.
func (f *File) Sync() error {
	return syncerr.Wrap("fdatasync", f.path, syncData(f.f))
}

// Truncate changes the file size.
func (f *File) Truncate(size int64) error {
	if err := f.f.Truncate(size); err != nil {
		return fmt.Errorf("segment: truncate %s: %w", f.path, err)
	}
	return nil
}

// Size returns the current file size.
func (f *File) Size() (int64, error) {
	info, err := f.f.Stat()
	if err != nil {
		return 0, fmt.Errorf("segment: stat %s: %w", f.path, err)
	}
	return info.Size(), nil
}

// ReadAt implements io.ReaderAt.
func (f *File) ReadAt(p []byte, off int64) (int, error) {
	return f.f.ReadAt(p, off)
}

// ReadHeader reads and verifies the header of the record at off.
func (f *File) ReadHeader(off int64) (Header, error) {
	var buf [HeaderSize]byte
	n, err := f.f.ReadAt(buf[:], off)
	if n < HeaderSize {
		if err == nil || errors.Is(err, io.EOF) {
			return Header{}, fmt.Errorf("%w: header at %d is truncated", ErrInvalid, off)
		}
		return Header{}, fmt.Errorf("segment: read header at %d in %s: %w", off, f.path, err)
	}
	return DecodeHeader(buf[:])
}

// ReadRecord reads the record at off into dst, verifying its header and data
// checksums. dst must hold at least RecordSize(length) bytes. The returned
// slice holds the complete record and aliases dst.
func (f *File) ReadRecord(off int64, dst []byte) (Header, []byte, error) {
	h, err := f.ReadHeader(off)
	if err != nil {
		return Header{}, nil, err
	}
	total := RecordSize(int64(h.Length))
	if int64(len(dst)) < total {
		return Header{}, nil, fmt.Errorf("segment: buffer of %d bytes too small for %d", len(dst), total)
	}
	rec := dst[:total]
	n, err := f.f.ReadAt(rec[HeaderSize:], off+HeaderSize)
	if n < len(rec)-HeaderSize {
		if err == nil || errors.Is(err, io.EOF) {
			return Header{}, nil, fmt.Errorf("%w: data at %d is truncated", ErrInvalid, off)
		}
		return Header{}, nil, fmt.Errorf("segment: read data at %d in %s: %w", off, f.path, err)
	}
	if Checksum(rec[HeaderSize:]) != h.DataCRC {
		return Header{}, nil, fmt.Errorf("%w: data checksum mismatch at %d", ErrInvalid, off)
	}
	h.AppendHeader(rec[:0])
	return h, rec, nil
}

// Close closes the file.
func (f *File) Close() error {
	if err := f.f.Close(); err != nil {
		return fmt.Errorf("segment: close %s: %w", f.path, err)
	}
	return nil
}
