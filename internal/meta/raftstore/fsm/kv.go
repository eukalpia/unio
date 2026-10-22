package fsm

import (
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble/v2"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// kvRecord reads a system key-value entry. The stored value is an 8-byte
// big-endian revision followed by the value. A missing key has revision 0.
func (f *FSM) kvRecord(key string) (value []byte, revision uint64, found bool, err error) {
	v, found, err := f.get(keys.KV([]byte(key)))
	if err != nil || !found {
		return nil, 0, false, err
	}
	if len(v) < 8 {
		return nil, 0, false, fmt.Errorf("fsm: kv %q record has %d bytes", key, len(v))
	}
	return v[8:], binary.BigEndian.Uint64(v[:8]), true, nil
}

// kvPut writes a key-value entry if its revision matches. The new revision
// is the Raft index of the command, which is unique and increases.
func (f *FSM) kvPut(b *pebble.Batch, c *pb.KVPut, index uint64) (*pb.ApplyResult, error) {
	if err := validKVKey(c.Key); err != nil {
		return nil, err
	}
	if err := validKVValue(c.Value); err != nil {
		return nil, err
	}
	_, cur, _, err := f.kvRecord(c.Key)
	if err != nil {
		return nil, err
	}
	if c.ExpectRevision != meta.AnyRevision && c.ExpectRevision != cur {
		return nil, meta.ErrRevisionMismatch
	}
	v := make([]byte, 8+len(c.Value))
	binary.BigEndian.PutUint64(v[:8], index)
	copy(v[8:], c.Value)
	if err := b.Set(keys.KV([]byte(c.Key)), v, nil); err != nil {
		return nil, fmt.Errorf("fsm: write kv: %w", err)
	}
	return &pb.ApplyResult{Revision: index}, nil
}

// kvDelete removes a key-value entry under the same revision rules. Deleting
// a missing key succeeds when the expected revision is 0 or AnyRevision.
func (f *FSM) kvDelete(b *pebble.Batch, c *pb.KVDelete) (*pb.ApplyResult, error) {
	if err := validKVKey(c.Key); err != nil {
		return nil, err
	}
	_, cur, found, err := f.kvRecord(c.Key)
	if err != nil {
		return nil, err
	}
	if c.ExpectRevision != meta.AnyRevision && c.ExpectRevision != cur {
		return nil, meta.ErrRevisionMismatch
	}
	if found {
		if err := b.Delete(keys.KV([]byte(c.Key)), nil); err != nil {
			return nil, fmt.Errorf("fsm: delete kv: %w", err)
		}
	}
	return &pb.ApplyResult{}, nil
}
