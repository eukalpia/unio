package engine

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine/digest"
)

// hexOf returns the lower-case hex form of an MD5 digest. For a single-part
// object this is its ETag.
func hexOf(sum [16]byte) string {
	return hex.EncodeToString(sum[:])
}

// multipartETag returns the ETag of a multipart object: the MD5 of the binary
// MD5 digests of its parts, followed by "-" and the number of parts. The parts
// must be in order.
func multipartETag(parts []meta.PartRecord) (string, error) {
	raw := make([]byte, 0, md5.Size*len(parts))
	for _, p := range parts {
		b, err := hex.DecodeString(p.ETag)
		if err != nil || len(b) != md5.Size {
			return "", fmt.Errorf("engine: part %d has a malformed ETag", p.Number)
		}
		raw = append(raw, b...)
	}
	sum := md5.Sum(raw)
	return fmt.Sprintf("%s-%d", hex.EncodeToString(sum[:]), len(parts)), nil
}

// compositeChecksum returns the composite checksum of a multipart object for
// alg: the checksum of the concatenated part checksums, with the part count.
// Every part must carry a checksum of alg; the caller checks that. A zero
// checksum is returned when alg is none.
func compositeChecksum(alg object.ChecksumAlgorithm, parts []meta.PartRecord) (object.Checksum, error) {
	if alg == object.ChecksumNone {
		return object.Checksum{}, nil
	}
	h, err := digest.New(alg)
	if err != nil {
		return object.Checksum{}, fmt.Errorf("engine: composite checksum: %w", err)
	}
	for _, p := range parts {
		_, _ = h.Write(p.Checksum.Value)
	}
	return object.Checksum{
		Algorithm: alg,
		Type:      object.ChecksumComposite,
		Value:     h.Sum(nil),
		Parts:     len(parts),
	}, nil
}

// cloneMap returns a copy of m, or nil when m is nil. Records never share
// maps with callers.
func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
