// Package pbconv converts between core records and their stored protobuf
// form (api/proto/unio/v1/records.proto). Conversions are lossless:
// FromPB(ToPB(x)) equals x for every valid x.
package pbconv

import (
	"fmt"
	"time"

	"github.com/eukalpia/unio/internal/core"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

func unixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromUnixNano(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// ChecksumToPB converts a checksum; a zero checksum becomes nil.
func ChecksumToPB(c core.Checksum) *uniov1.Checksum {
	if c.IsZero() {
		return nil
	}
	return &uniov1.Checksum{
		Algorithm: uniov1.ChecksumAlgorithm(c.Algorithm),
		Type:      uniov1.ChecksumType(c.Type),
		Value:     c.Value,
		Parts:     uint32(c.Parts),
	}
}

// ChecksumFromPB converts a stored checksum; nil becomes the zero value.
func ChecksumFromPB(p *uniov1.Checksum) core.Checksum {
	if p == nil || p.Algorithm == uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_NONE {
		return core.Checksum{}
	}
	return core.Checksum{
		Algorithm: core.ChecksumAlgorithm(p.Algorithm),
		Type:      core.ChecksumType(p.Type),
		Value:     p.Value,
		Parts:     int(p.Parts),
	}
}

// BucketToPB converts bucket info. deleting marks a bucket being deleted.
func BucketToPB(b core.BucketInfo, deleting bool) *uniov1.BucketRecord {
	return &uniov1.BucketRecord{
		Name:            b.Name,
		Id:              uint64(b.ID),
		CreatedUnixNano: unixNano(b.Created),
		Owner:           b.Owner,
		Versioning:      uniov1.VersioningState(b.Versioning),
		Deleting:        deleting,
	}
}

// BucketFromPB converts a stored bucket record.
func BucketFromPB(p *uniov1.BucketRecord) (core.BucketInfo, bool) {
	return core.BucketInfo{
		Name:       p.Name,
		ID:         core.BucketID(p.Id),
		Created:    fromUnixNano(p.CreatedUnixNano),
		Owner:      p.Owner,
		Versioning: core.VersioningState(p.Versioning),
	}, p.Deleting
}

// LayoutToPB converts an erasure layout. Zero drive ids are stored empty.
func LayoutToPB(l core.ErasureLayout) *uniov1.ErasureLayout {
	drives := make([][]byte, len(l.Drives))
	for i, d := range l.Drives {
		if !d.IsZero() {
			b := d
			drives[i] = b[:]
		}
	}
	return &uniov1.ErasureLayout{
		Set:          uint32(l.Set),
		DataShards:   uint32(l.DataShards),
		ParityShards: uint32(l.ParityShards),
		StripeSize:   uint32(l.StripeSize),
		Drives:       drives,
	}
}

// LayoutFromPB converts a stored layout.
func LayoutFromPB(p *uniov1.ErasureLayout) (core.ErasureLayout, error) {
	if p == nil {
		return core.ErasureLayout{}, nil
	}
	drives := make([]core.DriveID, len(p.Drives))
	for i, b := range p.Drives {
		switch len(b) {
		case 0:
		case len(core.DriveID{}):
			copy(drives[i][:], b)
		default:
			return core.ErasureLayout{}, fmt.Errorf("pbconv: layout drive %d: bad id length %d", i, len(b))
		}
	}
	return core.ErasureLayout{
		Set:          core.SetID(p.Set),
		DataShards:   int(p.DataShards),
		ParityShards: int(p.ParityShards),
		StripeSize:   int(p.StripeSize),
		Drives:       drives,
	}, nil
}

// PartToPB converts a part record.
func PartToPB(r core.PartRecord) *uniov1.PartRecord {
	data := r.Data
	return &uniov1.PartRecord{
		Number:          uint32(r.Number),
		Size:            r.Size,
		Etag:            r.ETag,
		Checksum:        ChecksumToPB(r.Checksum),
		ModTimeUnixNano: unixNano(r.ModTime),
		Data:            data[:],
		Layout:          LayoutToPB(r.Layout),
	}
}

// PartFromPB converts a stored part record.
func PartFromPB(p *uniov1.PartRecord) (core.PartRecord, error) {
	r := core.PartRecord{
		Number:   int(p.Number),
		Size:     p.Size,
		ETag:     p.Etag,
		Checksum: ChecksumFromPB(p.Checksum),
		ModTime:  fromUnixNano(p.ModTimeUnixNano),
	}
	if len(p.Data) != len(r.Data) {
		return r, fmt.Errorf("pbconv: part %d: bad data id length %d", p.Number, len(p.Data))
	}
	copy(r.Data[:], p.Data)
	l, err := LayoutFromPB(p.Layout)
	if err != nil {
		return r, err
	}
	r.Layout = l
	return r, nil
}

func attrsToPB(o core.PutObjectOptions) *uniov1.ObjectAttributes {
	return &uniov1.ObjectAttributes{
		ContentType:        o.ContentType,
		ContentEncoding:    o.ContentEncoding,
		ContentDisposition: o.ContentDisposition,
		ContentLanguage:    o.ContentLanguage,
		CacheControl:       o.CacheControl,
		Expires:            o.Expires,
		UserMetadata:       o.UserMetadata,
		Tags:               o.Tags,
		StorageClass:       o.StorageClass,
	}
}

func attrsFromPB(p *uniov1.ObjectAttributes) core.PutObjectOptions {
	if p == nil {
		return core.PutObjectOptions{}
	}
	return core.PutObjectOptions{
		ContentType:        p.ContentType,
		ContentEncoding:    p.ContentEncoding,
		ContentDisposition: p.ContentDisposition,
		ContentLanguage:    p.ContentLanguage,
		CacheControl:       p.CacheControl,
		Expires:            p.Expires,
		UserMetadata:       p.UserMetadata,
		Tags:               p.Tags,
		StorageClass:       p.StorageClass,
	}
}

// ObjectToPB converts an object record.
func ObjectToPB(r core.ObjectRecord) *uniov1.ObjectRecord {
	p := &uniov1.ObjectRecord{
		Bucket:          r.Bucket,
		Key:             r.Key,
		VersionId:       r.VersionID,
		ModTimeUnixNano: unixNano(r.ModTime),
		DeleteMarker:    r.DeleteMarker,
		Size:            r.Size,
		Etag:            r.ETag,
		Attributes: attrsToPB(core.PutObjectOptions{
			ContentType:        r.ContentType,
			ContentEncoding:    r.ContentEncoding,
			ContentDisposition: r.ContentDisposition,
			ContentLanguage:    r.ContentLanguage,
			CacheControl:       r.CacheControl,
			Expires:            r.Expires,
			UserMetadata:       r.UserMetadata,
			Tags:               r.Tags,
			StorageClass:       r.StorageClass,
		}),
		Checksum:  ChecksumToPB(r.Checksum),
		Multipart: r.Multipart,
	}
	if r.Inline != nil {
		p.Inline = r.Inline
	}
	if len(r.Parts) > 0 {
		p.Parts = make([]*uniov1.PartRecord, len(r.Parts))
		for i, part := range r.Parts {
			p.Parts[i] = PartToPB(part)
		}
	}
	return p
}

// ObjectFromPB converts a stored object record.
func ObjectFromPB(p *uniov1.ObjectRecord) (core.ObjectRecord, error) {
	a := attrsFromPB(p.Attributes)
	r := core.ObjectRecord{
		Bucket:             p.Bucket,
		Key:                p.Key,
		VersionID:          p.VersionId,
		ModTime:            fromUnixNano(p.ModTimeUnixNano),
		DeleteMarker:       p.DeleteMarker,
		Size:               p.Size,
		ETag:               p.Etag,
		ContentType:        a.ContentType,
		ContentEncoding:    a.ContentEncoding,
		ContentDisposition: a.ContentDisposition,
		ContentLanguage:    a.ContentLanguage,
		CacheControl:       a.CacheControl,
		Expires:            a.Expires,
		UserMetadata:       a.UserMetadata,
		Tags:               a.Tags,
		StorageClass:       a.StorageClass,
		Checksum:           ChecksumFromPB(p.Checksum),
		Multipart:          p.Multipart,
	}
	if p.Inline != nil {
		r.Inline = p.Inline
		if r.Inline == nil {
			r.Inline = []byte{}
		}
	}
	if len(p.Parts) > 0 {
		r.Parts = make([]core.PartRecord, len(p.Parts))
		for i, pp := range p.Parts {
			part, err := PartFromPB(pp)
			if err != nil {
				return r, err
			}
			r.Parts[i] = part
		}
	}
	return r, nil
}

// UploadToPB converts an upload record.
func UploadToPB(u core.UploadRecord) *uniov1.UploadRecord {
	return &uniov1.UploadRecord{
		Bucket:            u.Bucket,
		Key:               u.Key,
		UploadId:          u.UploadID,
		InitiatedUnixNano: unixNano(u.Initiated),
		Attributes:        attrsToPB(u.Options),
		ChecksumAlgorithm: uniov1.ChecksumAlgorithm(u.Options.ChecksumAlgorithm),
	}
}

// UploadFromPB converts a stored upload record. Conditions are not stored.
func UploadFromPB(p *uniov1.UploadRecord) core.UploadRecord {
	opts := attrsFromPB(p.Attributes)
	opts.ChecksumAlgorithm = core.ChecksumAlgorithm(p.ChecksumAlgorithm)
	return core.UploadRecord{
		Bucket:    p.Bucket,
		Key:       p.Key,
		UploadID:  p.UploadId,
		Initiated: fromUnixNano(p.InitiatedUnixNano),
		Options:   opts,
	}
}

func driveIDFromBytes(b []byte) (core.DriveID, error) {
	var id core.DriveID
	if len(b) != len(id) {
		return id, fmt.Errorf("pbconv: bad drive id length %d", len(b))
	}
	copy(id[:], b)
	return id, nil
}

// ClusterMapToPB converts a cluster map.
func ClusterMapToPB(m *core.ClusterMap) *uniov1.ClusterMap {
	p := &uniov1.ClusterMap{Version: m.Version, ClusterId: m.ClusterID}
	for _, n := range m.Nodes {
		p.Nodes = append(p.Nodes, &uniov1.NodeInfo{Id: string(n.ID), Addr: n.Addr, S3Addr: n.S3Addr})
	}
	for _, d := range m.Drives {
		id := d.ID
		p.Drives = append(p.Drives, &uniov1.DriveLocation{Id: id[:], Node: string(d.Node), Path: d.Path})
	}
	for _, s := range m.Sets {
		ps := &uniov1.SetInfo{Id: uint32(s.ID), DataShards: uint32(s.DataShards), ParityShards: uint32(s.ParityShards)}
		for _, d := range s.Drives {
			id := d
			ps.Drives = append(ps.Drives, id[:])
		}
		p.Sets = append(p.Sets, ps)
	}
	for _, part := range m.Partitions {
		pp := &uniov1.PartitionInfo{Id: uint32(part.ID)}
		for _, n := range part.Replicas {
			pp.Replicas = append(pp.Replicas, string(n))
		}
		for _, n := range part.NonVoters {
			pp.NonVoters = append(pp.NonVoters, string(n))
		}
		p.Partitions = append(p.Partitions, pp)
	}
	return p
}

// ClusterMapFromPB converts a stored cluster map.
func ClusterMapFromPB(p *uniov1.ClusterMap) (*core.ClusterMap, error) {
	m := &core.ClusterMap{Version: p.Version, ClusterID: p.ClusterId}
	for _, n := range p.Nodes {
		m.Nodes = append(m.Nodes, core.NodeInfo{ID: core.NodeID(n.Id), Addr: n.Addr, S3Addr: n.S3Addr})
	}
	for _, d := range p.Drives {
		id, err := driveIDFromBytes(d.Id)
		if err != nil {
			return nil, err
		}
		m.Drives = append(m.Drives, core.DriveLocation{ID: id, Node: core.NodeID(d.Node), Path: d.Path})
	}
	for _, s := range p.Sets {
		cs := core.SetInfo{ID: core.SetID(s.Id), DataShards: int(s.DataShards), ParityShards: int(s.ParityShards)}
		for _, b := range s.Drives {
			id, err := driveIDFromBytes(b)
			if err != nil {
				return nil, err
			}
			cs.Drives = append(cs.Drives, id)
		}
		m.Sets = append(m.Sets, cs)
	}
	for _, pp := range p.Partitions {
		part := core.PartitionInfo{ID: core.PartitionID(pp.Id)}
		for _, n := range pp.Replicas {
			part.Replicas = append(part.Replicas, core.NodeID(n))
		}
		for _, n := range pp.NonVoters {
			part.NonVoters = append(part.NonVoters, core.NodeID(n))
		}
		m.Partitions = append(m.Partitions, part)
	}
	return m, nil
}
