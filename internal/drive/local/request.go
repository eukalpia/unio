package local

import (
	"context"
	"errors"

	"github.com/eukalpia/unio/internal/drive/local/index"
	"github.com/eukalpia/unio/internal/ids"
)

// reqKind identifies a mutation handled by the committer.
type reqKind uint8

const (
	// reqPut appends a packed record for a new shard.
	reqPut reqKind = iota + 1
	// reqBlob places an already written blob file for a new shard.
	reqBlob
	// reqDelete removes a shard: a tombstone for packed shards, an unlink for
	// blobs.
	reqDelete
	// reqCopy moves a live packed record to the active segment (compaction).
	reqCopy
	// reqCopyTomb carries a tombstone forward during compaction.
	reqCopyTomb
	// reqRetire removes a segment that holds no live records (compaction).
	reqRetire
)

// errSkipped reports that a compaction request was superseded and had nothing
// to do.
var errSkipped = errors.New("local: request superseded")

// request is one mutation for the committer. The committer answers each
// request exactly once on reply, which has room for the answer so that the
// committer never blocks.
type request struct {
	kind reqKind
	id   ids.ShardID

	// rec holds the encoded record for reqPut, reqCopy and reqCopyTomb. The
	// caller owns its buffer until reply.
	rec []byte
	// created is the write time in Unix nanoseconds (reqPut and reqBlob).
	created int64
	// size is the data length (reqBlob).
	size int64
	// tmp and final are the staging and final paths of a blob. For reqDelete
	// of a blob, final is the file to unlink.
	tmp, final string
	// from is the expected current location (reqCopy), or the location of a
	// deleted shard (reqDelete, recorded when the delete is staged).
	from index.Location
	// origin is the segment that held the record a tombstone deletes.
	origin uint64
	// seg is the segment a compaction request works on.
	seg uint64

	reply chan error
}

func newRequest(kind reqKind, id ids.ShardID) *request {
	return &request{kind: kind, id: id, reply: make(chan error, 1)}
}

// submit hands r to the committer and waits for its answer. Once the
// committer has the request it always answers, so the wait does not honour
// ctx: a write that reached the committer completes or fails on its own.
func (d *Drive) submit(ctx context.Context, r *request) error {
	select {
	case d.reqs <- r:
	case <-ctx.Done():
		return ctx.Err()
	}
	return <-r.reply
}

// signal asks compaction to run a pass soon. It never blocks.
func (d *Drive) signal() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}
