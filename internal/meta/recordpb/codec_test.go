package recordpb

import (
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/proto"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// namedObject, namedPart, namedUpload and namedBucket pair a fixture with a
// name for t.Run.
type namedObject struct {
	name string
	rec  meta.ObjectRecord
}

type namedPart struct {
	name string
	rec  meta.PartRecord
}

type namedUpload struct {
	name string
	rec  meta.UploadRecord
}

type namedBucket struct {
	name     string
	info     object.BucketInfo
	deleting bool
}

// equivOpts applies the codec equivalence rules (doc.go): nil and empty
// slices and maps compare equal, except ObjectRecord.Inline, where presence
// is significant and must be compared exactly.
func equivOpts() cmp.Option {
	return cmp.FilterPath(func(p cmp.Path) bool {
		last := p.Last()
		return last == nil || last.String() != ".Inline"
	}, cmpopts.EquateEmpty())
}

// fixtureTime is a non-zero instant with sub-second precision and a
// non-UTC location. Round trips return the same instant in UTC.
func fixtureTime() time.Time {
	return time.Date(2025, 3, 4, 5, 6, 7, 123456789, time.FixedZone("CET", 3600))
}

// fixtureDataID returns a DataID with every byte set to b.
func fixtureDataID(b byte) ids.DataID {
	var id ids.DataID
	for i := range id {
		id[i] = b
	}
	return id
}

// fixtureDriveID returns a non-zero DriveID with every byte set to b.
func fixtureDriveID(b byte) ids.DriveID {
	var id ids.DriveID
	for i := range id {
		id[i] = b
	}
	return id
}

func fixtureChecksum() object.Checksum {
	return object.Checksum{
		Algorithm: object.ChecksumCRC64NVME,
		Type:      object.ChecksumComposite,
		Value:     []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		Parts:     3,
	}
}

func fixtureLayout() erasure.Layout {
	return erasure.Layout{
		Set:          7,
		DataShards:   4,
		ParityShards: 2,
		StripeSize:   erasure.DefaultStripeSize,
		Drives: []ids.DriveID{
			fixtureDriveID(1), {}, fixtureDriveID(3),
			fixtureDriveID(4), fixtureDriveID(5), fixtureDriveID(6),
		},
	}
}

func fixturePart(number int) meta.PartRecord {
	return meta.PartRecord{
		Number:   number,
		Size:     5 << 20,
		ETag:     "9bb58f26192e4ba00f01e2e7b136bbd8",
		Checksum: fixtureChecksum(),
		ModTime:  fixtureTime(),
		Data:     fixtureDataID(byte(number)),
		Layout:   fixtureLayout(),
	}
}

func fixtureFullObject() meta.ObjectRecord {
	return meta.ObjectRecord{
		Bucket:             "photos",
		Key:                "2025/cat.jpg",
		VersionID:          "v-123",
		ModTime:            fixtureTime(),
		Size:               10,
		ETag:               "abc123",
		ContentType:        "image/jpeg",
		ContentEncoding:    "gzip",
		ContentDisposition: `attachment; filename="cat.jpg"`,
		ContentLanguage:    "en",
		CacheControl:       "max-age=60",
		Expires:            "Wed, 21 Oct 2026 07:28:00 GMT",
		UserMetadata:       map[string]string{"color": "orange", "empty": ""},
		Tags:               "a=b&c=d",
		Checksum:           fixtureChecksum(),
		StorageClass:       "STANDARD",
	}
}

func withInline(r meta.ObjectRecord, b []byte) meta.ObjectRecord {
	r.Inline = b
	return r
}

// objectFixtures covers the presence and equivalence cases of ObjectRecord.
func objectFixtures() []namedObject {
	multipart := fixtureFullObject()
	multipart.Multipart = true
	multipart.Parts = []meta.PartRecord{fixturePart(1), fixturePart(2)}

	return []namedObject{
		{"zero value", meta.ObjectRecord{}},
		{"full metadata, Inline absent", fixtureFullObject()},
		{"inline, present but empty", withInline(fixtureFullObject(), []byte{})},
		{"inline, data", withInline(fixtureFullObject(), []byte("hello world"))},
		{"multipart with parts", multipart},
		{"delete marker", meta.ObjectRecord{Bucket: "b", Key: "k", VersionID: "v2", ModTime: fixtureTime(), DeleteMarker: true}},
		{"non-UTF-8 metadata", meta.ObjectRecord{Bucket: "b", Key: "k", UserMetadata: map[string]string{"raw": "\xff\xfe"}}},
	}
}

// partFixtures covers PartRecord values, including a zero DataID.
func partFixtures() []namedPart {
	return []namedPart{
		{"zero value", meta.PartRecord{}},
		{"full part", fixturePart(3)},
		{"part with zero data id", meta.PartRecord{Number: 1, Size: 1, Data: ids.DataID{}}},
	}
}

// uploadFixtures covers UploadRecord values. The conditional upload has
// Conditions set, which are not stored.
func uploadFixtures() []namedUpload {
	opts := object.PutOptions{
		ContentType:        "application/octet-stream",
		ContentEncoding:    "identity",
		ContentDisposition: "inline",
		ContentLanguage:    "fr",
		CacheControl:       "no-store",
		Expires:            "Thu, 01 Jan 2026 00:00:00 GMT",
		UserMetadata:       map[string]string{"owner": "alice"},
		Tags:               "env=prod",
		StorageClass:       "STANDARD_IA",
		ChecksumAlgorithm:  object.ChecksumSHA256,
	}
	conditional := opts
	conditional.Conditions = object.Conditions{IfMatch: "abc", IfUnmodifiedSince: fixtureTime()}

	return []namedUpload{
		{"zero value", meta.UploadRecord{}},
		{"full options", meta.UploadRecord{Bucket: "photos", Key: "big.bin", UploadID: "u-1", Initiated: fixtureTime(), Options: opts}},
		{"conditions are dropped", meta.UploadRecord{Bucket: "photos", Key: "big.bin", UploadID: "u-2", Initiated: fixtureTime(), Options: conditional}},
	}
}

// bucketFixtures covers every versioning state with and without deletion.
func bucketFixtures() []namedBucket {
	return []namedBucket{
		{"zero value", object.BucketInfo{}, false},
		{"unversioned", object.BucketInfo{Name: "photos", ID: 1, Created: fixtureTime(), Owner: "alice", Versioning: object.VersioningUnversioned}, false},
		{"enabled", object.BucketInfo{Name: "photos", ID: 2, Created: fixtureTime(), Owner: "alice", Versioning: object.VersioningEnabled}, false},
		{"suspended and deleting", object.BucketInfo{Name: "logs", ID: ^ids.BucketID(0), Created: fixtureTime(), Owner: "ops", Versioning: object.VersioningSuspended}, true},
	}
}

// TestInlinePresenceOnWire records the wire-codec decision (doc.go). Both
// vtprotobuf and the reflection codec must keep "present but empty"
// distinct from "absent" for proto3 optional bytes.
func TestInlinePresenceOnWire(t *testing.T) {
	tests := []struct {
		name        string
		inline      []byte
		wantPresent bool
	}{
		{"absent", nil, false},
		{"present and empty", []byte{}, true},
		{"present with data", []byte("x"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &uniov1.ObjectRecord{Bucket: "b", Inline: tt.inline}

			vtBytes, err := src.MarshalVT()
			if err != nil {
				t.Fatalf("MarshalVT: %v", err)
			}
			var vt uniov1.ObjectRecord
			if err := vt.UnmarshalVT(vtBytes); err != nil {
				t.Fatalf("vtprotobuf UnmarshalVT: %v", err)
			}
			if got := vt.Inline != nil; got != tt.wantPresent {
				t.Errorf("vtprotobuf: Inline present = %v, want %v", got, tt.wantPresent)
			}

			reflBytes, err := proto.Marshal(src)
			if err != nil {
				t.Fatalf("proto.Marshal: %v", err)
			}
			var rf uniov1.ObjectRecord
			if err := proto.Unmarshal(reflBytes, &rf); err != nil {
				t.Fatalf("proto.Unmarshal: %v", err)
			}
			if got := rf.Inline != nil; got != tt.wantPresent {
				t.Errorf("reflection: Inline present = %v, want %v", got, tt.wantPresent)
			}
		})
	}
}

func TestCodecObjectRoundTrip(t *testing.T) {
	for _, nc := range objectFixtures() {
		t.Run(nc.name, func(t *testing.T) {
			b, err := MarshalObject(nc.rec)
			if err != nil {
				t.Fatalf("MarshalObject: %v", err)
			}
			got, err := UnmarshalObject(b)
			if err != nil {
				t.Fatalf("UnmarshalObject: %v", err)
			}
			if diff := cmp.Diff(nc.rec, got, equivOpts()); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
			if (nc.rec.Inline == nil) != (got.Inline == nil) {
				t.Errorf("Inline presence changed: want nil=%v, got nil=%v", nc.rec.Inline == nil, got.Inline == nil)
			}
		})
	}
}

func TestCodecObjectInlinePresence(t *testing.T) {
	tests := []struct {
		name   string
		inline []byte
	}{
		{"nil stays absent", nil},
		{"empty stays present", []byte{}},
		{"data stays present", []byte("payload")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := withInline(fixtureFullObject(), tt.inline)
			b, err := MarshalObject(want)
			if err != nil {
				t.Fatalf("MarshalObject: %v", err)
			}
			got, err := UnmarshalObject(b)
			if err != nil {
				t.Fatalf("UnmarshalObject: %v", err)
			}
			if (got.Inline == nil) != (tt.inline == nil) {
				t.Fatalf("Inline presence: got nil=%v, want nil=%v", got.Inline == nil, tt.inline == nil)
			}
			if diff := cmp.Diff(want, got, equivOpts()); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCodecPartRoundTrip(t *testing.T) {
	for _, np := range partFixtures() {
		t.Run(np.name, func(t *testing.T) {
			b, err := MarshalPart(np.rec)
			if err != nil {
				t.Fatalf("MarshalPart: %v", err)
			}
			got, err := UnmarshalPart(b)
			if err != nil {
				t.Fatalf("UnmarshalPart: %v", err)
			}
			if diff := cmp.Diff(np.rec, got, equivOpts()); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCodecUploadRoundTrip(t *testing.T) {
	for _, nu := range uploadFixtures() {
		t.Run(nu.name, func(t *testing.T) {
			b, err := MarshalUpload(nu.rec)
			if err != nil {
				t.Fatalf("MarshalUpload: %v", err)
			}
			got, err := UnmarshalUpload(b)
			if err != nil {
				t.Fatalf("UnmarshalUpload: %v", err)
			}
			want := nu.rec
			want.Options.Conditions = object.Conditions{}
			if diff := cmp.Diff(want, got, equivOpts()); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCodecBucketRoundTrip(t *testing.T) {
	for _, nb := range bucketFixtures() {
		t.Run(nb.name, func(t *testing.T) {
			b, err := MarshalBucket(nb.info, nb.deleting)
			if err != nil {
				t.Fatalf("MarshalBucket: %v", err)
			}
			info, deleting, err := UnmarshalBucket(b)
			if err != nil {
				t.Fatalf("UnmarshalBucket: %v", err)
			}
			if diff := cmp.Diff(nb.info, info, equivOpts()); diff != "" {
				t.Errorf("info mismatch (-want +got):\n%s", diff)
			}
			if deleting != nb.deleting {
				t.Errorf("deleting = %v, want %v", deleting, nb.deleting)
			}
		})
	}
}

// TestUnmarshalMalformedInput checks that every decoder rejects input that
// is not well-formed protobuf wire data.
func TestUnmarshalMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
	}{
		{"invalid varint tag", []byte{0xff, 0xff, 0xff}},
		{"length-delimited field longer than input", []byte{0x0a, 0x05, 'a'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := UnmarshalObject(tt.in); err == nil {
				t.Error("UnmarshalObject: want error")
			}
			if _, err := UnmarshalPart(tt.in); err == nil {
				t.Error("UnmarshalPart: want error")
			}
			if _, err := UnmarshalUpload(tt.in); err == nil {
				t.Error("UnmarshalUpload: want error")
			}
			if _, _, err := UnmarshalBucket(tt.in); err == nil {
				t.Error("UnmarshalBucket: want error")
			}
		})
	}
}

// TestUnmarshalTruncatedObject drops the last byte of a valid object. The
// final field is the non-empty Checksum message, so its declared length now
// exceeds the input.
func TestUnmarshalTruncatedObject(t *testing.T) {
	b, err := MarshalObject(fixtureFullObject())
	if err != nil {
		t.Fatalf("MarshalObject: %v", err)
	}
	if _, err := UnmarshalObject(b[:len(b)-1]); err == nil {
		t.Error("UnmarshalObject of truncated input: want error")
	}
}

// TestEmptyFieldsDecodeNil checks the nil/empty rule in doc.go: an empty
// byte field, list or map decodes to nil, and only Inline keeps presence.
func TestEmptyFieldsDecodeNil(t *testing.T) {
	cs, err := ChecksumFromProto(&uniov1.Checksum{Algorithm: uniov1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32, Value: []byte{}})
	if err != nil {
		t.Fatalf("ChecksumFromProto: %v", err)
	}
	if cs.Value != nil {
		t.Errorf("Checksum.Value = %v, want nil", cs.Value)
	}

	layout, err := LayoutFromProto(&uniov1.ErasureLayout{Set: 1, Drives: [][]byte{}})
	if err != nil {
		t.Fatalf("LayoutFromProto: %v", err)
	}
	if layout.Drives != nil {
		t.Errorf("Layout.Drives = %v, want nil", layout.Drives)
	}

	obj, err := ObjectFromProto(&uniov1.ObjectRecord{
		Parts:      []*uniov1.PartRecord{},
		Attributes: &uniov1.ObjectAttributes{UserMetadata: map[string]string{}},
	})
	if err != nil {
		t.Fatalf("ObjectFromProto: %v", err)
	}
	if obj.Parts != nil || obj.UserMetadata != nil {
		t.Errorf("Parts = %v, UserMetadata = %v, want both nil", obj.Parts, obj.UserMetadata)
	}
	if obj.Inline != nil {
		t.Errorf("Inline = %v, want nil when absent", obj.Inline)
	}
}

// TestUnmarshalWrongWireType checks that a known field with the wrong wire
// type is rejected rather than ignored. Field 1 of ObjectRecord is a string.
func TestUnmarshalWrongWireType(t *testing.T) {
	if _, err := UnmarshalObject([]byte{0x08, 0x01}); err == nil {
		t.Error("UnmarshalObject: want error for varint in string field")
	}
}

// FuzzUnmarshalObject checks that UnmarshalObject never panics, and that a
// record it accepts re-encodes and decodes to an equal value.
func FuzzUnmarshalObject(f *testing.F) {
	for _, nc := range objectFixtures() {
		b, err := MarshalObject(nc.rec)
		if err != nil {
			f.Fatalf("seed %s: %v", nc.name, err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		rec, err := UnmarshalObject(b)
		if err != nil {
			return
		}
		enc, err := MarshalObject(rec)
		if err != nil {
			t.Fatalf("re-encode of accepted record: %v", err)
		}
		again, err := UnmarshalObject(enc)
		if err != nil {
			t.Fatalf("decode of re-encoded record: %v", err)
		}
		if diff := cmp.Diff(rec, again, equivOpts()); diff != "" {
			t.Errorf("round trip changed record (-want +got):\n%s", diff)
		}
	})
}

// FuzzUnmarshalPart checks that UnmarshalPart never panics, and that a record
// it accepts re-encodes and decodes to an equal value.
func FuzzUnmarshalPart(f *testing.F) {
	for _, np := range partFixtures() {
		b, err := MarshalPart(np.rec)
		if err != nil {
			f.Fatalf("seed %s: %v", np.name, err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		rec, err := UnmarshalPart(b)
		if err != nil {
			return
		}
		enc, err := MarshalPart(rec)
		if err != nil {
			t.Fatalf("re-encode of accepted record: %v", err)
		}
		again, err := UnmarshalPart(enc)
		if err != nil {
			t.Fatalf("decode of re-encoded record: %v", err)
		}
		if diff := cmp.Diff(rec, again, equivOpts()); diff != "" {
			t.Errorf("round trip changed record (-want +got):\n%s", diff)
		}
	})
}

// FuzzUnmarshalUpload checks that UnmarshalUpload never panics, and that a
// record it accepts re-encodes and decodes to an equal value.
func FuzzUnmarshalUpload(f *testing.F) {
	for _, nu := range uploadFixtures() {
		b, err := MarshalUpload(nu.rec)
		if err != nil {
			f.Fatalf("seed %s: %v", nu.name, err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		rec, err := UnmarshalUpload(b)
		if err != nil {
			return
		}
		enc, err := MarshalUpload(rec)
		if err != nil {
			t.Fatalf("re-encode of accepted record: %v", err)
		}
		again, err := UnmarshalUpload(enc)
		if err != nil {
			t.Fatalf("decode of re-encoded record: %v", err)
		}
		if diff := cmp.Diff(rec, again, equivOpts()); diff != "" {
			t.Errorf("round trip changed record (-want +got):\n%s", diff)
		}
	})
}

// FuzzUnmarshalBucket checks that UnmarshalBucket never panics, and that a
// record it accepts re-encodes and decodes to an equal value.
func FuzzUnmarshalBucket(f *testing.F) {
	for _, nb := range bucketFixtures() {
		b, err := MarshalBucket(nb.info, nb.deleting)
		if err != nil {
			f.Fatalf("seed %s: %v", nb.name, err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		info, deleting, err := UnmarshalBucket(b)
		if err != nil {
			return
		}
		enc, err := MarshalBucket(info, deleting)
		if err != nil {
			t.Fatalf("re-encode of accepted record: %v", err)
		}
		again, againDeleting, err := UnmarshalBucket(enc)
		if err != nil {
			t.Fatalf("decode of re-encoded record: %v", err)
		}
		if diff := cmp.Diff(info, again, equivOpts()); diff != "" {
			t.Errorf("round trip changed info (-want +got):\n%s", diff)
		}
		if deleting != againDeleting {
			t.Errorf("round trip changed deleting: %v -> %v", deleting, againDeleting)
		}
	})
}
