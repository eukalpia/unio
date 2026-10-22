// Package recordpb converts between the metadata domain types and the
// protobuf messages that store them.
//
// The domain side is meta.ObjectRecord, meta.PartRecord and
// meta.UploadRecord, together with object.BucketInfo, object.Checksum,
// erasure.Layout and the ids types they use. The wire side is the messages in
// internal/pb/unio/v1, generated from api/proto/unio/v1/records.proto. This
// package does not change the schema; schema changes need an ADR (see
// docs/adr/0006-protobuf-encoding.md).
//
// Each entity has a ToProto and a FromProto function. The Marshal and
// Unmarshal functions add the wire codec on top of them.
//
// # Presence and equivalence
//
// ObjectRecord.Inline is the only field where nil and empty differ. A nil
// Inline means the object is not stored inline. A non-nil Inline, including an
// empty one, means it is. Presence survives both the in-memory conversion and
// the wire codec. Tests must compare Inline exactly, never with
// cmpopts.EquateEmpty.
//
// Every other byte slice, slice and map (UserMetadata, Checksum.Value, the
// Drives list, Parts) is equivalent when nil or empty. Converters copy them,
// and decoding an empty one yields nil.
//
// # Nested messages
//
// A nested message that is absent on the wire (Checksum, Attributes, Layout)
// decodes as the zero value of its domain type. Converters always write these
// messages, so absence only occurs in records written by other tools. A nil
// top-level argument to a FromProto or Unmarshal function is an error, and so
// is a nil entry in a repeated message field.
//
// # Integers, times and ids
//
// Values that do not fit their proto type are errors on encode: negative sizes,
// negative shard counts or stripe sizes, negative part numbers, and int values
// outside the uint32 range. Decode applies the same checks, so every record
// that decodes can be encoded again.
//
// Times are int64 Unix nanoseconds. See time.go for the zero-time rule and the
// representable range. A DataID is always 16 bytes. A drive entry is either
// zero-length (zero DriveID) or 16 bytes. Any other length is an error.
//
// Unknown enum values are errors in both directions.
//
// # Strings
//
// The codec does not check UTF-8 (vtprotobuf does not), so strings round-trip
// byte for byte, including bytes that are not valid UTF-8. HTTP header values
// can carry such bytes. Strictly, proto3 requires UTF-8 in string fields, so
// other protobuf implementations may reject these records.
//
// # Wire codec
//
// All messages use the vtprotobuf MarshalVT and UnmarshalVT methods. They keep
// the presence of proto3 optional bytes: a zero-length Inline decodes to a
// non-nil empty slice. The reflection codec (google.golang.org/protobuf/proto)
// does the same, and TestInlinePresenceOnWire checks both, so the reflection
// codec was not needed.
package recordpb
