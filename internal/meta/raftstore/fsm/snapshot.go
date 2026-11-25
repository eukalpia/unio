package fsm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/cockroachdb/pebble/v2"
	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
)

// snapshotMagic identifies the snapshot stream format. The stream is the
// magic followed by length-prefixed key and value pairs, in key order.
const snapshotMagic = "UNIOFSM1"

// maxFrameSize bounds one key or value in a snapshot stream.
const maxFrameSize = 64 << 20

// restoreBatchSize bounds the memory held by one restore batch.
const restoreBatchSize = 1 << 20

// snapshot is a point-in-time view of the state, captured by Snapshot.
type snapshot struct {
	snap *pebble.Snapshot
}

// Snapshot implements raft.FSM. It captures a consistent view of the state
// without blocking Apply for longer than creating a Pebble snapshot.
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	if err := f.failure(); err != nil {
		return nil, err
	}
	return &snapshot{snap: f.db.NewSnapshot()}, nil
}

// Persist implements raft.FSMSnapshot. It streams the captured state to sink.
func (s *snapshot) Persist(sink raft.SnapshotSink) error {
	if err := writeStream(sink, s.snap); err != nil {
		_ = sink.Cancel()
		return fmt.Errorf("fsm: persist snapshot: %w", err)
	}
	if err := sink.Close(); err != nil {
		return fmt.Errorf("fsm: close snapshot: %w", err)
	}
	return nil
}

// Release implements raft.FSMSnapshot.
func (s *snapshot) Release() {
	_ = s.snap.Close()
}

// writeStream writes every pair of snap to w in the snapshot format.
func writeStream(w io.Writer, snap *pebble.Snapshot) (err error) {
	bw := bufio.NewWriterSize(w, restoreBatchSize)
	if _, err := bw.WriteString(snapshotMagic); err != nil {
		return err
	}
	it, err := snap.NewIter(&pebble.IterOptions{})
	if err != nil {
		return err
	}
	defer func() { err = closeIter(it, err) }()
	var lenBuf [binary.MaxVarintLen64]byte
	for ok := it.First(); ok; ok = it.Next() {
		for _, frame := range [][]byte{it.Key(), it.Value()} {
			n := binary.PutUvarint(lenBuf[:], uint64(len(frame)))
			if _, err := bw.Write(lenBuf[:n]); err != nil {
				return err
			}
			if _, err := bw.Write(frame); err != nil {
				return err
			}
		}
	}
	if err := it.Error(); err != nil {
		return err
	}
	return bw.Flush()
}

// Restore implements raft.FSM. It replaces the whole state with the stream.
// A stream without the snapshot header is rejected before anything is
// cleared, so the state is unchanged. Any failure after that point leaves the
// state partially replaced, so it is fatal: the node must restart and install
// the snapshot again.
func (f *FSM) Restore(rc io.ReadCloser) error {
	br := bufio.NewReaderSize(rc, restoreBatchSize)
	if err := readHeader(br); err != nil {
		_ = rc.Close()
		return fmt.Errorf("fsm: restore: %w", err)
	}
	err := f.restore(br)
	if cerr := rc.Close(); cerr != nil && err == nil {
		err = fmt.Errorf("fsm: close snapshot stream: %w", cerr)
	}
	if err != nil {
		return f.fail(fmt.Errorf("fsm: restore: %w", err)).Err
	}
	return nil
}

// readHeader checks the snapshot magic at the start of a stream.
func readHeader(r io.Reader) error {
	magic := make([]byte, len(snapshotMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if string(magic) != snapshotMagic {
		return errors.New("unknown snapshot format")
	}
	return nil
}

// restore replaces the state with the frames that follow the header.
func (f *FSM) restore(br *bufio.Reader) error {
	if err := f.clearAll(); err != nil {
		return err
	}
	b := f.db.NewBatch()
	defer func() { _ = b.Close() }()
	for {
		key, err := readFrame(br)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		value, err := readFrame(br)
		if err != nil {
			return err
		}
		if err := b.Set(key, value, nil); err != nil {
			return fmt.Errorf("write pair: %w", err)
		}
		if b.Len() >= restoreBatchSize {
			if err := b.Commit(pebble.NoSync); err != nil {
				return fmt.Errorf("commit restore batch: %w", err)
			}
			b.Reset()
		}
	}
	if err := b.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("commit restore: %w", err)
	}
	applied, err := f.readUint64(keys.Meta(metaApplied))
	if err != nil {
		return err
	}
	f.applied.Store(applied)
	return nil
}

// clearAll deletes every key of the database.
func (f *FSM) clearAll() error {
	b := f.db.NewBatch()
	defer func() { _ = b.Close() }()
	if err := b.DeleteRange([]byte{0x00}, []byte{0xFF}, nil); err != nil {
		return fmt.Errorf("clear state: %w", err)
	}
	if err := b.Commit(pebble.NoSync); err != nil {
		return fmt.Errorf("clear state: %w", err)
	}
	return nil
}

// readFrame reads one length-prefixed frame. It returns io.EOF only when the
// stream ends exactly before a frame.
func readFrame(r *bufio.Reader) ([]byte, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("read frame length: %w", err)
	}
	if n > maxFrameSize {
		return nil, fmt.Errorf("frame of %d bytes exceeds the limit", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("read frame: %w", err)
	}
	return buf, nil
}
