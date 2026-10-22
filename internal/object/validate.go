package object

import (
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/eukalpia/unio/internal/s3err"
)

// Prefixes and suffixes that Amazon S3 reserves for general purpose bucket
// names (S3 bucket naming rules).
var (
	reservedPrefixes = []string{"xn--", "sthree-", "amzn-s3-demo-"}
	reservedSuffixes = []string{"-s3alias", "--ol-s3", ".mrap", "--x-s3", "--table-s3"}
)

// ReservedBucketName is the path prefix of Unio's service endpoints
// (/unio/health, /unio/metrics, /unio/admin). It can never be a bucket.
const ReservedBucketName = "unio"

// ValidateBucketName checks a bucket name against the S3 naming rules for
// general purpose buckets and rejects the reserved name.
func ValidateBucketName(name string) error {
	if name == ReservedBucketName {
		return s3err.Errorf(s3err.InvalidBucketName, "bucket name %q is reserved", ReservedBucketName)
	}
	if len(name) < 3 || len(name) > 63 {
		return s3err.Errorf(s3err.InvalidBucketName, "bucket name must be 3-63 characters long")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' || c == '.':
			if i == 0 || i == len(name)-1 {
				return s3err.Errorf(s3err.InvalidBucketName, "bucket name must start and end with a letter or digit")
			}
		default:
			return s3err.Errorf(s3err.InvalidBucketName, "bucket name contains invalid characters")
		}
	}
	if strings.Contains(name, "..") || strings.Contains(name, ".-") || strings.Contains(name, "-.") {
		return s3err.Errorf(s3err.InvalidBucketName, "bucket name contains an invalid label sequence")
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return s3err.Errorf(s3err.InvalidBucketName, "bucket name must not be an IP address")
	}
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(name, p) {
			return s3err.Errorf(s3err.InvalidBucketName, "bucket name uses the reserved prefix %q", p)
		}
	}
	for _, s := range reservedSuffixes {
		if strings.HasSuffix(name, s) {
			return s3err.Errorf(s3err.InvalidBucketName, "bucket name uses the reserved suffix %q", s)
		}
	}
	return nil
}

// ValidateKey checks an object key: non-empty, at most MaxKeyLength bytes,
// valid UTF-8, and free of NUL bytes.
func ValidateKey(key string) error {
	switch {
	case key == "":
		return s3err.Errorf(s3err.InvalidArgument, "object key must not be empty")
	case len(key) > MaxKeyLength:
		return s3err.Errorf(s3err.KeyTooLongError, "object key is longer than %d bytes", MaxKeyLength)
	case !utf8.ValidString(key):
		return s3err.Errorf(s3err.InvalidArgument, "object key is not valid UTF-8")
	case strings.IndexByte(key, 0) >= 0:
		return s3err.Errorf(s3err.InvalidArgument, "object key must not contain NUL")
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
		return s3err.Errorf(s3err.MetadataTooLarge, "user metadata exceeds %d bytes", MaxUserMetadataSize)
	}
	return nil
}
