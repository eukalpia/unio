package remotedrive

import (
	"math"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// Method names. Stream methods carry shard bytes; the others are RPCs.
const (
	MethodStatShard   = "drive.StatShard"
	MethodDeleteShard = "drive.DeleteShard"
	MethodInfo        = "drive.Info"
	MethodWalkShards  = "drive.WalkShards"
	MethodWriteShard  = "drive.WriteShard"
	MethodReadShard   = "drive.ReadShard"
)

// badRequest reports a request that does not decode or is out of range. The
// text names the request kind only, never the bytes received.
func badRequest(what string) error {
	return s3err.Errorf(s3err.InvalidRequest, "malformed %s request", what)
}

// shardRef converts a shard id to its wire form.
func shardRef(id ids.ShardID) *uniov1.ShardRef {
	return &uniov1.ShardRef{
		Data:  append([]byte(nil), id.Data[:]...),
		Index: uint32(id.Index),
	}
}

// shardIDOf converts a wire shard reference back to a shard id, rejecting
// references that a conforming peer would never send.
func shardIDOf(r *uniov1.ShardRef) (ids.ShardID, error) {
	var id ids.ShardID
	if len(r.GetData()) != len(id.Data) || r.GetIndex() > math.MaxUint16 {
		return id, badRequest("shard")
	}
	copy(id.Data[:], r.GetData())
	id.Index = uint16(r.GetIndex())
	return id, nil
}

// driveIDOf converts a wire drive id, which must be exactly 16 bytes.
func driveIDOf(b []byte) (ids.DriveID, error) {
	var id ids.DriveID
	if len(b) != len(id) {
		return id, badRequest("drive")
	}
	copy(id[:], b)
	return id, nil
}

// shardMeta converts drive shard metadata to its wire form.
func shardMeta(info drive.ShardInfo) *uniov1.ShardMeta {
	m := &uniov1.ShardMeta{Shard: shardRef(info.ID), Size: info.Size}
	if !info.Created.IsZero() {
		m.CreatedUnixNano = info.Created.UnixNano()
	}
	return m
}

// shardInfoOf converts wire shard metadata back to drive shard metadata.
func shardInfoOf(m *uniov1.ShardMeta) (drive.ShardInfo, error) {
	id, err := shardIDOf(m.GetShard())
	if err != nil {
		return drive.ShardInfo{}, err
	}
	info := drive.ShardInfo{ID: id, Size: m.GetSize()}
	if ns := m.GetCreatedUnixNano(); ns != 0 {
		info.Created = time.Unix(0, ns)
	}
	return info, nil
}
