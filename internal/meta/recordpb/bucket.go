package recordpb

import (
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Bucket conversions. The record stores the bucket info together with a
// deletion flag, which is true while the bucket is being deleted and writes
// are rejected. Unknown versioning values are errors in both directions.

// BucketToProto converts a bucket's info and deletion flag to its message. It
// returns an error for an unknown versioning state or a creation time outside
// the nanosecond range.
func BucketToProto(info object.BucketInfo, deleting bool) (*uniov1.BucketRecord, error) {
	p, err := bucketToProto(info, deleting)
	return p, prefixErr(err)
}

// BucketFromProto converts p to a bucket's info and deletion flag. It returns
// an error for a nil message or an unknown versioning state.
func BucketFromProto(p *uniov1.BucketRecord) (info object.BucketInfo, deleting bool, err error) {
	info, deleting, err = bucketFromProto(p)
	return info, deleting, prefixErr(err)
}

func bucketToProto(info object.BucketInfo, deleting bool) (*uniov1.BucketRecord, error) {
	created, err := encodeTime("bucket creation time", info.Created)
	if err != nil {
		return nil, err
	}
	versioning, err := versioningToProto(info.Versioning)
	if err != nil {
		return nil, err
	}
	return &uniov1.BucketRecord{
		Name:            info.Name,
		Id:              uint64(info.ID),
		CreatedUnixNano: created,
		Owner:           info.Owner,
		Versioning:      versioning,
		Deleting:        deleting,
	}, nil
}

func bucketFromProto(p *uniov1.BucketRecord) (object.BucketInfo, bool, error) {
	if p == nil {
		return object.BucketInfo{}, false, errNil("bucket")
	}
	versioning, err := versioningFromProto(p.GetVersioning())
	if err != nil {
		return object.BucketInfo{}, false, err
	}
	info := object.BucketInfo{
		Name:       p.GetName(),
		ID:         ids.BucketID(p.GetId()),
		Created:    TimeFromNano(p.GetCreatedUnixNano()),
		Owner:      p.GetOwner(),
		Versioning: versioning,
	}
	return info, p.GetDeleting(), nil
}

func versioningToProto(v object.VersioningState) (uniov1.VersioningState, error) {
	switch v {
	case object.VersioningUnversioned:
		return uniov1.VersioningState_VERSIONING_STATE_UNVERSIONED, nil
	case object.VersioningEnabled:
		return uniov1.VersioningState_VERSIONING_STATE_ENABLED, nil
	case object.VersioningSuspended:
		return uniov1.VersioningState_VERSIONING_STATE_SUSPENDED, nil
	}
	return 0, fmt.Errorf("unknown versioning state %d", v)
}

func versioningFromProto(v uniov1.VersioningState) (object.VersioningState, error) {
	switch v {
	case uniov1.VersioningState_VERSIONING_STATE_UNVERSIONED:
		return object.VersioningUnversioned, nil
	case uniov1.VersioningState_VERSIONING_STATE_ENABLED:
		return object.VersioningEnabled, nil
	case uniov1.VersioningState_VERSIONING_STATE_SUSPENDED:
		return object.VersioningSuspended, nil
	}
	return 0, fmt.Errorf("unknown versioning state %d", v)
}
