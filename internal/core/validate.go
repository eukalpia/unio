package core

import (
	"net/netip"
	"strings"
	"unicode/utf8"
)

// ValidateBucketName checks a bucket name against the S3 naming rules for
// general purpose buckets. It returns an *Error with ErrInvalidBucketName.
func ValidateBucketName(name string) error {
	if len(name) < 3 || len(name) > 63 {
		return Errorf(ErrInvalidBucketName, "bucket name must be 3-63 characters long")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' || c == '.':
			if i == 0 || i == len(name)-1 {
				return Errorf(ErrInvalidBucketName, "bucket name must start and end with a letter or digit")
			}
		default:
			return Errorf(ErrInvalidBucketName, "bucket name contains invalid characters")
		}
	}
	if strings.Contains(name, "..") || strings.Contains(name, ".-") || strings.Contains(name, "-.") {
		return Errorf(ErrInvalidBucketName, "bucket name contains invalid label sequence")
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return Errorf(ErrInvalidBucketName, "bucket name must not be an IP address")
	}
	if strings.HasPrefix(name, "xn--") || strings.HasPrefix(name, "sthree-") ||
		strings.HasSuffix(name, "-s3alias") || strings.HasSuffix(name, "--ol-s3") {
		return Errorf(ErrInvalidBucketName, "bucket name uses a reserved prefix or suffix")
	}
	return nil
}

// ValidateObjectKey checks an object key: non-empty, at most
// MaxObjectKeyLength bytes, valid UTF-8, and free of NUL bytes.
func ValidateObjectKey(key string) error {
	switch {
	case key == "":
		return Errorf(ErrInvalidArgument, "object key must not be empty")
	case len(key) > MaxObjectKeyLength:
		return Errorf(ErrKeyTooLongError, "object key is longer than %d bytes", MaxObjectKeyLength)
	case !utf8.ValidString(key):
		return Errorf(ErrInvalidArgument, "object key is not valid UTF-8")
	case strings.IndexByte(key, 0) >= 0:
		return Errorf(ErrInvalidArgument, "object key must not contain NUL")
	}
	return nil
}

// ValidateUserMetadata checks the total size of user metadata.
func ValidateUserMetadata(md map[string]string) error {
	n := 0
	for k, v := range md {
		n += len(k) + len(v)
	}
	if n > MaxUserMetadataSize {
		return Errorf(ErrMetadataTooLarge, "user metadata exceeds %d bytes", MaxUserMetadataSize)
	}
	return nil
}
