package recordpb

import (
	"fmt"

	"github.com/eukalpia/unio/internal/object"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Checksum conversions. A nil Value and an empty Value are equivalent: both
// encode to an empty field, and decoding an empty Value yields nil. Value is
// copied in both directions. Parts must fit in uint32 and must not be
// negative. Unknown algorithm and type values are errors in both directions.

// ChecksumToProto converts c to its message. It returns an error for an
// unknown algorithm or type, a negative Parts, or a Parts above math.MaxUint32.
func ChecksumToProto(c object.Checksum) (*uniov1.Checksum, error) {
	p, err := checksumToProto(c)
	return p, prefixErr(err)
}

// ChecksumFromProto converts p to a checksum. It returns an error for a nil
// message or an unknown enum value.
func ChecksumFromProto(p *uniov1.Checksum) (object.Checksum, error) {
	c, err := checksumFromProto(p)
	return c, prefixErr(err)
}

func checksumToProto(c object.Checksum) (*uniov1.Checksum, error) {
	alg, err := checksumAlgorithmToProto(c.Algorithm)
	if err != nil {
		return nil, err
	}
	typ, err := checksumTypeToProto(c.Type)
	if err != nil {
		return nil, err
	}
	parts, err := uint32Of("checksum parts", c.Parts)
	if err != nil {
		return nil, err
	}
	return &uniov1.Checksum{
		Algorithm: alg,
		Type:      typ,
		Value:     cloneBytes(c.Value),
		Parts:     parts,
	}, nil
}

func checksumFromProto(p *uniov1.Checksum) (object.Checksum, error) {
	if p == nil {
		return object.Checksum{}, errNil("checksum")
	}
	alg, err := checksumAlgorithmFromProto(p.GetAlgorithm())
	if err != nil {
		return object.Checksum{}, err
	}
	typ, err := checksumTypeFromProto(p.GetType())
	if err != nil {
		return object.Checksum{}, err
	}
	parts, err := intOf("checksum parts", p.GetParts())
	if err != nil {
		return object.Checksum{}, err
	}
	return object.Checksum{
		Algorithm: alg,
		Type:      typ,
		Value:     cloneBytes(p.GetValue()),
		Parts:     parts,
	}, nil
}

// checksumFromOptional decodes a nested checksum. An absent message decodes
// as the zero checksum.
func checksumFromOptional(p *uniov1.Checksum) (object.Checksum, error) {
	if p == nil {
		return object.Checksum{}, nil
	}
	return checksumFromProto(p)
}

func checksumAlgorithmToProto(a object.ChecksumAlgorithm) (uniov1.ChecksumAlgorithm, error) {
	switch a {
	case object.ChecksumNone:
		return uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_NONE, nil
	case object.ChecksumCRC32:
		return uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32, nil
	case object.ChecksumCRC32C:
		return uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C, nil
	case object.ChecksumCRC64NVME:
		return uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC64NVME, nil
	case object.ChecksumSHA1:
		return uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA1, nil
	case object.ChecksumSHA256:
		return uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256, nil
	}
	return 0, fmt.Errorf("unknown checksum algorithm %d", a)
}

func checksumAlgorithmFromProto(a uniov1.ChecksumAlgorithm) (object.ChecksumAlgorithm, error) {
	switch a {
	case uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_NONE:
		return object.ChecksumNone, nil
	case uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32:
		return object.ChecksumCRC32, nil
	case uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C:
		return object.ChecksumCRC32C, nil
	case uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC64NVME:
		return object.ChecksumCRC64NVME, nil
	case uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA1:
		return object.ChecksumSHA1, nil
	case uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256:
		return object.ChecksumSHA256, nil
	}
	return 0, fmt.Errorf("unknown checksum algorithm %d", a)
}

func checksumTypeToProto(t object.ChecksumType) (uniov1.ChecksumType, error) {
	switch t {
	case object.ChecksumFullObject:
		return uniov1.ChecksumType_CHECKSUM_TYPE_FULL_OBJECT, nil
	case object.ChecksumComposite:
		return uniov1.ChecksumType_CHECKSUM_TYPE_COMPOSITE, nil
	}
	return 0, fmt.Errorf("unknown checksum type %d", t)
}

func checksumTypeFromProto(t uniov1.ChecksumType) (object.ChecksumType, error) {
	switch t {
	case uniov1.ChecksumType_CHECKSUM_TYPE_FULL_OBJECT:
		return object.ChecksumFullObject, nil
	case uniov1.ChecksumType_CHECKSUM_TYPE_COMPOSITE:
		return object.ChecksumComposite, nil
	}
	return 0, fmt.Errorf("unknown checksum type %d", t)
}
