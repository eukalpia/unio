package headers

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

const (
	// maxBucketNameLen is the longest bucket name in bytes.
	maxBucketNameLen = 63
	// maxVersionIDLen bounds a version id in a copy source.
	maxVersionIDLen = 128
	// versionIDParam is the query parameter of a copy source version id.
	versionIDParam = "versionId="
	// maxCopySourceLen bounds the raw x-amz-copy-source value. Percent-encoding
	// expands each byte to at most three, so this covers every valid source.
	maxCopySourceLen = 1 + 3*maxBucketNameLen + 1 + 3*object.MaxKeyLength +
		len("?"+versionIDParam) + maxVersionIDLen
)

// ParseCopySource parses x-amz-copy-source: a percent-encoded "bucket/key" with an optional
// leading slash and an optional "?versionId=" suffix (raw, not inside the encoded key).
// The bucket is validated with object.ValidateBucketName, the key with object.ValidateKey.
// Malformed percent-encoding, a missing key or a missing bucket returns InvalidArgument.
//
// A version id must be 1 to 128 bytes of visible ASCII without the
// characters that delimit or escape query values (&, %, +, #, ?, =) and
// without spaces.
func ParseCopySource(v string) (object.Ref, error) {
	v = strings.TrimSpace(v)
	if len(v) > maxCopySourceLen {
		return object.Ref{}, s3err.Errorf(s3err.InvalidArgument, "copy source is too long")
	}
	v = strings.TrimPrefix(v, "/")
	path, query, hasQuery := strings.Cut(v, "?")

	var ref object.Ref
	if hasQuery {
		vid, ok := strings.CutPrefix(query, versionIDParam)
		if !ok || !validVersionID(vid) {
			return object.Ref{}, s3err.Errorf(s3err.InvalidArgument, "copy source version id is invalid")
		}
		ref.VersionID = vid
	}

	encBucket, encKey, ok := strings.Cut(path, "/")
	if !ok {
		return object.Ref{}, s3err.Errorf(s3err.InvalidArgument, "copy source must name a bucket and a key")
	}
	bucket, err := url.PathUnescape(encBucket)
	if err != nil {
		return object.Ref{}, s3err.Errorf(s3err.InvalidArgument, "copy source bucket is not valid percent-encoding")
	}
	key, err := url.PathUnescape(encKey)
	if err != nil {
		return object.Ref{}, s3err.Errorf(s3err.InvalidArgument, "copy source key is not valid percent-encoding")
	}
	if bucket == "" {
		return object.Ref{}, s3err.Errorf(s3err.InvalidArgument, "copy source must name a bucket")
	}
	if err := object.ValidateBucketName(bucket); err != nil {
		return object.Ref{}, err
	}
	if err := object.ValidateKey(key); err != nil {
		return object.Ref{}, err
	}
	ref.Bucket = bucket
	ref.Key = key
	return ref, nil
}

// validVersionID reports whether s is a version id that needs no escaping
// inside a query value.
func validVersionID(s string) bool {
	if s == "" || len(s) > maxVersionIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c <= 0x20, c >= 0x7f:
			return false
		case c == '%', c == '&', c == '+', c == '#', c == '?', c == '=':
			return false
		}
	}
	return true
}

// ParseMetadataDirective reads x-amz-metadata-directive: "COPY" (default when absent) or
// "REPLACE". Any other value returns InvalidArgument.
func ParseMetadataDirective(h http.Header) (object.MetadataDirective, error) {
	v, ok, err := singleHeader(h, "X-Amz-Metadata-Directive", s3err.InvalidArgument)
	if err != nil {
		return object.MetadataCopy, err
	}
	if !ok {
		return object.MetadataCopy, nil
	}
	switch v {
	case "COPY":
		return object.MetadataCopy, nil
	case "REPLACE":
		return object.MetadataReplace, nil
	}
	return object.MetadataCopy, s3err.Errorf(s3err.InvalidArgument, "x-amz-metadata-directive must be COPY or REPLACE")
}
