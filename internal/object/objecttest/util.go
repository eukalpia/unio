package objecttest

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// maxEmptyReads bounds consecutive (0, nil) reads from a body.
const maxEmptyReads = 100

// checkBucketKey validates a bucket name and an object key.
func checkBucketKey(bucket, key string) error {
	if err := object.ValidateBucketName(bucket); err != nil {
		return err
	}
	return object.ValidateKey(key)
}

// checkPutOptions validates the attributes of a new object version.
func checkPutOptions(opts object.PutOptions) error {
	if err := object.ValidateUserMetadata(opts.UserMetadata); err != nil {
		return err
	}
	if opts.ChecksumAlgorithm > object.ChecksumSHA256 {
		return s3err.Errorf(s3err.InvalidArgument, "unknown checksum algorithm")
	}
	return checkTags(opts.Tags)
}

func checkTags(tags string) error {
	if tags == "" {
		return nil
	}
	if n := strings.Count(tags, "&") + 1; n > object.MaxTags {
		return s3err.Errorf(s3err.InvalidTag, "more than %d tags", object.MaxTags)
	}
	return nil
}

// checkPartNumber validates a multipart part number.
func checkPartNumber(n int) error {
	if n < 1 || n > object.MaxPartNumber {
		return s3err.Errorf(s3err.InvalidArgument, "part number must be between 1 and %d", object.MaxPartNumber)
	}
	return nil
}

// checkListKeys resolves and validates a page size. Zero means the maximum.
func checkListKeys(n int) (int, error) {
	switch {
	case n == 0:
		return object.MaxListKeys, nil
	case n < 0 || n > object.MaxListKeys:
		return 0, s3err.Errorf(s3err.InvalidArgument, "max keys must be between 1 and %d", object.MaxListKeys)
	}
	return n, nil
}

// readBody reads exactly body.Size() bytes and requires the clean end that
// the object.Body contract promises. Nothing is returned unless the payload
// ended cleanly.
func readBody(body object.Body) (data []byte, md5sum [16]byte, ck object.Checksum, err error) {
	size := body.Size()
	if size < 0 {
		return nil, md5sum, ck, s3err.Errorf(s3err.InvalidArgument, "negative body size")
	}
	data = make([]byte, size)
	if _, err := io.ReadFull(body, data); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, md5sum, ck, s3err.Errorf(s3err.IncompleteBody, "body is shorter than declared")
		}
		return nil, md5sum, ck, fmt.Errorf("objecttest: read body: %w", err)
	}
	var one [1]byte
	for i := 0; ; i++ {
		n, rerr := body.Read(one[:])
		if n > 0 {
			return nil, md5sum, ck, s3err.Errorf(s3err.IncompleteBody, "body is longer than declared")
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, md5sum, ck, fmt.Errorf("objecttest: read body: %w", rerr)
		}
		if i >= maxEmptyReads {
			return nil, md5sum, ck, fmt.Errorf("objecttest: read body: %w", io.ErrNoProgress)
		}
	}
	return data, body.MD5(), body.Checksum(), nil
}

// md5Of returns the MD5 digest of data.
func md5Of(data []byte) [16]byte {
	return md5.Sum(data)
}

// newUploadID returns a random 32-character lower-case hex upload id.
func newUploadID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// listedKey is one entry of a delimited listing: an object key or a common
// prefix.
type listedKey struct {
	key      string
	isPrefix bool
}

// pageKeys applies prefix, delimiter, the exclusive after marker and the page
// size to a sorted key list. It returns the entries of the page and whether
// more entries follow.
func pageKeys(keys []string, prefix, delimiter, after string, max int) ([]listedKey, bool) {
	var out []listedKey
	lastPrefix := ""
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) || k <= after {
			continue
		}
		entry := listedKey{key: k}
		if delimiter != "" {
			rest := k[len(prefix):]
			if i := strings.Index(rest, delimiter); i >= 0 {
				cp := prefix + rest[:i+len(delimiter)]
				if cp == lastPrefix || strings.HasPrefix(after, cp) {
					continue
				}
				lastPrefix = cp
				entry = listedKey{key: cp, isPrefix: true}
			}
		}
		if len(out) == max {
			return out, true
		}
		out = append(out, entry)
	}
	return out, false
}
