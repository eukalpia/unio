package headers

import (
	"net/http"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// maxAttributeLen bounds the length of a verbatim object attribute value in bytes.
const maxAttributeLen = 1024

// storageClassDefault is the storage class of an object written without x-amz-storage-class.
const storageClassDefault = "STANDARD"

// ParseObjectAttributes builds the destination attributes of a PUT, CreateMultipartUpload
// or CopyObject with REPLACE. It fills ContentType, ContentEncoding, ContentDisposition,
// ContentLanguage, CacheControl, Expires (verbatim strings, at most 1024 bytes, no control
// bytes), UserMetadata (ParseUserMetadata), Tags (ParseTagging), StorageClass
// (x-amz-storage-class: STANDARD, REDUCED_REDUNDANCY, STANDARD_IA, ONEZONE_IA,
// INTELLIGENT_TIERING, GLACIER, DEEP_ARCHIVE, GLACIER_IR; an absent header yields
// "STANDARD"; anything else returns InvalidStorageClass), ChecksumAlgorithm (from
// ParseChecksumRequest; the digest is ignored here) and Conditions (ParseConditions).
func ParseObjectAttributes(h http.Header) (object.PutOptions, error) {
	var opts object.PutOptions
	verbatim := [...]struct {
		name string
		dst  *string
	}{
		{"Content-Type", &opts.ContentType},
		{"Content-Encoding", &opts.ContentEncoding},
		{"Content-Disposition", &opts.ContentDisposition},
		{"Content-Language", &opts.ContentLanguage},
		{"Cache-Control", &opts.CacheControl},
		{"Expires", &opts.Expires},
	}
	for _, a := range verbatim {
		v := h.Get(a.name)
		if len(v) > maxAttributeLen || containsControl(v) {
			return object.PutOptions{}, s3err.Errorf(s3err.InvalidArgument, "%s is invalid", a.name)
		}
		*a.dst = v
	}

	md, err := ParseUserMetadata(h)
	if err != nil {
		return object.PutOptions{}, err
	}
	opts.UserMetadata = md

	tags, _, err := singleHeader(h, "X-Amz-Tagging", s3err.InvalidTag)
	if err != nil {
		return object.PutOptions{}, err
	}
	if opts.Tags, err = ParseTagging(tags); err != nil {
		return object.PutOptions{}, err
	}

	class, ok, err := singleHeader(h, "X-Amz-Storage-Class", s3err.InvalidStorageClass)
	if err != nil {
		return object.PutOptions{}, err
	}
	if !ok {
		class = storageClassDefault
	}
	if !validStorageClass(class) {
		return object.PutOptions{}, s3err.Errorf(s3err.InvalidStorageClass, "storage class is not supported")
	}
	opts.StorageClass = class

	cs, err := ParseChecksumRequest(h)
	if err != nil {
		return object.PutOptions{}, err
	}
	opts.ChecksumAlgorithm = cs.Algorithm

	if opts.Conditions, err = ParseConditions(h); err != nil {
		return object.PutOptions{}, err
	}
	return opts, nil
}

// validStorageClass reports whether s is a storage class that S3 accepts on writes.
func validStorageClass(s string) bool {
	switch s {
	case "STANDARD", "REDUCED_REDUNDANCY", "STANDARD_IA", "ONEZONE_IA",
		"INTELLIGENT_TIERING", "GLACIER", "DEEP_ARCHIVE", "GLACIER_IR":
		return true
	}
	return false
}
