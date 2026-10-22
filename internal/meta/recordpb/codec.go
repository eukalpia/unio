package recordpb

import (
	"bytes"
	"fmt"
	"math"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Error construction. The unexported converters return errors without the
// package prefix. The exported functions add "recordpb: " once, so nested
// errors read "recordpb: part 1: ..." and not with the prefix repeated.

// prefixErr adds the package prefix to err. It returns nil for nil.
func prefixErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("recordpb: %w", err)
}

// errNil reports a nil message passed to a FromProto or Unmarshal function.
func errNil(kind string) error {
	return fmt.Errorf("nil %s message", kind)
}

// uint32Of converts a domain int to its uint32 proto form. It rejects negative
// values and values above math.MaxUint32, and never truncates.
func uint32Of(field string, v int) (uint32, error) {
	if v < 0 || int64(v) > math.MaxUint32 {
		return 0, fmt.Errorf("%s %d is outside the uint32 range", field, v)
	}
	return uint32(v), nil
}

// intOf converts a proto uint32 to int. It only fails where int is narrower
// than 32 bits.
func intOf(field string, v uint32) (int, error) {
	if uint64(v) > math.MaxInt {
		return 0, fmt.Errorf("%s %d overflows int", field, v)
	}
	return int(v), nil
}

// nonNegative rejects a negative int64 field.
func nonNegative(field string, v int64) error {
	if v < 0 {
		return fmt.Errorf("%s %d is negative", field, v)
	}
	return nil
}

// cloneBytes returns a copy of b for a field where nil and empty are
// equivalent, so an empty b yields nil. Do not use it for Inline, where
// presence is significant.
func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return bytes.Clone(b)
}

// cloneMap returns a copy of m, or nil when m is empty.
func cloneMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// vtMarshaler and vtUnmarshaler are implemented by the vtprotobuf messages.
type vtMarshaler interface {
	MarshalVT() ([]byte, error)
}

type vtUnmarshaler interface {
	UnmarshalVT([]byte) error
}

// marshalVT encodes m. kind names the message in the error.
func marshalVT(kind string, m vtMarshaler) ([]byte, error) {
	b, err := m.MarshalVT()
	if err != nil {
		return nil, fmt.Errorf("recordpb: marshal %s: %w", kind, err)
	}
	return b, nil
}

// unmarshalVT decodes b into m. kind names the message in the error.
func unmarshalVT(kind string, m vtUnmarshaler, b []byte) error {
	if err := m.UnmarshalVT(b); err != nil {
		return fmt.Errorf("recordpb: unmarshal %s: %w", kind, err)
	}
	return nil
}

// MarshalObject encodes r with the wire codec. It returns an error if r cannot
// be represented (see ObjectToProto).
func MarshalObject(r meta.ObjectRecord) ([]byte, error) {
	p, err := ObjectToProto(r)
	if err != nil {
		return nil, err
	}
	return marshalVT("object", p)
}

// UnmarshalObject decodes an ObjectRecord from b. Inline presence is kept:
// a present but empty Inline decodes as a non-nil empty slice.
func UnmarshalObject(b []byte) (meta.ObjectRecord, error) {
	var p uniov1.ObjectRecord
	if err := unmarshalVT("object", &p, b); err != nil {
		return meta.ObjectRecord{}, err
	}
	return ObjectFromProto(&p)
}

// MarshalPart encodes p with the wire codec.
func MarshalPart(p meta.PartRecord) ([]byte, error) {
	m, err := PartToProto(p)
	if err != nil {
		return nil, err
	}
	return marshalVT("part", m)
}

// UnmarshalPart decodes a PartRecord from b.
func UnmarshalPart(b []byte) (meta.PartRecord, error) {
	var p uniov1.PartRecord
	if err := unmarshalVT("part", &p, b); err != nil {
		return meta.PartRecord{}, err
	}
	return PartFromProto(&p)
}

// MarshalUpload encodes u with the wire codec. Conditions are not stored.
func MarshalUpload(u meta.UploadRecord) ([]byte, error) {
	p, err := UploadToProto(u)
	if err != nil {
		return nil, err
	}
	return marshalVT("upload", p)
}

// UnmarshalUpload decodes an UploadRecord from b.
func UnmarshalUpload(b []byte) (meta.UploadRecord, error) {
	var p uniov1.UploadRecord
	if err := unmarshalVT("upload", &p, b); err != nil {
		return meta.UploadRecord{}, err
	}
	return UploadFromProto(&p)
}

// MarshalBucket encodes a bucket's info and deletion flag with the wire codec.
func MarshalBucket(info object.BucketInfo, deleting bool) ([]byte, error) {
	p, err := BucketToProto(info, deleting)
	if err != nil {
		return nil, err
	}
	return marshalVT("bucket", p)
}

// UnmarshalBucket decodes a bucket's info and deletion flag from b.
func UnmarshalBucket(b []byte) (object.BucketInfo, bool, error) {
	var p uniov1.BucketRecord
	if err := unmarshalVT("bucket", &p, b); err != nil {
		return object.BucketInfo{}, false, err
	}
	return BucketFromProto(&p)
}
