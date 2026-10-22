// Package mappb converts cluster maps to and from their stored protobuf form
// (ClusterMap in records.proto).
//
// The conversion is lossless for every value a Map can hold, with one
// normalization: an empty slice and a nil slice both encode as an absent
// repeated field, so they decode as nil. Decoding validates the encoded values
// and rejects anything that cannot be a Map.
package mappb

import (
	"fmt"
	"math"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// driveIDLen is the encoded length of a drive id.
const driveIDLen = len(ids.DriveID{})

// ToProto converts m to its protobuf form. It fails on values the wire form
// cannot represent: negative shard counts.
func ToProto(m *cluster.Map) (*uniov1.ClusterMap, error) {
	p := &uniov1.ClusterMap{
		Version:   m.Version,
		ClusterId: m.ClusterID,
	}
	for _, n := range m.Nodes {
		p.Nodes = append(p.Nodes, &uniov1.NodeInfo{Id: string(n.ID), Addr: n.Addr, S3Addr: n.S3Addr})
	}
	for _, d := range m.Drives {
		id := d.ID
		p.Drives = append(p.Drives, &uniov1.DriveLocation{Id: id[:], Node: string(d.Node), Path: d.Path})
	}
	for _, s := range m.Sets {
		ps := &uniov1.SetInfo{Id: uint32(s.ID)}
		if s.DataShards < 0 || s.ParityShards < 0 || int64(s.DataShards) > math.MaxUint32 || int64(s.ParityShards) > math.MaxUint32 {
			return nil, fmt.Errorf("mappb: set %d has invalid shard counts %d+%d", s.ID, s.DataShards, s.ParityShards)
		}
		ps.DataShards = uint32(s.DataShards)
		ps.ParityShards = uint32(s.ParityShards)
		for _, d := range s.Drives {
			id := d
			ps.Drives = append(ps.Drives, id[:])
		}
		p.Sets = append(p.Sets, ps)
	}
	for _, part := range m.Partitions {
		pp := &uniov1.PartitionInfo{Id: uint32(part.ID)}
		for _, r := range part.Replicas {
			pp.Replicas = append(pp.Replicas, string(r))
		}
		for _, nv := range part.NonVoters {
			pp.NonVoters = append(pp.NonVoters, string(nv))
		}
		p.Partitions = append(p.Partitions, pp)
	}
	return p, nil
}

// FromProto converts a protobuf cluster map back to a Map. It rejects drive
// ids that are not 16 bytes and shard counts that do not fit an int.
func FromProto(p *uniov1.ClusterMap) (*cluster.Map, error) {
	m := &cluster.Map{
		Version:   p.GetVersion(),
		ClusterID: p.GetClusterId(),
	}
	for _, n := range p.GetNodes() {
		m.Nodes = append(m.Nodes, cluster.NodeInfo{
			ID:     ids.NodeID(n.GetId()),
			Addr:   n.GetAddr(),
			S3Addr: n.GetS3Addr(),
		})
	}
	for _, d := range p.GetDrives() {
		id, err := driveID(d.GetId())
		if err != nil {
			return nil, err
		}
		m.Drives = append(m.Drives, cluster.DriveLocation{ID: id, Node: ids.NodeID(d.GetNode()), Path: d.GetPath()})
	}
	for _, s := range p.GetSets() {
		if s.GetDataShards() > math.MaxInt32 || s.GetParityShards() > math.MaxInt32 {
			return nil, fmt.Errorf("mappb: set %d shard counts out of range", s.GetId())
		}
		set := cluster.SetInfo{
			ID:           ids.SetID(s.GetId()),
			DataShards:   int(s.GetDataShards()),
			ParityShards: int(s.GetParityShards()),
		}
		for _, raw := range s.GetDrives() {
			id, err := driveID(raw)
			if err != nil {
				return nil, err
			}
			set.Drives = append(set.Drives, id)
		}
		m.Sets = append(m.Sets, set)
	}
	for _, part := range p.GetPartitions() {
		info := cluster.PartitionInfo{ID: ids.PartitionID(part.GetId())}
		for _, r := range part.GetReplicas() {
			info.Replicas = append(info.Replicas, ids.NodeID(r))
		}
		for _, nv := range part.GetNonVoters() {
			info.NonVoters = append(info.NonVoters, ids.NodeID(nv))
		}
		m.Partitions = append(m.Partitions, info)
	}
	return m, nil
}

// driveID decodes a 16-byte drive id.
func driveID(b []byte) (ids.DriveID, error) {
	var id ids.DriveID
	if len(b) != driveIDLen {
		return id, fmt.Errorf("mappb: drive id is %d bytes, want %d", len(b), driveIDLen)
	}
	copy(id[:], b)
	return id, nil
}

// Marshal encodes m as a ClusterMap message.
func Marshal(m *cluster.Map) ([]byte, error) {
	p, err := ToProto(m)
	if err != nil {
		return nil, err
	}
	return p.MarshalVT()
}

// Unmarshal decodes a ClusterMap message produced by Marshal.
func Unmarshal(b []byte) (*cluster.Map, error) {
	var p uniov1.ClusterMap
	if err := p.UnmarshalVT(b); err != nil {
		return nil, fmt.Errorf("mappb: decode cluster map: %w", err)
	}
	return FromProto(&p)
}
