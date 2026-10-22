package engine

import (
	"fmt"
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// validateBucketKey checks a bucket name and an object key.
func validateBucketKey(bucket, key string) error {
	if err := object.ValidateBucketName(bucket); err != nil {
		return err
	}
	return object.ValidateKey(key)
}

// validateRef checks a copy source.
func validateRef(src object.Ref) error {
	return validateBucketKey(src.Bucket, src.Key)
}

// validatePutOptions checks the attributes of a new version.
func validatePutOptions(opts object.PutOptions) error {
	if err := object.ValidateUserMetadata(opts.UserMetadata); err != nil {
		return err
	}
	if opts.ChecksumAlgorithm > object.ChecksumSHA256 {
		return s3err.Errorf(s3err.InvalidArgument, "unknown checksum algorithm")
	}
	if _, err := object.DecodeTags(opts.Tags); err != nil {
		return err
	}
	return nil
}

// validatePartNumber checks a multipart part number.
func validatePartNumber(n int) error {
	if n < 1 || n > object.MaxPartNumber {
		return s3err.Errorf(s3err.InvalidArgument, "part number must be between 1 and %d", object.MaxPartNumber)
	}
	return nil
}

// validateSize checks a declared size against its limit.
func validateSize(size, limit int64, code s3err.Code) error {
	switch {
	case size < 0:
		return s3err.Errorf(s3err.InvalidArgument, "negative size")
	case size > limit:
		return s3err.Errorf(code, "size %d exceeds the limit of %d bytes", size, limit)
	}
	return nil
}

// resolveLimit returns the page size for a listing: zero selects the
// maximum, and values outside [1, MaxListKeys] are errors.
func resolveLimit(n int) (int, error) {
	switch {
	case n == 0:
		return object.MaxListKeys, nil
	case n < 0 || n > object.MaxListKeys:
		return 0, s3err.Errorf(s3err.InvalidArgument, "max keys must be between 1 and %d", object.MaxListKeys)
	}
	return n, nil
}

// validateUploadID rejects ids that cannot name an upload. Such ids are
// reported as NoSuchUpload, as S3 does for unknown ids.
func validateUploadID(id string) error {
	if len(id) != 32 {
		return s3err.Errorf(s3err.NoSuchUpload, "no such upload")
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return s3err.Errorf(s3err.NoSuchUpload, "no such upload")
		}
	}
	return nil
}

// normalizeETag removes the quotes a client may put around an ETag.
func normalizeETag(etag string) string {
	return strings.Trim(etag, `"`)
}

// checkPartNumberOpt validates GetOptions.PartNumber, where zero means that no
// part was requested.
func checkPartNumberOpt(n int) error {
	if n == 0 {
		return nil
	}
	return validatePartNumber(n)
}

// checkBody rejects a missing request body. A nil body is a caller error, and
// the engine must not panic on it.
func checkBody(body object.Body) error {
	if body == nil {
		return s3err.Errorf(s3err.InvalidArgument, "missing request body")
	}
	return nil
}

// wrapCause adds context to an error without hiding its S3 code.
func wrapCause(what string, err error) error {
	return fmt.Errorf("engine: %s: %w", what, err)
}
