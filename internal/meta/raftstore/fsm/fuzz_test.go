package fsm

import (
	"testing"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/meta"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// numCommandKinds is the number of command selectors buildCommand knows. The
// data group takes selectors 0..8 and the system group 9..14.
const numCommandKinds = 15

// Fuzz modes: even values feed raw bytes to Apply, odd values build a command
// from the structured parameters.
const (
	fuzzRaw        uint8 = 0
	fuzzStructured uint8 = 1
)

// Flag bits of genParams. A set bit makes the generated command omit or
// corrupt one part of itself, so that rejection paths are exercised.
const (
	flagNilMessage   uint8 = 1 << 0 // omit the record, upload or part
	flagNilCondition uint8 = 1 << 1 // omit the write conditions
	flagNilLayout    uint8 = 1 << 2 // omit the erasure layout
	flagNilPart      uint8 = 1 << 3 // omit the part
	flagNilChecksum  uint8 = 1 << 4 // omit the checksum algorithm
	flagRawDataID    uint8 = 1 << 5 // use the payload as the data id, any length
	flagBadDrives    uint8 = 1 << 6 // give the layout a drive count it cannot have
	flagInline       uint8 = 1 << 7 // inline the payload
)

// genParams are the fuzzed fields a structured command is built from.
type genParams struct {
	bucket  uint64
	name    string
	key     string
	version string
	number  uint32
	payload []byte
	flags   uint8
}

// fuzzDataID returns the data id of a generated command. Unless flagRawDataID
// is set, the id is exactly 16 bytes, taken from the payload.
func (p genParams) fuzzDataID() []byte {
	if p.flags&flagRawDataID != 0 {
		return append([]byte(nil), p.payload...)
	}
	id := make([]byte, 16)
	copy(id, p.payload)
	return id
}

// fuzzLayout returns the layout of a generated command.
func (p genParams) fuzzLayout() *pb.ErasureLayout {
	if p.flags&flagNilLayout != 0 {
		return nil
	}
	shards := uint32(2)
	drives := make([][]byte, 0, 3)
	for i := 0; i < 3; i++ {
		drives = append(drives, p.fuzzDataID())
	}
	if p.flags&flagBadDrives != 0 {
		drives = drives[:len(p.payload)%3]
	}
	return &pb.ErasureLayout{Set: 1, DataShards: shards, ParityShards: 1, StripeSize: 1 << 20, Drives: drives}
}

// fuzzPart returns the part of a generated command.
func (p genParams) fuzzPart() *pb.PartRecord {
	if p.flags&flagNilPart != 0 {
		return nil
	}
	return &pb.PartRecord{Number: p.number, Size: int64(len(p.payload)), Etag: p.version, Data: p.fuzzDataID(), Layout: p.fuzzLayout()}
}

// fuzzRecord returns the object record of a generated command.
func (p genParams) fuzzRecord() *pb.ObjectRecord {
	if p.flags&flagNilMessage != 0 {
		return nil
	}
	rec := &pb.ObjectRecord{
		Bucket:          p.name,
		Key:             p.key,
		VersionId:       p.version,
		ModTimeUnixNano: int64(p.number),
		Size:            int64(len(p.payload)),
		Etag:            p.version,
		Multipart:       p.number%2 == 1,
	}
	rec.Attributes = &pb.ObjectAttributes{UserMetadata: map[string]string{"k1": "v", "k2": p.version, "k3": p.name}}
	if p.flags&flagInline != 0 {
		rec.Inline = append([]byte{}, p.payload...)
	} else if p.number%3 == 0 {
		rec.Parts = []*pb.PartRecord{p.fuzzPart()}
	}
	if p.flags&flagNilChecksum == 0 {
		rec.Checksum = &pb.Checksum{Algorithm: pb.ChecksumAlgorithm(p.number % 8), Value: p.payload}
	}
	return rec
}

// fuzzConditions returns the write conditions of a generated command.
func (p genParams) fuzzConditions() *pb.WriteConditions {
	if p.flags&flagNilCondition != 0 {
		return nil
	}
	return &pb.WriteConditions{IfMatch: p.version, IfNoneMatch: p.name}
}

// buildCommand builds a command from the selector and the parameters. The
// result is often invalid by construction: every selector accepts the fields
// as given, and validation decides.
func buildCommand(sel uint8, p genParams) *pb.Command {
	switch sel % numCommandKinds {
	case 0:
		return &pb.Command{Cmd: &pb.Command_PutObject{PutObject: &pb.PutObject{
			BucketId: p.bucket, Record: p.fuzzRecord(), Conditions: p.fuzzConditions(),
		}}}
	case 1:
		return &pb.Command{Cmd: &pb.Command_DeleteObject{DeleteObject: &pb.DeleteObject{
			BucketId: p.bucket, Bucket: p.name, Key: p.key, VersionId: p.version,
			Conditions: p.fuzzConditions(), ModTimeUnixNano: int64(p.number), MarkerVersionId: p.version,
		}}}
	case 2:
		return &pb.Command{Cmd: &pb.Command_UpdateLayout{UpdateLayout: &pb.UpdateLayout{
			BucketId: p.bucket, Key: p.key, VersionId: p.version, PartNumber: p.number,
			Data: p.fuzzDataID(), Layout: p.fuzzLayout(),
		}}}
	case 3:
		var up *pb.UploadRecord
		if p.flags&flagNilMessage == 0 {
			up = &pb.UploadRecord{Bucket: p.name, Key: p.key, UploadId: p.version, InitiatedUnixNano: int64(p.number)}
			if p.flags&flagNilChecksum == 0 {
				up.ChecksumAlgorithm = pb.ChecksumAlgorithm(p.number % 8)
			}
		}
		return &pb.Command{Cmd: &pb.Command_CreateUpload{CreateUpload: &pb.CreateUpload{BucketId: p.bucket, Upload: up}}}
	case 4:
		return &pb.Command{Cmd: &pb.Command_PutPart{PutPart: &pb.PutPart{
			BucketId: p.bucket, Key: p.key, UploadId: p.version, Part: p.fuzzPart(),
		}}}
	case 5:
		parts := []*pb.PartRecord{p.fuzzPart(), {Number: p.number + 1, Data: p.fuzzDataID()}}
		rec := p.fuzzRecord()
		if rec != nil {
			rec.Parts = parts
		}
		return &pb.Command{Cmd: &pb.Command_CompleteUpload{CompleteUpload: &pb.CompleteUpload{
			BucketId: p.bucket, Key: p.key, UploadId: p.version, Record: rec, Conditions: p.fuzzConditions(),
		}}}
	case 6:
		return &pb.Command{Cmd: &pb.Command_AbortUpload{AbortUpload: &pb.AbortUpload{
			BucketId: p.bucket, Key: p.key, UploadId: p.version,
		}}}
	case 7:
		return &pb.Command{Cmd: &pb.Command_FenceBucket{FenceBucket: &pb.FenceBucket{
			BucketId: p.bucket, Fenced: p.number%2 == 1,
		}}}
	case 8:
		return &pb.Command{Cmd: &pb.Command_SetPartitionVersioning{SetPartitionVersioning: &pb.SetPartitionVersioning{
			BucketId: p.bucket, Versioning: pb.VersioningState(p.number % 4),
		}}}
	case 9:
		return &pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{
			Name: p.name, Owner: p.key, Versioning: pb.VersioningState(p.number % 4), CreatedUnixNano: int64(p.number),
		}}}
	case 10:
		return &pb.Command{Cmd: &pb.Command_MarkBucketDeleting{MarkBucketDeleting: &pb.MarkBucketDeleting{
			Name: p.name, BucketId: p.bucket, Deleting: p.number%2 == 1,
		}}}
	case 11:
		return &pb.Command{Cmd: &pb.Command_DeleteBucket{DeleteBucket: &pb.DeleteBucket{
			Name: p.name, BucketId: p.bucket,
		}}}
	case 12:
		return &pb.Command{Cmd: &pb.Command_SetBucketVersioning{SetBucketVersioning: &pb.SetBucketVersioning{
			Name: p.name, BucketId: p.bucket, Versioning: pb.VersioningState(p.number % 4),
		}}}
	case 13:
		return &pb.Command{Cmd: &pb.Command_KvPut{KvPut: &pb.KVPut{
			Key: p.key, Value: p.payload, ExpectRevision: uint64(p.number),
		}}}
	default:
		return &pb.Command{Cmd: &pb.Command_KvDelete{KvDelete: &pb.KVDelete{
			Key: p.key, ExpectRevision: uint64(p.number),
		}}}
	}
}

// fuzzKind returns the state machine kind a fuzzed selector targets. Most
// selectors match the group that accepts them; some are sent to the other
// group on purpose.
func fuzzKind(kindSel uint8) Kind {
	if kindSel%2 == 1 {
		return KindSystem
	}
	return KindData
}

// fuzzPrelude gives the harness the state that fuzzed commands can hit: an
// object, an upload with a part, and a bucket.
func fuzzPrelude(t *testing.T, h *harness) {
	t.Helper()
	if h.kind == KindSystem {
		h.mustApply(&pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{Name: "seedbkt", Owner: "o"}}})
		return
	}
	h.mustApply(putCmd(1, objRec("seed", "", "e"), nil))
	h.mustApply(&pb.Command{Cmd: &pb.Command_CreateUpload{CreateUpload: &pb.CreateUpload{
		BucketId: 1, Upload: &pb.UploadRecord{Bucket: "bkt", Key: "seed", UploadId: "seed-up", InitiatedUnixNano: 1},
	}}})
	h.mustApply(&pb.Command{Cmd: &pb.Command_PutPart{PutPart: &pb.PutPart{
		BucketId: 1, Key: "seed", UploadId: "seed-up", Part: goodPart(1),
	}}})
}

// fuzzSeeds returns one valid command of every type, with the state that
// fuzzPrelude creates, and the kind selector that routes it.
func fuzzSeeds() []struct {
	kindSel uint8
	cmd     *pb.Command
} {
	return []struct {
		kindSel uint8
		cmd     *pb.Command
	}{
		{0, putCmd(1, objRec("seed-2", "", "e2"), nil)},
		{0, deleteCmd(1, "seed", "", "", nil)},
		{0, &pb.Command{Cmd: &pb.Command_UpdateLayout{UpdateLayout: &pb.UpdateLayout{
			BucketId: 1, Key: "seed", PartNumber: 0, Data: dataID(9), Layout: goodLayout(),
		}}}},
		{0, &pb.Command{Cmd: &pb.Command_CreateUpload{CreateUpload: &pb.CreateUpload{
			BucketId: 1, Upload: &pb.UploadRecord{Bucket: "bkt", Key: "seed", UploadId: "seed-up-2", InitiatedUnixNano: 2},
		}}}},
		{0, &pb.Command{Cmd: &pb.Command_PutPart{PutPart: &pb.PutPart{
			BucketId: 1, Key: "seed", UploadId: "seed-up", Part: goodPart(2),
		}}}},
		{0, &pb.Command{Cmd: &pb.Command_CompleteUpload{CompleteUpload: &pb.CompleteUpload{
			BucketId: 1, Key: "seed", UploadId: "seed-up",
			Record: withRecord(func(r *pb.ObjectRecord) { r.Key = "seed"; r.Parts = []*pb.PartRecord{goodPart(1)} }),
		}}}},
		{0, &pb.Command{Cmd: &pb.Command_AbortUpload{AbortUpload: &pb.AbortUpload{BucketId: 1, Key: "seed", UploadId: "seed-up"}}}},
		{0, fenceCmd(1, false)},
		{0, versioningCmd(1, pb.VersioningState_VERSIONING_STATE_UNVERSIONED)},
		{1, &pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{Name: "bucket-two", Owner: "o"}}}},
		{1, &pb.Command{Cmd: &pb.Command_MarkBucketDeleting{MarkBucketDeleting: &pb.MarkBucketDeleting{Name: "seedbkt", BucketId: 1, Deleting: true}}}},
		{1, &pb.Command{Cmd: &pb.Command_DeleteBucket{DeleteBucket: &pb.DeleteBucket{Name: "seedbkt", BucketId: 1}}}},
		{1, &pb.Command{Cmd: &pb.Command_SetBucketVersioning{SetBucketVersioning: &pb.SetBucketVersioning{Name: "seedbkt", BucketId: 1, Versioning: pb.VersioningState_VERSIONING_STATE_ENABLED}}}},
		{1, &pb.Command{Cmd: &pb.Command_KvPut{KvPut: &pb.KVPut{Key: "k", Value: []byte("v"), ExpectRevision: meta.AnyRevision}}}},
		{1, &pb.Command{Cmd: &pb.Command_KvDelete{KvDelete: &pb.KVDelete{Key: "k", ExpectRevision: meta.AnyRevision}}}},
	}
}

// applyRaw feeds raw bytes to Apply as the next log entry.
func (h *harness) applyRaw(data []byte) *Outcome {
	h.t.Helper()
	h.index++
	res := h.f.Apply(&raft.Log{Index: h.index, Type: raft.LogCommand, Data: data})
	out, ok := res.(*Outcome)
	if !ok {
		h.t.Fatalf("Apply returned %T, want *Outcome", res)
	}
	return out
}

// FuzzApply feeds arbitrary bytes and structured, often invalid, commands to a
// state machine that holds state. The properties checked:
//   - Apply never panics, and no command reaches a handler panic (PanicCount
//     stays zero, so a handler bug cannot hide behind the recovery).
//   - A rejected command leaves the visible state unchanged.
//   - Apply always returns an *Outcome.
func FuzzApply(f *testing.F) {
	for _, seed := range fuzzSeeds() {
		data, err := seed.cmd.MarshalVT()
		if err != nil {
			f.Fatalf("encode seed: %v", err)
		}
		f.Add(fuzzRaw, seed.kindSel, data, uint8(0), uint64(0), "", "", "", uint32(0), []byte(nil), uint8(0))
	}
	for sel := uint8(0); sel < numCommandKinds; sel++ {
		kindSel := uint8(0)
		if sel >= 9 {
			kindSel = 1
		}
		f.Add(fuzzStructured, kindSel, []byte(nil), sel, uint64(1), "bkt", "seed", "seed-up", uint32(1), []byte("payload"), uint8(0))
		f.Add(fuzzStructured, kindSel, []byte(nil), sel, uint64(1), "seedbkt", "seed", "seed-up", uint32(2), []byte("0123456789abcdef"), flagInline)
	}
	f.Add(fuzzStructured, uint8(0), []byte(nil), uint8(5), uint64(0), "", "", "", uint32(10001), []byte{}, flagNilPart|flagBadDrives)
	f.Add(fuzzRaw, uint8(1), []byte{0xff, 0xff, 0xff}, uint8(0), uint64(0), "", "", "", uint32(0), []byte(nil), uint8(0))

	f.Fuzz(func(t *testing.T, mode, kindSel uint8, raw []byte, sel uint8, bucket uint64, name, key, version string, number uint32, payload []byte, flags uint8) {
		h := newHarness(t, fuzzKind(kindSel))
		fuzzPrelude(t, h)
		var data []byte
		if mode%2 == fuzzRaw {
			data = raw
		} else {
			p := genParams{bucket: bucket, name: name, key: key, version: version, number: number, payload: payload, flags: flags}
			encoded, err := buildCommand(sel, p).MarshalVT()
			if err != nil {
				t.Fatalf("encode generated command: %v", err)
			}
			data = encoded
		}
		before := h.dumpState()
		out := h.applyRaw(data)
		if n := h.f.PanicCount(); n != 0 {
			t.Fatalf("a command panicked in a handler (%d times): input %x", n, data)
		}
		if out == nil {
			t.Fatal("Apply returned no outcome")
		}
		if out.Err != nil {
			h.assertUnchanged(before)
		}
	})
}
