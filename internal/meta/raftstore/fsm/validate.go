package fsm

import (
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// Limits enforced on commands that peers propose. The S3 layer checks the
// same limits for client requests; the state machine checks them again
// because every replica applies the same log, and a command that one replica
// cannot apply would make the replicas diverge.
const (
	// maxIDLength bounds upload ids and version ids.
	maxIDLength = 64
	// maxSystemKeyLength bounds system key-value keys.
	maxSystemKeyLength = 4096
	// maxBucketOwnerLength bounds the owner recorded for a bucket.
	maxBucketOwnerLength = 256
	// maxChecksumLength bounds the digest of an additional checksum.
	maxChecksumLength = 64
	// maxShards bounds the total number of shards of one erasure layout.
	maxShards = 256
	// maxReadLimit bounds the page size of a listing read.
	maxReadLimit = 10000
)

// validBucketID rejects the zero id, which no bucket is ever given.
func validBucketID(id uint64) error {
	if id == 0 {
		return errMalformed("bucket id is zero")
	}
	return nil
}

// validKey checks an object key with the rules of the S3 layer.
func validKey(key string) error {
	return object.ValidateKey(key)
}

// validBucketName checks a bucket name with the rules of the S3 layer.
func validBucketName(name string) error {
	return object.ValidateBucketName(name)
}

// validVersionID bounds a version id. Version ids are opaque to the state
// machine; the empty id is the null version and is always allowed here.
func validVersionID(id string) error {
	if len(id) > maxIDLength {
		return errMalformed("version id is too long")
	}
	return nil
}

// validUploadID requires a non-empty upload id of bounded length.
func validUploadID(id string) error {
	if id == "" || len(id) > maxIDLength {
		return errMalformed("upload id is empty or too long")
	}
	return nil
}

// validDataID requires exactly one DataID.
func validDataID(id []byte) error {
	if len(id) != len(ids.DataID{}) {
		return errMalformed(fmt.Sprintf("data id has %d bytes, want %d", len(id), len(ids.DataID{})))
	}
	return nil
}

// validVersioning rejects unknown versioning states.
func validVersioning(v pb.VersioningState) error {
	if v > pb.VersioningState_VERSIONING_STATE_SUSPENDED {
		return errMalformed(fmt.Sprintf("unknown versioning state %d", int32(v)))
	}
	return nil
}

// validChecksum checks an additional checksum. A nil checksum is absent.
func validChecksum(c *pb.Checksum) error {
	if c == nil {
		return nil
	}
	if c.Algorithm > pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256 {
		return errMalformed(fmt.Sprintf("unknown checksum algorithm %d", int32(c.Algorithm)))
	}
	if c.Type > pb.ChecksumType_CHECKSUM_TYPE_COMPOSITE {
		return errMalformed(fmt.Sprintf("unknown checksum type %d", int32(c.Type)))
	}
	if len(c.Value) > maxChecksumLength {
		return errMalformed("checksum value is too long")
	}
	return nil
}

// validLayout checks an erasure layout. The shard counts must give the drive
// list its length, every drive entry is empty or a DriveID, and at least the
// write quorum of shards is recorded as written: a layout below quorum
// describes a write that must never have been committed (invariant I4).
func validLayout(l *pb.ErasureLayout) error {
	if l == nil {
		return errMalformed("missing erasure layout")
	}
	if l.DataShards == 0 {
		return errMalformed("erasure layout has no data shards")
	}
	total := uint64(l.DataShards) + uint64(l.ParityShards)
	if total > maxShards {
		return errMalformed("erasure layout has too many shards")
	}
	if uint64(len(l.Drives)) != total {
		return errMalformed(fmt.Sprintf("erasure layout has %d drives for %d shards", len(l.Drives), total))
	}
	written := uint64(0)
	for _, d := range l.Drives {
		switch len(d) {
		case 0:
		case len(ids.DriveID{}):
			written++
		default:
			return errMalformed(fmt.Sprintf("drive id has %d bytes", len(d)))
		}
	}
	if l.StripeSize == 0 {
		return errMalformed("erasure layout has no stripe size")
	}
	quorum := uint64(l.DataShards)
	if l.DataShards == l.ParityShards {
		quorum++
	}
	if written < quorum {
		return errMalformed(fmt.Sprintf("erasure layout records %d shards written, below the quorum of %d", written, quorum))
	}
	return nil
}

// validPart checks one part. Parts number from minNumber up to the S3 maximum.
func validPart(p *pb.PartRecord, minNumber uint32) error {
	if p == nil {
		return errMalformed("missing part")
	}
	if p.Number < minNumber || p.Number > object.MaxPartNumber {
		return errMalformed(fmt.Sprintf("part number %d is out of range", p.Number))
	}
	if p.Size < 0 {
		return errMalformed("part size is negative")
	}
	if err := validDataID(p.Data); err != nil {
		return err
	}
	if err := validLayout(p.Layout); err != nil {
		return err
	}
	return validChecksum(p.Checksum)
}

// validRecord checks an object record proposed for a write, including the
// layout of each part it lists. Parts are listed in ascending order and number
// from minPartNumber.
func validRecord(rec *pb.ObjectRecord, minPartNumber uint32) error {
	if err := validRecordFields(rec); err != nil {
		return err
	}
	return validParts(rec.Parts, minPartNumber, true)
}

// validRecordFields checks the fields of an object record other than its parts.
func validRecordFields(rec *pb.ObjectRecord) error {
	if rec == nil {
		return errMalformed("missing object record")
	}
	if err := validBucketName(rec.Bucket); err != nil {
		return err
	}
	if err := validKey(rec.Key); err != nil {
		return err
	}
	if err := validVersionID(rec.VersionId); err != nil {
		return err
	}
	if rec.DeleteMarker {
		return errMalformed("a write cannot carry a delete marker")
	}
	if rec.Size < 0 {
		return errMalformed("object size is negative")
	}
	if rec.Inline != nil {
		if len(rec.Inline) > object.MaxInlineThreshold {
			return errMalformed(fmt.Sprintf("inline data of %d bytes exceeds the threshold", len(rec.Inline)))
		}
		if len(rec.Parts) > 0 {
			return errMalformed("an inline object cannot have parts")
		}
	}
	if err := object.ValidateUserMetadata(rec.GetAttributes().GetUserMetadata()); err != nil {
		return err
	}
	return validChecksum(rec.Checksum)
}

// validParts checks a list of parts: each part is valid, numbers ascend and
// start from minNumber. With layouts set, every part must carry a valid
// layout; a completion names parts by number and data id only, because the
// state machine uses the stored copies.
func validParts(parts []*pb.PartRecord, minNumber uint32, layouts bool) error {
	var prev uint32
	for i, p := range parts {
		if layouts {
			if err := validPart(p, minNumber); err != nil {
				return err
			}
		} else if err := validPartReference(p, minNumber); err != nil {
			return err
		}
		if i > 0 && p.Number <= prev {
			return s3err.Errorf(s3err.InvalidPartOrder, "part numbers must be ascending")
		}
		prev = p.Number
	}
	return nil
}

// validPartReference checks a part named in a completion: its number and data
// id. The caller's copy need not carry a layout or checksum.
func validPartReference(p *pb.PartRecord, minNumber uint32) error {
	if p == nil {
		return errMalformed("missing part")
	}
	if p.Number < minNumber || p.Number > object.MaxPartNumber {
		return errMalformed(fmt.Sprintf("part number %d is out of range", p.Number))
	}
	return validDataID(p.Data)
}

// validUpload checks an upload record proposed for creation.
func validUpload(up *pb.UploadRecord) error {
	if up == nil {
		return errMalformed("missing upload record")
	}
	if err := validBucketName(up.Bucket); err != nil {
		return err
	}
	if err := validKey(up.Key); err != nil {
		return err
	}
	if err := validUploadID(up.UploadId); err != nil {
		return err
	}
	if up.ChecksumAlgorithm > pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256 {
		return errMalformed(fmt.Sprintf("unknown checksum algorithm %d", int32(up.ChecksumAlgorithm)))
	}
	return object.ValidateUserMetadata(up.GetAttributes().GetUserMetadata())
}

// validKVKey checks a system key. Keys are non-empty and bounded.
func validKVKey(key string) error {
	if key == "" || len(key) > maxSystemKeyLength {
		return errMalformed("system key is empty or too long")
	}
	return nil
}

// validKVValue checks a system value against the store's size limit.
func validKVValue(value []byte) error {
	if len(value) > meta.MaxSystemValueSize {
		return meta.ErrValueTooLarge
	}
	return nil
}

// validOwner bounds the owner recorded for a bucket.
func validOwner(owner string) error {
	if len(owner) > maxBucketOwnerLength {
		return errMalformed("bucket owner is too long")
	}
	return nil
}

// validListLimit bounds the page size of a listing read.
func validListLimit(limit uint32) error {
	if limit == 0 {
		return errBadLimit
	}
	if limit > maxReadLimit {
		return errMalformed("listing limit is too large")
	}
	return nil
}

// validDeleteObject checks a DeleteObject command. The bucket name is checked
// because a delete marker records it.
func validDeleteObject(c *pb.DeleteObject) error {
	if err := validBucketID(c.BucketId); err != nil {
		return err
	}
	if err := validBucketName(c.Bucket); err != nil {
		return err
	}
	if err := validKey(c.Key); err != nil {
		return err
	}
	if err := validVersionID(c.VersionId); err != nil {
		return err
	}
	return validVersionID(c.MarkerVersionId)
}

// validUpdateLayout checks an UpdateLayout command. Part zero is the single
// part of an object that was not multipart.
func validUpdateLayout(c *pb.UpdateLayout) error {
	if err := validBucketID(c.BucketId); err != nil {
		return err
	}
	if err := validKey(c.Key); err != nil {
		return err
	}
	if err := validVersionID(c.VersionId); err != nil {
		return err
	}
	if c.PartNumber > object.MaxPartNumber {
		return errMalformed(fmt.Sprintf("part number %d is out of range", c.PartNumber))
	}
	if err := validDataID(c.Data); err != nil {
		return err
	}
	return validLayout(c.Layout)
}
