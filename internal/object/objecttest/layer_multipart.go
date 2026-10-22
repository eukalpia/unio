package objecttest

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine/digest"
	"github.com/eukalpia/unio/internal/s3err"
)

// NewMultipartUpload implements object.Layer.
func (l *Layer) NewMultipartUpload(_ context.Context, bucketName, key string, opts object.PutOptions) (string, error) {
	if err := checkBucketKey(bucketName, key); err != nil {
		return "", err
	}
	if err := checkPutOptions(opts); err != nil {
		return "", err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.bucketLocked(bucketName); err != nil {
		return "", err
	}
	stored := opts
	stored.Conditions = object.Conditions{}
	stored.UserMetadata = cloneMap(opts.UserMetadata)
	id, err := newUploadID()
	if err != nil {
		return "", err
	}
	l.uploads[id] = &upload{
		bucket:    bucketName,
		key:       key,
		id:        id,
		initiated: l.now(),
		opts:      stored,
		parts:     make(map[int]*uploadPart),
	}
	return id, nil
}

// PutObjectPart implements object.Layer.
func (l *Layer) PutObjectPart(ctx context.Context, bucketName, key, uploadID string, partNumber int, body object.Body) (object.PartInfo, error) {
	if err := ctx.Err(); err != nil {
		return object.PartInfo{}, err
	}
	if err := checkBucketKey(bucketName, key); err != nil {
		return object.PartInfo{}, err
	}
	if err := checkPartNumber(partNumber); err != nil {
		return object.PartInfo{}, err
	}
	if body.Size() > object.MaxPartSize {
		return object.PartInfo{}, s3err.Errorf(s3err.EntityTooLarge, "part exceeds the maximum part size")
	}
	data, md5sum, ck, err := readBody(body)
	if err != nil {
		return object.PartInfo{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.quorumFail {
		return object.PartInfo{}, quorumError()
	}
	u, err := l.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return object.PartInfo{}, err
	}
	return storePart(u, partNumber, data, md5sum, ck, l.now()), nil
}

// CopyObjectPart implements object.Layer.
func (l *Layer) CopyObjectPart(_ context.Context, src object.Ref, bucketName, key, uploadID string, partNumber int, opts object.CopyOptions) (object.PartInfo, error) {
	if err := checkBucketKey(bucketName, key); err != nil {
		return object.PartInfo{}, err
	}
	if err := checkPartNumber(partNumber); err != nil {
		return object.PartInfo{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	u, err := l.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return object.PartInfo{}, err
	}
	_, _, srcContent, err := l.srcLocked(src, opts.SourceConditions)
	if err != nil {
		return object.PartInfo{}, err
	}
	size := int64(len(srcContent.data))
	off, length := int64(0), size
	if opts.SourceRange != nil {
		if off, length, err = opts.SourceRange.Resolve(size); err != nil {
			return object.PartInfo{}, err
		}
	}
	if l.quorumFail {
		return object.PartInfo{}, quorumError()
	}
	data := append([]byte(nil), srcContent.data[off:off+length]...)
	ck, err := copyPartChecksum(data, u.opts.ChecksumAlgorithm)
	if err != nil {
		return object.PartInfo{}, err
	}
	return storePart(u, partNumber, data, digest.SumMD5(data), ck, l.now()), nil
}

// ListParts implements object.Layer.
func (l *Layer) ListParts(_ context.Context, bucketName, key, uploadID string, opts object.ListPartsOptions) (object.ListPartsResult, error) {
	if err := checkBucketKey(bucketName, key); err != nil {
		return object.ListPartsResult{}, err
	}
	if opts.PartNumberMarker < 0 {
		return object.ListPartsResult{}, s3err.Errorf(s3err.InvalidArgument, "negative part number marker")
	}
	max, err := checkListKeys(opts.MaxParts)
	if err != nil {
		return object.ListPartsResult{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	u, err := l.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return object.ListPartsResult{}, err
	}
	nums := make([]int, 0, len(u.parts))
	for n := range u.parts {
		if n > opts.PartNumberMarker {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	res := object.ListPartsResult{ChecksumAlgorithm: u.opts.ChecksumAlgorithm}
	if len(nums) > max {
		nums = nums[:max]
		res.IsTruncated = true
	}
	for _, n := range nums {
		p := u.parts[n]
		res.Parts = append(res.Parts, object.PartInfo{
			PartNumber: n,
			Size:       int64(len(p.data)),
			ETag:       p.etag,
			ModTime:    p.modTime.UnixNano(),
			Checksum:   cloneChecksum(p.checksum),
		})
	}
	if len(nums) > 0 {
		res.NextPartNumberMarker = nums[len(nums)-1]
	}
	return res, nil
}

// CompleteMultipartUpload implements object.Layer.
func (l *Layer) CompleteMultipartUpload(_ context.Context, bucketName, key, uploadID string, parts []object.CompletePart, opts object.PutOptions) (object.Info, error) {
	if err := checkBucketKey(bucketName, key); err != nil {
		return object.Info{}, err
	}
	if err := checkCompleteShape(parts); err != nil {
		return object.Info{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	u, err := l.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return object.Info{}, err
	}
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.Info{}, err
	}
	stored, total, err := matchParts(u, parts)
	if err != nil {
		return object.Info{}, err
	}
	exists, etag := l.currentLocked(b, key)
	if err := opts.Conditions.EvaluateWrite(exists, etag); err != nil {
		return object.Info{}, err
	}
	data := make([]byte, 0, total)
	spans := make([]partSpan, 0, len(parts))
	var md5s []byte
	for i, sp := range stored {
		spans = append(spans, partSpan{number: parts[i].PartNumber, off: int64(len(data)), size: int64(len(sp.data))})
		data = append(data, sp.data...)
		sum := digest.SumMD5(sp.data)
		md5s = append(md5s, sum[:]...)
	}
	info := newInfo(bucketName, key, l.now(), [16]byte{}, u.opts)
	info.ETag = fmt.Sprintf("%x-%d", digest.SumMD5(md5s), len(parts))
	info.Parts = len(parts)
	info.Size = int64(len(data))
	info.Checksum, err = multipartChecksum(u.opts.ChecksumAlgorithm, data, stored)
	if err != nil {
		return object.Info{}, err
	}
	b.commit(key, info, &content{data: data, parts: spans})
	delete(l.uploads, uploadID)
	return copyInfo(*info), nil
}

// AbortMultipartUpload implements object.Layer.
func (l *Layer) AbortMultipartUpload(_ context.Context, bucketName, key, uploadID string) error {
	if err := checkBucketKey(bucketName, key); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.uploadLocked(bucketName, key, uploadID); err != nil {
		return err
	}
	delete(l.uploads, uploadID)
	return nil
}

// ListMultipartUploads implements object.Layer.
func (l *Layer) ListMultipartUploads(_ context.Context, bucketName string, opts object.ListUploadsOptions) (object.ListUploadsResult, error) {
	if err := object.ValidateBucketName(bucketName); err != nil {
		return object.ListUploadsResult{}, err
	}
	max, err := checkListKeys(opts.MaxUploads)
	if err != nil {
		return object.ListUploadsResult{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.bucketLocked(bucketName); err != nil {
		return object.ListUploadsResult{}, err
	}
	var all []*upload
	for _, u := range l.uploads {
		if u.bucket == bucketName {
			all = append(all, u)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].key != all[j].key {
			return all[i].key < all[j].key
		}
		return all[i].id < all[j].id
	})
	var res object.ListUploadsResult
	lastPrefix := ""
	for _, u := range all {
		if !strings.HasPrefix(u.key, opts.Prefix) {
			continue
		}
		if opts.KeyMarker != "" && (u.key < opts.KeyMarker || (u.key == opts.KeyMarker && u.id <= opts.UploadIDMarker)) {
			continue
		}
		if opts.Delimiter != "" {
			rest := u.key[len(opts.Prefix):]
			if i := strings.Index(rest, opts.Delimiter); i >= 0 {
				cp := opts.Prefix + rest[:i+len(opts.Delimiter)]
				if cp == lastPrefix || strings.HasPrefix(opts.KeyMarker, cp) {
					continue
				}
				if len(res.Uploads)+len(res.CommonPrefixes) == max {
					res.IsTruncated = true
					return res, nil
				}
				lastPrefix = cp
				res.CommonPrefixes = append(res.CommonPrefixes, cp)
				res.NextKeyMarker, res.NextUploadIDMarker = cp, ""
				continue
			}
		}
		if len(res.Uploads)+len(res.CommonPrefixes) == max {
			res.IsTruncated = true
			return res, nil
		}
		res.Uploads = append(res.Uploads, object.Upload{Key: u.key, UploadID: u.id, Initiated: u.initiated.UnixNano()})
		res.NextKeyMarker, res.NextUploadIDMarker = u.key, u.id
	}
	return res, nil
}

// uploadLocked finds an upload of bucket/key.
func (l *Layer) uploadLocked(bucketName, key, uploadID string) (*upload, error) {
	u, ok := l.uploads[uploadID]
	if !ok || u.bucket != bucketName || u.key != key {
		return nil, s3err.Errorf(s3err.NoSuchUpload, "no such upload")
	}
	return u, nil
}

// storePart records a part under its number, replacing an earlier one.
func storePart(u *upload, number int, data []byte, md5sum [16]byte, ck object.Checksum, now time.Time) object.PartInfo {
	p := &uploadPart{data: data, etag: hex.EncodeToString(md5sum[:]), checksum: cloneChecksum(ck), modTime: now}
	u.parts[number] = p
	return object.PartInfo{
		PartNumber: number,
		Size:       int64(len(data)),
		ETag:       p.etag,
		ModTime:    now.UnixNano(),
		Checksum:   ck,
	}
}

// checkCompleteShape validates the part list of CompleteMultipartUpload
// without consulting the upload.
func checkCompleteShape(parts []object.CompletePart) error {
	if len(parts) == 0 {
		return s3err.Errorf(s3err.InvalidRequest, "at least one part is required")
	}
	prev := 0
	for _, p := range parts {
		if err := checkPartNumber(p.PartNumber); err != nil {
			return err
		}
		if p.PartNumber <= prev {
			return s3err.Errorf(s3err.InvalidPartOrder, "parts must be in ascending order")
		}
		prev = p.PartNumber
	}
	return nil
}

// matchParts checks the listed parts against the stored ones and returns
// the stored parts and the total size.
func matchParts(u *upload, parts []object.CompletePart) ([]*uploadPart, int64, error) {
	stored := make([]*uploadPart, len(parts))
	var total int64
	for i, p := range parts {
		sp, ok := u.parts[p.PartNumber]
		if !ok {
			return nil, 0, s3err.Errorf(s3err.InvalidPart, "part %d was not uploaded", p.PartNumber)
		}
		if strings.Trim(p.ETag, `"`) != sp.etag {
			return nil, 0, s3err.Errorf(s3err.InvalidPart, "etag of part %d does not match", p.PartNumber)
		}
		if u.opts.ChecksumAlgorithm != object.ChecksumNone && sp.checksum.Algorithm != u.opts.ChecksumAlgorithm {
			return nil, 0, s3err.Errorf(s3err.InvalidPart, "part %d has no matching checksum", p.PartNumber)
		}
		if i < len(parts)-1 && int64(len(sp.data)) < object.MinPartSize {
			return nil, 0, s3err.Errorf(s3err.EntityTooSmall, "part %d is smaller than the minimum part size", p.PartNumber)
		}
		stored[i] = sp
		total += int64(len(sp.data))
	}
	if total > object.MaxObjectSize {
		return nil, 0, s3err.Errorf(s3err.EntityTooLarge, "object exceeds the maximum size")
	}
	return stored, total, nil
}

// multipartChecksum returns the additional checksum of a completed multipart
// object: the full-object checksum for CRC-64/NVME, else the composite over the
// part checksums,
// or a zero checksum when alg is none.
func multipartChecksum(alg object.ChecksumAlgorithm, data []byte, parts []*uploadPart) (object.Checksum, error) {
	if alg == object.ChecksumNone {
		return object.Checksum{}, nil
	}
	if alg == object.ChecksumCRC64NVME {
		// S3 defines CRC-64/NVME over the whole object. The fake computes it
		// directly over the data, independently of the engine's combination.
		return fullChecksum(alg, data)
	}
	h, err := digest.New(alg)
	if err != nil {
		return object.Checksum{}, err
	}
	for _, p := range parts {
		h.Write(p.checksum.Value)
	}
	return object.Checksum{Algorithm: alg, Type: object.ChecksumComposite, Value: h.Sum(nil), Parts: len(parts)}, nil
}

// copyPartChecksum returns the checksum of a copied part for alg.
func copyPartChecksum(data []byte, alg object.ChecksumAlgorithm) (object.Checksum, error) {
	if alg == object.ChecksumNone {
		return object.Checksum{}, nil
	}
	return fullChecksum(alg, data)
}
