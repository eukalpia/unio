package recordpb

import (
	"fmt"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Upload conversions. UploadRecord.Options holds the attributes the final
// object will get, plus the requested checksum algorithm. Options.Conditions
// are NOT stored. They are dropped on encode, and decoding yields zero
// Conditions. Callers that compare a round trip must clear Conditions first.

// UploadToProto converts u to its message. Options.Conditions is dropped. It
// returns an error for an unknown checksum algorithm or a time outside the
// nanosecond range.
func UploadToProto(u meta.UploadRecord) (*uniov1.UploadRecord, error) {
	p, err := uploadToProto(u)
	return p, prefixErr(err)
}

// UploadFromProto converts p to an upload record. Options.Conditions is
// zero. It returns an error for a nil message or an unknown checksum
// algorithm.
func UploadFromProto(p *uniov1.UploadRecord) (meta.UploadRecord, error) {
	u, err := uploadFromProto(p)
	return u, prefixErr(err)
}

func uploadToProto(u meta.UploadRecord) (*uniov1.UploadRecord, error) {
	initiated, err := encodeTime("upload initiated time", u.Initiated)
	if err != nil {
		return nil, err
	}
	alg, err := checksumAlgorithmToProto(u.Options.ChecksumAlgorithm)
	if err != nil {
		return nil, err
	}
	o := u.Options
	return &uniov1.UploadRecord{
		Bucket:            u.Bucket,
		Key:               u.Key,
		UploadId:          u.UploadID,
		InitiatedUnixNano: initiated,
		Attributes: attributesToProto(objectAttributes{
			contentType:        o.ContentType,
			contentEncoding:    o.ContentEncoding,
			contentDisposition: o.ContentDisposition,
			contentLanguage:    o.ContentLanguage,
			cacheControl:       o.CacheControl,
			expires:            o.Expires,
			userMetadata:       o.UserMetadata,
			tags:               o.Tags,
			storageClass:       o.StorageClass,
		}),
		ChecksumAlgorithm: alg,
	}, nil
}

func uploadFromProto(p *uniov1.UploadRecord) (meta.UploadRecord, error) {
	if p == nil {
		return meta.UploadRecord{}, errNil("upload")
	}
	alg, err := checksumAlgorithmFromProto(p.GetChecksumAlgorithm())
	if err != nil {
		return meta.UploadRecord{}, fmt.Errorf("upload checksum: %w", err)
	}
	attrs := attributesFromProto(p.GetAttributes())
	return meta.UploadRecord{
		Bucket:    p.GetBucket(),
		Key:       p.GetKey(),
		UploadID:  p.GetUploadId(),
		Initiated: TimeFromNano(p.GetInitiatedUnixNano()),
		Options: object.PutOptions{
			ContentType:        attrs.contentType,
			ContentEncoding:    attrs.contentEncoding,
			ContentDisposition: attrs.contentDisposition,
			ContentLanguage:    attrs.contentLanguage,
			CacheControl:       attrs.cacheControl,
			Expires:            attrs.expires,
			UserMetadata:       attrs.userMetadata,
			Tags:               attrs.tags,
			StorageClass:       attrs.storageClass,
			ChecksumAlgorithm:  alg,
		},
	}, nil
}
