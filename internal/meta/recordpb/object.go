package recordpb

import (
	"bytes"
	"fmt"

	"github.com/eukalpia/unio/internal/meta"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Object conversions. Inline is the one field whose presence is significant:
// a nil Inline is absent, and a non-nil Inline, including an empty one, is
// present. Every other slice and map is equivalent when nil or empty. The
// conversions copy all byte slices and maps. Size must be non-negative. Parts
// keep their order. A nil entry in Parts is an error.

// objectAttributes holds the attribute set shared by stored objects and
// uploads. It mirrors uniov1.ObjectAttributes.
type objectAttributes struct {
	contentType        string
	contentEncoding    string
	contentDisposition string
	contentLanguage    string
	cacheControl       string
	expires            string
	userMetadata       map[string]string
	tags               string
	storageClass       string
}

// attributesToProto converts a to its message, copying the metadata map.
func attributesToProto(a objectAttributes) *uniov1.ObjectAttributes {
	return &uniov1.ObjectAttributes{
		ContentType:        a.contentType,
		ContentEncoding:    a.contentEncoding,
		ContentDisposition: a.contentDisposition,
		ContentLanguage:    a.contentLanguage,
		CacheControl:       a.cacheControl,
		Expires:            a.expires,
		UserMetadata:       cloneMap(a.userMetadata),
		Tags:               a.tags,
		StorageClass:       a.storageClass,
	}
}

// attributesFromProto decodes the attributes. A nil message decodes as zero
// attributes, because the generated getters are nil-safe.
func attributesFromProto(p *uniov1.ObjectAttributes) objectAttributes {
	return objectAttributes{
		contentType:        p.GetContentType(),
		contentEncoding:    p.GetContentEncoding(),
		contentDisposition: p.GetContentDisposition(),
		contentLanguage:    p.GetContentLanguage(),
		cacheControl:       p.GetCacheControl(),
		expires:            p.GetExpires(),
		userMetadata:       cloneMap(p.GetUserMetadata()),
		tags:               p.GetTags(),
		storageClass:       p.GetStorageClass(),
	}
}

// ObjectToProto converts r to its message. It returns an error for a negative
// Size, a time outside the nanosecond range, or an invalid checksum or part.
func ObjectToProto(r meta.ObjectRecord) (*uniov1.ObjectRecord, error) {
	p, err := objectToProto(r)
	return p, prefixErr(err)
}

// ObjectFromProto converts p to an object record. It returns an error for a
// nil message, a negative size, a nil part, or an invalid checksum or part.
func ObjectFromProto(p *uniov1.ObjectRecord) (meta.ObjectRecord, error) {
	r, err := objectFromProto(p)
	return r, prefixErr(err)
}

func objectToProto(r meta.ObjectRecord) (*uniov1.ObjectRecord, error) {
	if err := nonNegative("object size", r.Size); err != nil {
		return nil, err
	}
	modTime, err := encodeTime("object mod time", r.ModTime)
	if err != nil {
		return nil, err
	}
	checksum, err := checksumToProto(r.Checksum)
	if err != nil {
		return nil, fmt.Errorf("object checksum: %w", err)
	}
	var parts []*uniov1.PartRecord
	if len(r.Parts) > 0 {
		parts = make([]*uniov1.PartRecord, len(r.Parts))
		for i, part := range r.Parts {
			m, err := partToProto(part)
			if err != nil {
				return nil, fmt.Errorf("part index %d: %w", i, err)
			}
			parts[i] = m
		}
	}
	p := &uniov1.ObjectRecord{
		Bucket:          r.Bucket,
		Key:             r.Key,
		VersionId:       r.VersionID,
		ModTimeUnixNano: modTime,
		DeleteMarker:    r.DeleteMarker,
		Size:            r.Size,
		Etag:            r.ETag,
		Attributes: attributesToProto(objectAttributes{
			contentType:        r.ContentType,
			contentEncoding:    r.ContentEncoding,
			contentDisposition: r.ContentDisposition,
			contentLanguage:    r.ContentLanguage,
			cacheControl:       r.CacheControl,
			expires:            r.Expires,
			userMetadata:       r.UserMetadata,
			tags:               r.Tags,
			storageClass:       r.StorageClass,
		}),
		Checksum:  checksum,
		Parts:     parts,
		Multipart: r.Multipart,
	}
	if r.Inline != nil {
		p.Inline = bytes.Clone(r.Inline)
	}
	return p, nil
}

func objectFromProto(p *uniov1.ObjectRecord) (meta.ObjectRecord, error) {
	if p == nil {
		return meta.ObjectRecord{}, errNil("object")
	}
	if err := nonNegative("object size", p.GetSize()); err != nil {
		return meta.ObjectRecord{}, err
	}
	checksum, err := checksumFromOptional(p.GetChecksum())
	if err != nil {
		return meta.ObjectRecord{}, fmt.Errorf("object checksum: %w", err)
	}
	var parts []meta.PartRecord
	if n := len(p.GetParts()); n > 0 {
		parts = make([]meta.PartRecord, n)
		for i, pp := range p.GetParts() {
			part, err := partFromProto(pp)
			if err != nil {
				return meta.ObjectRecord{}, fmt.Errorf("part index %d: %w", i, err)
			}
			parts[i] = part
		}
	}
	attrs := attributesFromProto(p.GetAttributes())
	r := meta.ObjectRecord{
		Bucket:             p.GetBucket(),
		Key:                p.GetKey(),
		VersionID:          p.GetVersionId(),
		ModTime:            TimeFromNano(p.GetModTimeUnixNano()),
		DeleteMarker:       p.GetDeleteMarker(),
		Size:               p.GetSize(),
		ETag:               p.GetEtag(),
		ContentType:        attrs.contentType,
		ContentEncoding:    attrs.contentEncoding,
		ContentDisposition: attrs.contentDisposition,
		ContentLanguage:    attrs.contentLanguage,
		CacheControl:       attrs.cacheControl,
		Expires:            attrs.expires,
		UserMetadata:       attrs.userMetadata,
		Tags:               attrs.tags,
		Checksum:           checksum,
		StorageClass:       attrs.storageClass,
		Parts:              parts,
		Multipart:          p.GetMultipart(),
	}
	if in := p.GetInline(); in != nil {
		r.Inline = bytes.Clone(in)
	}
	return r, nil
}
