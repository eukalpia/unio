package engine

import (
	"encoding/binary"
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
	raw := make([]byte, 0, digest.MD5Size*len(parts))
	for _, p := range parts {
		b, err := hex.DecodeString(p.ETag)
		if err != nil || len(b) != digest.MD5Size {
			return "", fmt.Errorf("engine: part %d has a malformed ETag", p.Number)
		}
		raw = append(raw, b...)
	}
	sum := digest.SumMD5(raw)
	return fmt.Sprintf("%s-%d", hex.EncodeToString(sum[:]), len(parts)), nil
}

// multipartChecksum returns the additional checksum of a multipart object. The
// parts must be in order, and each must carry a checksum of alg; the caller
// checks that. A zero checksum is returned when alg is none.
//
// CRC-64/NVME is a full-object checksum: S3 defines it over the whole object,
// and it is computed here by combining the part checksums. Every other
// algorithm is a composite: the checksum of the concatenated part checksums,
// with the part count.
func multipartChecksum(alg object.ChecksumAlgorithm, parts []meta.PartRecord) (object.Checksum, error) {
	switch alg {
	case object.ChecksumNone:
		return object.Checksum{}, nil
	case object.ChecksumCRC64NVME:
		return fullCRC64NVME(parts)
	}
	h, err := digest.New(alg)
	if err != nil {
		return object.Checksum{}, fmt.Errorf("engine: composite checksum: %w", err)
	}
	for _, p := range parts {
		_, _ = h.Write(p.Checksum.Value) // hash writes never fail
	}
	return object.Checksum{
		Algorithm: alg,
		Type:      object.ChecksumComposite,
		Value:     h.Sum(nil),
		Parts:     len(parts),
	}, nil
}

// fullCRC64NVME combines the CRC-64/NVME checksums of the parts, in order, into
// the full-object checksum.
func fullCRC64NVME(parts []meta.PartRecord) (object.Checksum, error) {
	if len(parts) == 0 {
		return object.Checksum{}, fmt.Errorf("engine: no parts to checksum")
	}
	var crc uint64
	for i, p := range parts {
		if len(p.Checksum.Value) != 8 {
			return object.Checksum{}, fmt.Errorf("engine: part %d has a malformed CRC-64/NVME", p.Number)
		}
		v := binary.BigEndian.Uint64(p.Checksum.Value)
		if i == 0 {
			crc = v
			continue
		}
		crc = digest.CombineCRC64NVME(crc, v, p.Size)
	}
	value := make([]byte, 8)
	binary.BigEndian.PutUint64(value, crc)
	return object.Checksum{
		Algorithm: object.ChecksumCRC64NVME,
		Type:      object.ChecksumFullObject,
		Value:     value,
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
