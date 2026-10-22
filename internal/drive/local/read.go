package local

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/drive/local/internal/faults"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// maxReadAttempts bounds re-lookups when a segment is retired between the
// index lookup and the open. The index then names the new location.
const maxReadAttempts = 4

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// errRetired reports that a segment was retired while being opened.
var errRetired = errors.New("local: segment retired")

// ReadShard implements drive.Drive. Reads of a whole packed shard verify its
// data checksum, which is checked when the reader reaches the end. Range
// reads verify the record header only.
func (d *Drive) ReadShard(ctx context.Context, id ids.ShardID, offset, length int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return nil, drive.ErrClosed
	}
	for attempt := 1; ; attempt++ {
		loc, err := d.lookup(id)
		if err != nil {
			return nil, err
		}
		end, err := resolveRange(loc.Length, offset, length)
		if err != nil {
			return nil, fmt.Errorf("local: shard %s: %w", id, err)
		}
		if loc.Kind == index.KindBlob {
			return d.openBlob(id, loc, offset, end)
		}
		rc, err := d.openPacked(id, loc, offset, end)
		if errors.Is(err, errRetired) && attempt < maxReadAttempts {
			continue
		}
		return rc, err
	}
}

// openPacked opens bytes [offset, end) of a packed shard. It holds a reference
// to the segment until the reader is closed.
func (d *Drive) openPacked(id ids.ShardID, loc index.Location, offset, end int64) (io.ReadCloser, error) {
	ref, ok := d.segs.acquire(loc.Segment)
	if !ok {
		return nil, errRetired
	}
	release := func() error { return d.segs.release(ref) }
	h, err := ref.file.ReadHeader(loc.Offset)
	if err != nil {
		_ = release()
		return nil, corrupt(id, err)
	}
	if h.ShardID != id || int64(h.Length) != loc.Length || h.Flags&segment.FlagTombstone != 0 {
		_ = release()
		return nil, corrupt(id, fmt.Errorf("record at %d does not match the index", loc.Offset))
	}
	src := io.NewSectionReader(ref.file, loc.Offset+segment.HeaderSize+offset, end-offset)
	var r io.Reader = src
	if offset == 0 && end == loc.Length {
		r = &verifyReader{
			r:     src,
			id:    id,
			want:  h.DataCRC,
			left:  loc.Length,
			hooks: d.opts.hooks,
		}
	}
	return &closeReader{Reader: r, release: release}, nil
}

// openBlob opens bytes [offset, end) of a blob file. Blobs carry no checksum of
// their own; the erasure layer verifies their content.
func (d *Drive) openBlob(id ids.ShardID, loc index.Location, offset, end int64) (io.ReadCloser, error) {
	f, err := os.Open(d.blobs.Path(id))
	if errors.Is(err, os.ErrNotExist) {
		// A concurrent delete unlinks the file after the index changes.
		if _, lerr := d.lookup(id); errors.Is(lerr, drive.ErrShardNotFound) {
			return nil, drive.ErrShardNotFound
		}
		return nil, corrupt(id, errors.New("blob file missing"))
	}
	if err != nil {
		return nil, fmt.Errorf("local: shard %s: open blob: %w", id, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("local: shard %s: stat blob: %w", id, err)
	}
	if st.Size() != loc.Length {
		_ = f.Close()
		return nil, corrupt(id, fmt.Errorf("blob is %d bytes, index says %d", st.Size(), loc.Length))
	}
	return &closeReader{
		Reader:  io.NewSectionReader(f, offset, end-offset),
		release: f.Close,
	}, nil
}

// verifyReader checks the data checksum of a whole packed shard. The check
// happens at the end of the stream, because only then has every byte been
// seen. Bytes are passed through before that point, so callers that stop early
// are not verified.
type verifyReader struct {
	r     io.Reader
	id    ids.ShardID
	want  uint32
	crc   uint32
	left  int64
	hooks *faults.Hooks
}

func (v *verifyReader) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	v.hooks.Corrupt(faults.ReadData, p[:n])
	v.crc = crc32.Update(v.crc, castagnoli, p[:n])
	v.left -= int64(n)
	if errors.Is(err, io.EOF) && (v.left != 0 || v.crc != v.want) {
		return n, corrupt(v.id, errors.New("data checksum mismatch"))
	}
	return n, err
}

// corrupt wraps a verification failure for shard id as drive.ErrCorrupt.
func corrupt(id ids.ShardID, cause error) error {
	return fmt.Errorf("local: shard %s: %w: %w", id, drive.ErrCorrupt, cause)
}

// resolveRange validates a read range against a shard of size bytes and
// returns the exclusive end offset. A negative length reads to the end.
func resolveRange(size, offset, length int64) (int64, error) {
	if offset < 0 || offset > size {
		return 0, fmt.Errorf("offset %d outside shard of %d bytes", offset, size)
	}
	if length < 0 {
		return size, nil
	}
	if length > size-offset {
		return 0, fmt.Errorf("range [%d, %d) outside shard of %d bytes", offset, offset+length, size)
	}
	return offset + length, nil
}
