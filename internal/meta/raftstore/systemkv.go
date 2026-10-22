package raftstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Get implements meta.SystemKV. It is linearizable, like every operation of
// the store.
func (s *Store) Get(ctx context.Context, key string) ([]byte, uint64, error) {
	q := &pb.ReadQuery{Query: &pb.ReadQuery_KvGet{KvGet: &pb.KVGetQuery{Key: key}}}
	res, err := s.router.Read(ctx, ids.SystemPartition, q)
	if err != nil {
		return nil, 0, err
	}
	entries := res.GetKv().GetEntries()
	if len(entries) != 1 {
		return nil, 0, fmt.Errorf("raftstore: kv read returned %d entries", len(entries))
	}
	return entries[0].GetValue(), entries[0].GetRevision(), nil
}

// Put implements meta.SystemKV. The revision of the new value is the Raft
// index of the command.
func (s *Store) Put(ctx context.Context, key string, value []byte, expectRevision uint64) (uint64, error) {
	if len(value) > meta.MaxSystemValueSize {
		return 0, meta.ErrValueTooLarge
	}
	cmd := &pb.Command{Cmd: &pb.Command_KvPut{KvPut: &pb.KVPut{
		Key:            key,
		Value:          value,
		ExpectRevision: expectRevision,
	}}}
	res, err := s.router.Propose(ctx, ids.SystemPartition, cmd)
	if err != nil {
		return 0, err
	}
	return res.GetRevision(), nil
}

// Delete implements meta.SystemKV.
func (s *Store) Delete(ctx context.Context, key string, expectRevision uint64) error {
	cmd := &pb.Command{Cmd: &pb.Command_KvDelete{KvDelete: &pb.KVDelete{
		Key:            key,
		ExpectRevision: expectRevision,
	}}}
	_, err := s.router.Propose(ctx, ids.SystemPartition, cmd)
	return err
}

// List implements meta.SystemKV. Entries are returned in key order.
func (s *Store) List(ctx context.Context, prefix string) ([]meta.KV, error) {
	q := &pb.ReadQuery{Query: &pb.ReadQuery_KvList{KvList: &pb.KVListQuery{Prefix: prefix}}}
	res, err := s.router.Read(ctx, ids.SystemPartition, q)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errors.New("raftstore: kv list returned no result")
	}
	out := make([]meta.KV, 0, len(res.GetKv().GetEntries()))
	for _, e := range res.GetKv().GetEntries() {
		out = append(out, meta.KV{Key: e.GetKey(), Value: e.GetValue(), Revision: e.GetRevision()})
	}
	return out, nil
}
