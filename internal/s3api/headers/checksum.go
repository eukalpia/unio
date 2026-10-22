package headers

import (
	"net/http"
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

const (
	// checksumHeaderPrefix starts the digest, algorithm and mode headers.
	checksumHeaderPrefix = "x-amz-checksum-"
	// maxTrailerNames bounds the number of names in x-amz-trailer.
	maxTrailerNames = 64
	// contentMD5Len is the size of a Content-MD5 digest in bytes.
	contentMD5Len = 16
)

// ChecksumRequest is the additional-checksum request of an upload.
type ChecksumRequest struct {
	// Algorithm is the requested algorithm, object.ChecksumNone when absent.
	Algorithm object.ChecksumAlgorithm
	// Expected is the digest supplied in an x-amz-checksum-<algo> header (raw bytes,
	// base64 decoded), or nil when the digest arrives later (trailer) or is absent.
	Expected []byte
	// TrailerDeclared is true when x-amz-trailer names an x-amz-checksum-<algo> trailer.
	// The client then promises that the trailing checksum arrives after the body, so the
	// body is only valid if it does (see payload.Spec.TrailerRequired). Expected may be nil
	// while TrailerDeclared is true.
	TrailerDeclared bool
}

// ParseChecksumRequest collects the checksum request from the headers:
//   - x-amz-checksum-<algo> (crc32, crc32c, crc64nvme, sha1, sha256; names are
//     case-insensitive) carries a base64 digest. Its length must match the algorithm
//     (CRC32/CRC32C 4 bytes, CRC64NVME 8, SHA1 20, SHA256 32); malformed base64 or a wrong
//     length returns InvalidRequest. More than one digest header returns InvalidRequest.
//   - x-amz-sdk-checksum-algorithm and x-amz-checksum-algorithm (CreateMultipartUpload)
//     name the algorithm without a digest. Names are upper case as S3 sends them
//     (object.ParseChecksumAlgorithm); unknown names return InvalidRequest.
//   - x-amz-trailer lists trailer names (comma separated). A name of the form
//     x-amz-checksum-<algo> selects that algorithm and sets TrailerDeclared (aws-chunked
//     uploads, where the digest is verified later from the trailer).
//
// All sources that name an algorithm must agree; a disagreement returns InvalidRequest.
// A digest header that names an algorithm different from the others returns InvalidRequest.
// Absent everything, it returns the zero value and nil.
//
// The x-amz-checksum-mode and x-amz-checksum-type headers are not digests and
// are ignored here.
func ParseChecksumRequest(h http.Header) (ChecksumRequest, error) {
	var req ChecksumRequest
	digestSeen := false
	for key, vals := range h {
		if !hasPrefixFold(key, checksumHeaderPrefix) {
			continue
		}
		suffix := strings.ToLower(key[len(checksumHeaderPrefix):])
		switch suffix {
		case "algorithm", "mode", "type":
			continue
		}
		algo, ok := digestAlgorithm(suffix)
		if !ok {
			return ChecksumRequest{}, s3err.Errorf(s3err.InvalidRequest, "unsupported checksum header")
		}
		if digestSeen {
			return ChecksumRequest{}, s3err.Errorf(s3err.InvalidRequest, "more than one checksum digest")
		}
		digestSeen = true
		if len(vals) != 1 {
			return ChecksumRequest{}, s3err.Errorf(s3err.InvalidRequest, "checksum digest must not be repeated")
		}
		digest, ok := decodeBase64Exact(vals[0], digestLen(algo))
		if !ok {
			return ChecksumRequest{}, s3err.Errorf(s3err.InvalidRequest, "checksum digest is malformed")
		}
		if err := agreeAlgorithm(&req.Algorithm, algo); err != nil {
			return ChecksumRequest{}, err
		}
		req.Expected = digest
	}

	for _, name := range [...]string{"X-Amz-Sdk-Checksum-Algorithm", "X-Amz-Checksum-Algorithm"} {
		v, ok, err := singleHeader(h, name, s3err.InvalidRequest)
		if err != nil {
			return ChecksumRequest{}, err
		}
		if !ok {
			continue
		}
		algo, ok := object.ParseChecksumAlgorithm(v)
		if !ok {
			return ChecksumRequest{}, s3err.Errorf(s3err.InvalidRequest, "unsupported checksum algorithm")
		}
		if err := agreeAlgorithm(&req.Algorithm, algo); err != nil {
			return ChecksumRequest{}, err
		}
	}

	// Repeated trailer lines form one list, so they are joined.
	if trailers := strings.Join(h.Values("X-Amz-Trailer"), ","); trailers != "" {
		if err := applyTrailerNames(trailers, &req); err != nil {
			return ChecksumRequest{}, err
		}
	}
	return req, nil
}

// applyTrailerNames applies the checksum names of a comma-separated trailer
// list to req, and records a promised checksum trailer in req.TrailerDeclared.
// Empty list elements are ignored, as RFC 9110 requires.
func applyTrailerNames(list string, req *ChecksumRequest) error {
	count := 0
	for {
		name, rest, more := strings.Cut(list, ",")
		if name = strings.TrimSpace(name); name != "" {
			count++
			if count > maxTrailerNames {
				return s3err.Errorf(s3err.InvalidRequest, "x-amz-trailer lists too many names")
			}
			if lower := strings.ToLower(name); strings.HasPrefix(lower, checksumHeaderPrefix) {
				a, ok := digestAlgorithm(lower[len(checksumHeaderPrefix):])
				if !ok {
					return s3err.Errorf(s3err.InvalidRequest, "unsupported checksum trailer")
				}
				if err := agreeAlgorithm(&req.Algorithm, a); err != nil {
					return err
				}
				req.TrailerDeclared = true
			}
		}
		if !more {
			return nil
		}
		list = rest
	}
}

// agreeAlgorithm sets *cur to next, or fails when *cur already names a
// different algorithm.
func agreeAlgorithm(cur *object.ChecksumAlgorithm, next object.ChecksumAlgorithm) error {
	if *cur != object.ChecksumNone && *cur != next {
		return s3err.Errorf(s3err.InvalidRequest, "checksum algorithms disagree")
	}
	*cur = next
	return nil
}

// digestAlgorithm maps the lower-case suffix of an x-amz-checksum-<algo> name
// to its algorithm.
func digestAlgorithm(suffix string) (object.ChecksumAlgorithm, bool) {
	switch suffix {
	case "crc32":
		return object.ChecksumCRC32, true
	case "crc32c":
		return object.ChecksumCRC32C, true
	case "crc64nvme":
		return object.ChecksumCRC64NVME, true
	case "sha1":
		return object.ChecksumSHA1, true
	case "sha256":
		return object.ChecksumSHA256, true
	}
	return object.ChecksumNone, false
}

// digestLen returns the raw digest size in bytes of a checksum algorithm, or
// 0 for ChecksumNone.
func digestLen(a object.ChecksumAlgorithm) int {
	switch a {
	case object.ChecksumCRC32, object.ChecksumCRC32C:
		return 4
	case object.ChecksumCRC64NVME:
		return 8
	case object.ChecksumSHA1:
		return 20
	case object.ChecksumSHA256:
		return 32
	}
	return 0
}

// ParseChecksumMode reports whether x-amz-checksum-mode is "ENABLED"
// (case-insensitive).
func ParseChecksumMode(h http.Header) bool {
	return strings.EqualFold(strings.TrimSpace(h.Get("X-Amz-Checksum-Mode")), "ENABLED")
}

// ParseContentMD5 returns the 16-byte digest of the Content-MD5 header, or nil
// when the header is absent. Malformed base64, a length other than 16, or a
// repeated header returns InvalidDigest.
func ParseContentMD5(h http.Header) ([]byte, error) {
	vals := h.Values("Content-MD5")
	switch len(vals) {
	case 0:
		return nil, nil
	case 1:
	default:
		return nil, s3err.Errorf(s3err.InvalidDigest, "Content-MD5 must not be repeated")
	}
	sum, ok := decodeBase64Exact(vals[0], contentMD5Len)
	if !ok {
		return nil, s3err.Errorf(s3err.InvalidDigest, "Content-MD5 is malformed")
	}
	return sum, nil
}
