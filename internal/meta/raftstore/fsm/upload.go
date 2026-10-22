package fsm

import (
	"bytes"
	"fmt"

	"github.com/cockroachdb/pebble/v2"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// errNoUpload is returned for commands naming an upload that does not exist.
var errNoUpload = s3err.Errorf(s3err.NoSuchUpload, "upload does not exist")

// uploadIDKey returns the meta record that claims an upload id in a bucket.
// Parts are keyed by bucket and upload id alone, so an id must name one upload
// in the whole bucket, whatever its key. The claim exists from CreateUpload
// until CompleteUpload or AbortUpload removes it.
func uploadIDKey(bucket ids.BucketID, uploadID string) []byte {
	return keys.Meta(fmt.Sprintf("uploadID\x00%016x%s", uint64(bucket), uploadID))
}

// uploadRecord reads the upload record, or returns nil when absent.
func (f *FSM) uploadRecord(bucket ids.BucketID, key []byte, uploadID string) (*pb.UploadRecord, error) {
	v, found, err := f.get(keys.Upload(bucket, key, []byte(uploadID)))
	if err != nil || !found {
		return nil, err
	}
	up := &pb.UploadRecord{}
	if err := up.UnmarshalVT(v); err != nil {
		return nil, fmt.Errorf("fsm: decode upload: %w", err)
	}
	return up, nil
}

// uploadParts returns every stored part of an upload, in part-number order.
func (f *FSM) uploadParts(bucket ids.BucketID, uploadID string) ([]*pb.PartRecord, error) {
	it, err := f.prefixIter(keys.PartUploadPrefix(bucket, []byte(uploadID)))
	if err != nil {
		return nil, err
	}
	var parts []*pb.PartRecord
	for ok := it.First(); ok; ok = it.Next() {
		p := &pb.PartRecord{}
		if err := p.UnmarshalVT(it.Value()); err != nil {
			return nil, closeIter(it, fmt.Errorf("fsm: decode part: %w", err))
		}
		parts = append(parts, p)
	}
	return parts, closeIter(it, it.Error())
}

// createUpload registers an upload. Upload ids are random, so an id that is
// already claimed in the bucket is a bug and is rejected, whatever the key.
func (f *FSM) createUpload(b *pebble.Batch, c *pb.CreateUpload) (*pb.ApplyResult, error) {
	bucket := ids.BucketID(c.BucketId)
	up := c.GetUpload()
	if up == nil || up.Key == "" || up.UploadId == "" {
		return nil, errMalformed("create upload")
	}
	ctrl, err := f.control(bucket)
	if err != nil {
		return nil, err
	}
	if ctrl.fenced {
		return nil, errBucketGone
	}
	_, claimed, err := f.get(uploadIDKey(bucket, up.UploadId))
	if err != nil {
		return nil, err
	}
	if claimed {
		return nil, errInvalid("upload id already in use")
	}
	v, err := up.MarshalVT()
	if err != nil {
		return nil, fmt.Errorf("fsm: encode upload: %w", err)
	}
	if err := b.Set(keys.Upload(bucket, []byte(up.Key), []byte(up.UploadId)), v, nil); err != nil {
		return nil, fmt.Errorf("fsm: write upload: %w", err)
	}
	if err := b.Set(uploadIDKey(bucket, up.UploadId), nil, nil); err != nil {
		return nil, fmt.Errorf("fsm: claim upload id: %w", err)
	}
	return &pb.ApplyResult{}, nil
}

// putPart stores a part, replacing the part with the same number. The
// replaced part is returned as garbage.
func (f *FSM) putPart(b *pebble.Batch, c *pb.PutPart) (*pb.ApplyResult, error) {
	bucket := ids.BucketID(c.BucketId)
	part := c.GetPart()
	if part == nil || c.UploadId == "" {
		return nil, errMalformed("put part")
	}
	ctrl, err := f.control(bucket)
	if err != nil {
		return nil, err
	}
	if ctrl.fenced {
		return nil, errBucketGone
	}
	up, err := f.uploadRecord(bucket, []byte(c.Key), c.UploadId)
	if err != nil {
		return nil, err
	}
	if up == nil {
		return nil, errNoUpload
	}
	pKey := keys.Part(bucket, []byte(c.UploadId), part.Number)
	res := &pb.ApplyResult{}
	old, found, err := f.get(pKey)
	if err != nil {
		return nil, err
	}
	if found {
		replaced := &pb.PartRecord{}
		if err := replaced.UnmarshalVT(old); err != nil {
			return nil, fmt.Errorf("fsm: decode part: %w", err)
		}
		res.ReplacedPart = replaced
	}
	v, err := part.MarshalVT()
	if err != nil {
		return nil, fmt.Errorf("fsm: encode part: %w", err)
	}
	if err := b.Set(pKey, v, nil); err != nil {
		return nil, fmt.Errorf("fsm: write part: %w", err)
	}
	return res, nil
}

// completeUpload commits the parts listed in the record as the newest
// version and removes the upload. Each listed part must still be stored with
// the same DataID, otherwise the command fails with InvalidPart. The stored
// copies of the parts are used in the new record, and parts that are not
// used are returned as garbage.
func (f *FSM) completeUpload(b *pebble.Batch, c *pb.CompleteUpload) (*pb.ApplyResult, error) {
	bucket := ids.BucketID(c.BucketId)
	rec := c.GetRecord()
	if rec == nil || rec.Key != c.Key || c.UploadId == "" {
		return nil, errMalformed("complete upload")
	}
	ctrl, err := f.control(bucket)
	if err != nil {
		return nil, err
	}
	if ctrl.fenced {
		return nil, errBucketGone
	}
	up, err := f.uploadRecord(bucket, []byte(c.Key), c.UploadId)
	if err != nil {
		return nil, err
	}
	if up == nil {
		return nil, errNoUpload
	}
	stored, err := f.uploadParts(bucket, c.UploadId)
	if err != nil {
		return nil, err
	}
	byNumber := make(map[uint32]*pb.PartRecord, len(stored))
	for _, p := range stored {
		byNumber[p.Number] = p
	}
	used := make(map[uint32]bool, len(rec.Parts))
	final := make([]*pb.PartRecord, 0, len(rec.Parts))
	var prev uint32
	for i, p := range rec.Parts {
		if i > 0 && p.Number <= prev {
			return nil, s3err.Errorf(s3err.InvalidPartOrder, "part numbers must be ascending")
		}
		prev = p.Number
		sp := byNumber[p.Number]
		if sp == nil || !bytes.Equal(sp.Data, p.Data) || sp.Etag != p.Etag {
			return nil, s3err.Errorf(s3err.InvalidPart, "part %d does not match the upload", p.Number)
		}
		final = append(final, sp)
		used[p.Number] = true
	}

	key := []byte(c.Key)
	latest, _, err := f.latestVersion(bucket, key)
	if err != nil {
		return nil, err
	}
	if err := checkConditions(c.GetConditions(), latest); err != nil {
		return nil, err
	}
	rec.Parts = final

	garbage, err := f.addVersion(b, ctrl, bucket, key, rec)
	if err != nil {
		return nil, err
	}
	res := &pb.ApplyResult{VersionId: rec.VersionId, Garbage: garbage}
	for _, p := range stored {
		if !used[p.Number] {
			res.UnusedParts = append(res.UnusedParts, p)
		}
	}
	if err := b.Delete(keys.Upload(bucket, key, []byte(c.UploadId)), nil); err != nil {
		return nil, fmt.Errorf("fsm: delete upload: %w", err)
	}
	if err := b.Delete(uploadIDKey(bucket, c.UploadId), nil); err != nil {
		return nil, fmt.Errorf("fsm: release upload id: %w", err)
	}
	if _, err := f.deletePrefix(b, keys.PartUploadPrefix(bucket, []byte(c.UploadId))); err != nil {
		return nil, err
	}
	return res, nil
}

// abortUpload removes an upload and returns all of its parts as garbage.
func (f *FSM) abortUpload(b *pebble.Batch, c *pb.AbortUpload) (*pb.ApplyResult, error) {
	bucket := ids.BucketID(c.BucketId)
	up, err := f.uploadRecord(bucket, []byte(c.Key), c.UploadId)
	if err != nil {
		return nil, err
	}
	if up == nil {
		return nil, errNoUpload
	}
	stored, err := f.uploadParts(bucket, c.UploadId)
	if err != nil {
		return nil, err
	}
	if err := b.Delete(keys.Upload(bucket, []byte(c.Key), []byte(c.UploadId)), nil); err != nil {
		return nil, fmt.Errorf("fsm: delete upload: %w", err)
	}
	if err := b.Delete(uploadIDKey(bucket, c.UploadId), nil); err != nil {
		return nil, fmt.Errorf("fsm: release upload id: %w", err)
	}
	if _, err := f.deletePrefix(b, keys.PartUploadPrefix(bucket, []byte(c.UploadId))); err != nil {
		return nil, err
	}
	return &pb.ApplyResult{UnusedParts: stored}, nil
}
