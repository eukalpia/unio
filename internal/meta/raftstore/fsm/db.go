package fsm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
	"google.golang.org/protobuf/proto"

	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
)

// get returns a copy of the committed value of key and whether it exists.
// A present value may be empty, so found is reported separately.
func (f *FSM) get(key []byte) (val []byte, found bool, err error) {
	v, closer, err := f.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("fsm: get: %w", err)
	}
	out := bytes.Clone(v)
	if out == nil {
		out = []byte{}
	}
	if err := closer.Close(); err != nil {
		return nil, false, fmt.Errorf("fsm: get: close: %w", err)
	}
	return out, true, nil
}

// readUint64 returns the big-endian uint64 stored under key, or 0 if absent.
func (f *FSM) readUint64(key []byte) (uint64, error) {
	v, found, err := f.get(key)
	if err != nil || !found {
		return 0, err
	}
	if len(v) != 8 {
		return 0, fmt.Errorf("fsm: counter %q has %d bytes", key, len(v))
	}
	return binary.BigEndian.Uint64(v), nil
}

// setUint64 stores v big-endian under key in batch b.
func setUint64(b *pebble.Batch, key []byte, v uint64) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	if err := b.Set(key, buf[:], nil); err != nil {
		return fmt.Errorf("fsm: set counter: %w", err)
	}
	return nil
}

// iter opens an iterator over [lower, upper). A nil bound is unbounded.
func (f *FSM) iter(lower, upper []byte) (*pebble.Iterator, error) {
	it, err := f.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, fmt.Errorf("fsm: iterator: %w", err)
	}
	return it, nil
}

// prefixIter opens an iterator over all keys that start with prefix.
func (f *FSM) prefixIter(prefix []byte) (*pebble.Iterator, error) {
	return f.iter(prefix, keys.Successor(prefix))
}

// closeIter closes it and folds its error into err.
func closeIter(it *pebble.Iterator, err error) error {
	if cerr := it.Close(); cerr != nil && err == nil {
		return fmt.Errorf("fsm: close iterator: %w", cerr)
	}
	return err
}

// deletePrefix deletes every key under prefix in batch b. It returns the
// number of deleted keys.
func (f *FSM) deletePrefix(b *pebble.Batch, prefix []byte) (n int, err error) {
	it, err := f.prefixIter(prefix)
	if err != nil {
		return 0, err
	}
	defer func() { err = closeIter(it, err) }()
	for ok := it.First(); ok; ok = it.Next() {
		if err := b.Delete(it.Key(), nil); err != nil {
			return n, fmt.Errorf("fsm: delete: %w", err)
		}
		n++
	}
	if err := it.Error(); err != nil {
		return n, fmt.Errorf("fsm: iterate: %w", err)
	}
	return n, nil
}

// beUint64 decodes an 8-byte big-endian value. The caller checks the length.
func beUint64(b []byte) uint64 {
	return binary.BigEndian.Uint64(b)
}

// marshalStored encodes a record for the database. Object and upload records
// carry maps, and the vtprotobuf encoder writes map entries in Go's random
// iteration order, so two replicas holding the same state would store
// different bytes. The reflection encoder with Deterministic set sorts the
// map keys, which makes the stored bytes a function of the state alone.
func marshalStored(m proto.Message) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(m)
}
