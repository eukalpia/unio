package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/segment"
	"github.com/eukalpia/unio/internal/ids"
)

// WriteShard implements drive.Drive. Shards up to PackedMaxShardSize are
// appended to a segment; larger shards become blob files. The call returns
// after the shard is durable under the drive's sync policy.
func (d *Drive) WriteShard(ctx context.Context, id ids.ShardID, r io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("local: shard %s: negative size %d", id, size)
	}
	if err := d.checkAbsent(id); err != nil {
		return err
	}
	if size <= d.opts.packedMax {
		return d.writePacked(ctx, id, r, size)
	}
	return d.writeBlob(ctx, id, r, size)
}

// writePacked reads the shard into a pooled buffer, encodes its record in
// place and hands the record to the committer.
func (d *Drive) writePacked(ctx context.Context, id ids.ShardID, r io.Reader, size int64) error {
	buf, err := d.pools.forData(size).Get(ctx)
	if err != nil {
		return err
	}
	defer buf.Release()

	full := buf.Bytes()
	data := full[segment.HeaderSize : segment.HeaderSize+size]
	if err := readExact(r, data); err != nil {
		return fmt.Errorf("local: shard %s: %w", id, err)
	}
	h := segment.Header{ShardID: id, Length: uint64(size), DataCRC: segment.Checksum(data)}
	h.AppendHeader(full[:0])

	req := newRequest(reqPut, id)
	req.rec = full[:segment.RecordSize(size)]
	req.created = time.Now().UnixNano()
	return d.enqueue(ctx, req)
}

// checkAbsent fails if the drive is closed or already holds id. It is an early
// check: the committer repeats it authoritatively, so a race with a concurrent
// writer still yields exactly one winner.
func (d *Drive) checkAbsent(id ids.ShardID) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return drive.ErrClosed
	}
	if _, err := d.lookup(id); err == nil {
		return drive.ErrShardExists
	} else if !errors.Is(err, drive.ErrShardNotFound) {
		return err
	}
	return nil
}

// enqueue hands a request to the committer and waits for its answer. It holds
// the drive lock only for that exchange, so a client that is slow to send its
// payload cannot delay Close. Closing the drive makes the request fail.
func (d *Drive) enqueue(ctx context.Context, r *request) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return drive.ErrClosed
	}
	return d.submit(ctx, r)
}

// writeBlob writes the shard to a temporary file and asks the committer to
// move it into place. The file is removed if the shard is not placed.
func (d *Drive) writeBlob(ctx context.Context, id ids.ShardID, r io.Reader, size int64) error {
	tmp, err := d.blobs.NewTemp()
	if err != nil {
		return err
	}
	if err := d.blobs.Write(ctx, tmp, r, size); err != nil {
		return fmt.Errorf("local: shard %s: %w", id, err)
	}
	req := newRequest(reqBlob, id)
	req.tmp = tmp
	req.final = d.blobs.Path(id)
	req.size = size
	req.created = time.Now().UnixNano()
	err = d.enqueue(ctx, req)
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// readExact fills dst from r and fails unless r holds exactly len(dst) bytes.
func readExact(r io.Reader, dst []byte) error {
	if _, err := io.ReadFull(r, dst); err != nil {
		return fmt.Errorf("reading data: %w", err)
	}
	var one [1]byte
	n, err := r.Read(one[:])
	if n > 0 {
		return fmt.Errorf("reader holds more than %d bytes", len(dst))
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("reading data: %w", err)
	}
	return nil
}
