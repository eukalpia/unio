package fakes

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// NewMultipartUpload starts an upload of bucketName/key and returns its ID.
// The attributes in opts are kept and applied when the upload completes.
func (l *Layer) NewMultipartUpload(ctx context.Context, bucketName, key string, opts object.PutOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := l.checkBucketKey(bucketName, key); err != nil {
		return "", err
	}
	if err := checkPutOptions(opts); err != nil {
		return "", err
	}
	if err := checkAlgorithm(opts.ChecksumAlgorithm); err != nil {
		return "", err
	}
	id, err := newUploadID()
	if err != nil {
		return "", err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return "", err
	}
	b.uploads[id] = &upload{
		key:       key,
		id:        id,
		opts:      clonePutOptions(opts),
		initiated: time.Now().UnixNano(),
		parts:     make(map[int]*storedPart),
	}
	return id, nil
}

// PutObjectPart stores body as part partNumber of an upload. A later upload
// of the same part number replaces it. The body is read before the lock is
// taken, and nothing is stored when the read fails.
func (l *Layer) PutObjectPart(ctx context.Context, bucketName, key, uploadID string, partNumber int, body object.Body) (object.PartInfo, error) {
	if err := ctx.Err(); err != nil {
		return object.PartInfo{}, err
	}
	if err := l.checkPartTarget(bucketName, key, uploadID, partNumber); err != nil {
		return object.PartInfo{}, err
	}
	if body == nil {
		return object.PartInfo{}, s3err.Errorf(s3err.InvalidRequest, "part body is required")
	}
	if body.Size() > object.MaxPartSize {
		return object.PartInfo{}, s3err.Errorf(s3err.EntityTooLarge, "part exceeds %d bytes", int64(object.MaxPartSize))
	}
	data, err := readPayload(body)
	if err != nil {
		return object.PartInfo{}, err
	}
	digest := body.MD5()
	part := &storedPart{
		info: object.PartInfo{
			PartNumber: partNumber,
			Size:       int64(len(data)),
			ETag:       hex.EncodeToString(digest[:]),
			ModTime:    time.Now().UnixNano(),
			Checksum:   cloneChecksum(body.Checksum()),
		},
		digest: digest,
		data:   data,
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	u, err := l.partUploadLocked(bucketName, key, uploadID, partNumber)
	if err != nil {
		return object.PartInfo{}, err
	}
	u.parts[partNumber] = part
	return clonePartInfo(part.info), nil
}

// CopyObjectPart stores a part whose content is a window of the source
// object (opts.SourceRange, or the whole object). The part ETag is the MD5 of
// the copied bytes.
func (l *Layer) CopyObjectPart(ctx context.Context, src object.Ref, bucketName, key, uploadID string, partNumber int, opts object.CopyOptions) (object.PartInfo, error) {
	if err := ctx.Err(); err != nil {
		return object.PartInfo{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	u, err := l.partUploadLocked(bucketName, key, uploadID, partNumber)
	if err != nil {
		return object.PartInfo{}, err
	}
	obj, err := l.sourceLocked(src, opts.SourceConditions)
	if err != nil {
		return object.PartInfo{}, err
	}
	off, n, err := windowOf(opts.SourceRange, obj.info.Size)
	if err != nil {
		return object.PartInfo{}, err
	}
	if n > object.MaxPartSize {
		return object.PartInfo{}, s3err.Errorf(s3err.EntityTooLarge, "part exceeds %d bytes", int64(object.MaxPartSize))
	}
	data := obj.data[off : off+n]
	digest := md5.Sum(data)
	part := &storedPart{
		info: object.PartInfo{
			PartNumber: partNumber,
			Size:       n,
			ETag:       hex.EncodeToString(digest[:]),
			ModTime:    time.Now().UnixNano(),
		},
		digest: digest,
		data:   data,
	}
	u.parts[partNumber] = part
	return clonePartInfo(part.info), nil
}

// ListParts returns one page of the parts of an upload, in part number
// order, after opts.PartNumberMarker.
func (l *Layer) ListParts(ctx context.Context, bucketName, key, uploadID string, opts object.ListPartsOptions) (object.ListPartsResult, error) {
	if err := ctx.Err(); err != nil {
		return object.ListPartsResult{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, u, err := l.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return object.ListPartsResult{}, err
	}
	max, err := pageSize(opts.MaxParts)
	if err != nil {
		return object.ListPartsResult{}, err
	}
	res := object.ListPartsResult{ChecksumAlgorithm: u.opts.ChecksumAlgorithm}
	for _, n := range sortedPartNumbers(u) {
		if n <= opts.PartNumberMarker {
			continue
		}
		if len(res.Parts) == max {
			res.IsTruncated = true
			break
		}
		res.Parts = append(res.Parts, clonePartInfo(u.parts[n].info))
	}
	if count := len(res.Parts); count > 0 {
		res.NextPartNumberMarker = res.Parts[count-1].PartNumber
	}
	return res, nil
}

// CompleteMultipartUpload assembles the listed parts into the current
// version of bucketName/key and removes the upload. The attributes come from
// the upload's NewMultipartUpload options; opts supplies only the destination
// conditions. Nothing changes when any check fails.
func (l *Layer) CompleteMultipartUpload(ctx context.Context, bucketName, key, uploadID string, parts []object.CompletePart, opts object.PutOptions) (object.Info, error) {
	if err := ctx.Err(); err != nil {
		return object.Info{}, err
	}
	if err := checkPartList(parts); err != nil {
		return object.Info{}, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	b, u, err := l.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return object.Info{}, err
	}
	selected, err := selectParts(u, parts)
	if err != nil {
		return object.Info{}, err
	}
	cur, exists := b.objects[key]
	etag := ""
	if exists {
		etag = cur.info.ETag
	}
	if err := opts.Conditions.EvaluateWrite(exists, etag); err != nil {
		return object.Info{}, err
	}

	var size int64
	for _, p := range selected {
		size += p.info.Size
	}
	if size > object.MaxObjectSize {
		return object.Info{}, s3err.Errorf(s3err.EntityTooLarge, "object exceeds %d bytes", int64(object.MaxObjectSize))
	}
	var data, digests []byte
	partData := make([][]byte, 0, len(selected))
	for _, p := range selected {
		data = append(data, p.data...)
		digests = append(digests, p.digest[:]...)
		partData = append(partData, p.data)
	}
	whole := md5.Sum(digests)
	etagOut := hex.EncodeToString(whole[:]) + "-" + strconv.Itoa(len(selected))
	info := infoFromPut(bucketName, key, size, etagOut, u.opts)
	info.Parts = len(selected)
	if u.opts.ChecksumAlgorithm != object.ChecksumNone {
		c, err := compositeChecksum(u.opts.ChecksumAlgorithm, partData)
		if err != nil {
			return object.Info{}, err
		}
		info.Checksum = c
	}

	b.objects[key] = &storedObject{info: info, data: data}
	delete(b.uploads, uploadID)
	return cloneInfo(info), nil
}

// AbortMultipartUpload discards an upload and its parts.
func (l *Layer) AbortMultipartUpload(ctx context.Context, bucketName, key, uploadID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, _, err := l.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return err
	}
	delete(b.uploads, uploadID)
	return nil
}

// checkPartTarget verifies that a part request targets an existing upload.
// It takes l.mu.
func (l *Layer) checkPartTarget(bucketName, key, uploadID string, partNumber int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.partUploadLocked(bucketName, key, uploadID, partNumber)
	return err
}

// partUploadLocked resolves the upload that a part request targets. The bucket
// and key must exist, the part number must be valid, and uploadID must name an
// upload of key. l.mu must be held.
func (l *Layer) partUploadLocked(bucketName, key, uploadID string, partNumber int) (*upload, error) {
	if _, err := l.bucketLocked(bucketName); err != nil {
		return nil, err
	}
	if err := object.ValidateKey(key); err != nil {
		return nil, err
	}
	if err := checkPartNumber(partNumber); err != nil {
		return nil, err
	}
	_, u, err := l.uploadLocked(bucketName, key, uploadID)
	return u, err
}

// checkPartNumber validates a multipart part number.
func checkPartNumber(n int) error {
	if n < 1 || n > object.MaxPartNumber {
		return s3err.Errorf(s3err.InvalidArgument, "part number must be between 1 and %d", object.MaxPartNumber)
	}
	return nil
}

// checkPartList validates the shape of a CompleteMultipartUpload request:
// at least one part, at most object.MaxPartNumber, and strictly ascending
// part numbers.
func checkPartList(parts []object.CompletePart) error {
	switch {
	case len(parts) == 0:
		return s3err.Errorf(s3err.InvalidRequest, "at least one part is required")
	case len(parts) > object.MaxPartNumber:
		return s3err.Errorf(s3err.InvalidRequest, "at most %d parts can be completed", object.MaxPartNumber)
	}
	for i := 1; i < len(parts); i++ {
		if parts[i].PartNumber <= parts[i-1].PartNumber {
			return s3err.Errorf(s3err.InvalidPartOrder, "part numbers must be in ascending order")
		}
	}
	return nil
}

// selectParts resolves the parts of a completion request against the stored
// parts of u. Every part must exist, carry the ETag the client sent, and,
// except for the last one, be at least object.MinPartSize bytes.
func selectParts(u *upload, parts []object.CompletePart) ([]*storedPart, error) {
	out := make([]*storedPart, 0, len(parts))
	for i, cp := range parts {
		p, ok := u.parts[cp.PartNumber]
		if !ok {
			return nil, s3err.Errorf(s3err.InvalidPart, "part %d was not uploaded", cp.PartNumber)
		}
		if unquoteETag(cp.ETag) != p.info.ETag {
			return nil, s3err.Errorf(s3err.InvalidPart, "ETag of part %d does not match", cp.PartNumber)
		}
		if i < len(parts)-1 && p.info.Size < object.MinPartSize {
			return nil, s3err.Errorf(s3err.EntityTooSmall, "part %d is smaller than %d bytes", cp.PartNumber, object.MinPartSize)
		}
		out = append(out, p)
	}
	return out, nil
}

// sortedPartNumbers returns the stored part numbers of u in ascending order.
func sortedPartNumbers(u *upload) []int {
	return slices.Sorted(maps.Keys(u.parts))
}

// newUploadID returns a random 128-bit upload ID in lower-case hex.
func newUploadID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", s3err.Wrap(s3err.InternalError, fmt.Errorf("upload id: %w", err))
	}
	return hex.EncodeToString(raw[:]), nil
}
