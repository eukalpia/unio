// Package objecttest provides an in-memory object.Layer with plain S3
// semantics, a test Body with failure injection, and the conformance suite
// that every object.Layer implementation must pass (RunLayerSuite).
//
// Implementations may additionally satisfy the optional interfaces Flusher,
// DataCounter and QuorumFaults. The suite uses them, when present, for
// checks that need to wait for asynchronous cleanup, count stored data, or
// simulate a lost write quorum.
package objecttest

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine/digest"
	"github.com/eukalpia/unio/internal/s3err"
)

// Flusher is implemented by layers that delete data asynchronously. Flush
// waits until the queued deletions have finished.
type Flusher interface {
	Flush(ctx context.Context) error
}

// DataCounter is implemented by layers that can report how many data units
// they store outside metadata. The count covers the parts of multipart
// objects and uploads and every non-inline object, with the default inline
// threshold.
type DataCounter interface {
	StoredDataCount() int
}

// QuorumFaults is implemented by layers whose storage can be made to miss
// its write quorum. While the fault is on, writes fail with
// ServiceUnavailable and store nothing.
type QuorumFaults interface {
	SetWriteQuorumFailure(on bool)
}

// Layer is an in-memory object.Layer. It is safe for concurrent use.
//
// It does not implement object versions: VersionID must be empty.
type Layer struct {
	mu         sync.Mutex
	now        func() time.Time
	nextBucket uint64
	buckets    map[string]*bucket
	uploads    map[string]*upload
	quorumFail bool
}

type bucket struct {
	info    object.BucketInfo
	objects map[string]*object.Info
	content map[string]*content
}

// content is the stored data of one object.
type content struct {
	data  []byte
	parts []partSpan // non-nil for multipart objects
}

type partSpan struct {
	number int
	off    int64
	size   int64
}

type upload struct {
	bucket    string
	key       string
	id        string
	initiated time.Time
	opts      object.PutOptions
	parts     map[int]*uploadPart
}

type uploadPart struct {
	data     []byte
	etag     string
	checksum object.Checksum
	modTime  time.Time
}

var (
	_ object.Layer = (*Layer)(nil)
	_ Flusher      = (*Layer)(nil)
	_ DataCounter  = (*Layer)(nil)
	_ QuorumFaults = (*Layer)(nil)
)

// NewLayer returns an empty in-memory layer.
func NewLayer() *Layer {
	return &Layer{
		now:     time.Now,
		buckets: make(map[string]*bucket),
		uploads: make(map[string]*upload),
	}
}

// Flush implements Flusher. The fake deletes synchronously.
func (l *Layer) Flush(context.Context) error { return nil }

// SetWriteQuorumFailure implements QuorumFaults.
func (l *Layer) SetWriteQuorumFailure(on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.quorumFail = on
}

// StoredDataCount implements DataCounter.
func (l *Layer) StoredDataCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, b := range l.buckets {
		for _, c := range b.content {
			n += dataUnits(c)
		}
	}
	for _, u := range l.uploads {
		n += len(u.parts)
	}
	return n
}

// dataUnits counts the erasure-coded units a version occupies: one per part
// of a multipart object, one for a larger single-part object, none for an
// inline object.
func dataUnits(c *content) int {
	if c.parts != nil {
		return len(c.parts)
	}
	if int64(len(c.data)) > object.DefaultInlineThreshold {
		return 1
	}
	return 0
}

// MakeBucket implements object.Layer.
func (l *Layer) MakeBucket(_ context.Context, name string, opts object.MakeBucketOptions) (object.BucketInfo, error) {
	if err := object.ValidateBucketName(name); err != nil {
		return object.BucketInfo{}, err
	}
	if opts.Versioning > object.VersioningSuspended {
		return object.BucketInfo{}, s3err.Errorf(s3err.InvalidArgument, "unknown versioning state")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[name]; ok {
		if b.info.Owner == opts.Owner {
			return object.BucketInfo{}, s3err.Errorf(s3err.BucketAlreadyOwnedByYou, "bucket already owned by you")
		}
		return object.BucketInfo{}, s3err.Errorf(s3err.BucketAlreadyExists, "bucket already exists")
	}
	l.nextBucket++
	b := &bucket{
		info: object.BucketInfo{
			Name:       name,
			ID:         ids.BucketID(l.nextBucket),
			Created:    l.now(),
			Owner:      opts.Owner,
			Versioning: opts.Versioning,
		},
		objects: make(map[string]*object.Info),
		content: make(map[string]*content),
	}
	l.buckets[name] = b
	return b.info, nil
}

// GetBucketInfo implements object.Layer.
func (l *Layer) GetBucketInfo(_ context.Context, name string) (object.BucketInfo, error) {
	if err := object.ValidateBucketName(name); err != nil {
		return object.BucketInfo{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(name)
	if err != nil {
		return object.BucketInfo{}, err
	}
	return b.info, nil
}

// ListBuckets implements object.Layer.
func (l *Layer) ListBuckets(context.Context) ([]object.BucketInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]object.BucketInfo, 0, len(l.buckets))
	for _, b := range l.buckets {
		out = append(out, b.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteBucket implements object.Layer.
func (l *Layer) DeleteBucket(_ context.Context, name string, opts object.DeleteBucketOptions) error {
	if err := object.ValidateBucketName(name); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.bucketLocked(name); err != nil {
		return err
	}
	inUse := len(l.buckets[name].objects) > 0 || l.hasUploadsLocked(name)
	if inUse && !opts.Force {
		return s3err.Errorf(s3err.BucketNotEmpty, "bucket is not empty")
	}
	for id, u := range l.uploads {
		if u.bucket == name {
			delete(l.uploads, id)
		}
	}
	delete(l.buckets, name)
	return nil
}

// PutObject implements object.Layer.
func (l *Layer) PutObject(ctx context.Context, bucketName, key string, body object.Body, opts object.PutOptions) (object.Info, error) {
	if err := ctx.Err(); err != nil {
		return object.Info{}, err
	}
	if err := checkBucketKey(bucketName, key); err != nil {
		return object.Info{}, err
	}
	if err := checkPutOptions(opts); err != nil {
		return object.Info{}, err
	}
	if body.Size() > object.MaxPutSize {
		return object.Info{}, s3err.Errorf(s3err.EntityTooLarge, "object exceeds the maximum PUT size")
	}
	data, md5sum, ck, err := readBody(body)
	if err != nil {
		return object.Info{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.quorumFail && int64(len(data)) > object.DefaultInlineThreshold {
		return object.Info{}, quorumError()
	}
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.Info{}, err
	}
	exists, etag := l.currentLocked(b, key)
	if err := opts.Conditions.EvaluateWrite(exists, etag); err != nil {
		return object.Info{}, err
	}
	info := newInfo(bucketName, key, l.now(), md5sum, opts)
	info.Checksum = ck
	info.Size = int64(len(data))
	b.commit(key, info, &content{data: data})
	return *info, nil
}

// GetObject implements object.Layer.
func (l *Layer) GetObject(_ context.Context, bucketName, key string, opts object.GetOptions) (*object.Reader, error) {
	if err := checkBucketKey(bucketName, key); err != nil {
		return nil, err
	}
	if err := checkPartNumberOpt(opts.PartNumber); err != nil {
		return nil, err
	}
	if opts.VersionID != "" {
		return nil, s3err.Errorf(s3err.NoSuchVersion, "versions are not supported")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	info, c, err := l.readableLocked(bucketName, key, opts)
	if err != nil {
		return nil, err
	}
	window, err := selectWindow(c, opts.PartNumber, opts.Range)
	if err != nil {
		return nil, err
	}
	info.Size = window.size
	data := c.data[window.off : window.off+window.length]
	return &object.Reader{
		ReadCloser: io.NopCloser(bytes.NewReader(data)),
		Info:       *info,
		Offset:     window.off - window.base,
		Length:     window.length,
	}, nil
}

// HeadObject implements object.Layer.
func (l *Layer) HeadObject(_ context.Context, bucketName, key string, opts object.GetOptions) (object.Info, error) {
	if err := checkBucketKey(bucketName, key); err != nil {
		return object.Info{}, err
	}
	if err := checkPartNumberOpt(opts.PartNumber); err != nil {
		return object.Info{}, err
	}
	if opts.VersionID != "" {
		return object.Info{}, s3err.Errorf(s3err.NoSuchVersion, "versions are not supported")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	info, c, err := l.readableLocked(bucketName, key, opts)
	if err != nil {
		return object.Info{}, err
	}
	window, err := selectWindow(c, opts.PartNumber, nil)
	if err != nil {
		return object.Info{}, err
	}
	info.Size = window.size
	return *info, nil
}

// DeleteObject implements object.Layer.
func (l *Layer) DeleteObject(_ context.Context, bucketName, key string, opts object.DeleteOptions) (object.DeleteResult, error) {
	if err := checkBucketKey(bucketName, key); err != nil {
		return object.DeleteResult{}, err
	}
	if opts.VersionID != "" {
		return object.DeleteResult{}, s3err.Errorf(s3err.NoSuchVersion, "versions are not supported")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.DeleteResult{}, err
	}
	exists, etag := l.currentLocked(b, key)
	if err := opts.Conditions.EvaluateWrite(exists, etag); err != nil {
		return object.DeleteResult{}, err
	}
	delete(b.objects, key)
	delete(b.content, key)
	return object.DeleteResult{Key: key}, nil
}

// DeleteObjects implements object.Layer.
func (l *Layer) DeleteObjects(ctx context.Context, bucketName string, objects []object.ToDelete) ([]object.DeleteResult, error) {
	if err := object.ValidateBucketName(bucketName); err != nil {
		return nil, err
	}
	if len(objects) > object.MaxDeleteObjects {
		return nil, s3err.Errorf(s3err.MalformedXML, "more than %d objects", object.MaxDeleteObjects)
	}
	l.mu.Lock()
	_, err := l.bucketLocked(bucketName)
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	results := make([]object.DeleteResult, 0, len(objects))
	for _, o := range objects {
		res, err := l.DeleteObject(ctx, bucketName, o.Key, object.DeleteOptions{})
		if err != nil {
			res = object.DeleteResult{Key: o.Key, Err: err}
		}
		results = append(results, res)
	}
	return results, nil
}

// ListObjects implements object.Layer.
func (l *Layer) ListObjects(_ context.Context, bucketName string, opts object.ListOptions) (object.ListResult, error) {
	if err := object.ValidateBucketName(bucketName); err != nil {
		return object.ListResult{}, err
	}
	max, err := checkListKeys(opts.MaxKeys)
	if err != nil {
		return object.ListResult{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.ListResult{}, err
	}
	keys := make([]string, 0, len(b.objects))
	for k := range b.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	page, truncated := pageKeys(keys, opts.Prefix, opts.Delimiter, opts.After, max)
	res := object.ListResult{IsTruncated: truncated}
	for _, e := range page {
		if e.isPrefix {
			res.CommonPrefixes = append(res.CommonPrefixes, e.key)
		} else {
			info := *b.objects[e.key]
			info.IsLatest = true
			res.Objects = append(res.Objects, info)
		}
		res.NextAfter = e.key
	}
	return res, nil
}

// srcLocked resolves a copy source and checks the source preconditions.
func (l *Layer) srcLocked(src object.Ref, conds object.Conditions) (*bucket, *object.Info, *content, error) {
	if err := checkBucketKey(src.Bucket, src.Key); err != nil {
		return nil, nil, nil, err
	}
	if src.VersionID != "" {
		return nil, nil, nil, s3err.Errorf(s3err.NoSuchVersion, "versions are not supported")
	}
	b, err := l.bucketLocked(src.Bucket)
	if err != nil {
		return nil, nil, nil, err
	}
	info, ok := b.objects[src.Key]
	if !ok {
		return nil, nil, nil, s3err.Errorf(s3err.NoSuchKey, "no such key")
	}
	if err := conds.EvaluateRead(*info); err != nil {
		if s3err.Is(err, s3err.NotModified) {
			// Copy-source preconditions report 412 even for If-None-Match.
			return nil, nil, nil, s3err.Errorf(s3err.PreconditionFailed, "copy source precondition failed")
		}
		return nil, nil, nil, err
	}
	return b, info, b.content[src.Key], nil
}

// CopyObject implements object.Layer.
func (l *Layer) CopyObject(ctx context.Context, src object.Ref, dstBucket, dstKey string, opts object.CopyOptions) (object.Info, error) {
	if err := ctx.Err(); err != nil {
		return object.Info{}, err
	}
	if err := checkBucketKey(dstBucket, dstKey); err != nil {
		return object.Info{}, err
	}
	if opts.SourceRange != nil {
		return object.Info{}, s3err.Errorf(s3err.InvalidArgument, "a range is only valid for part copies")
	}
	if err := checkPutOptions(opts.Put); err != nil {
		return object.Info{}, err
	}
	if src.Bucket == dstBucket && src.Key == dstKey && opts.Directive == object.MetadataCopy {
		return object.Info{}, s3err.Errorf(s3err.InvalidRequest, "this copy would replace the object with itself without changing its metadata")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, srcInfo, srcContent, err := l.srcLocked(src, opts.SourceConditions)
	if err != nil {
		return object.Info{}, err
	}
	if int64(len(srcContent.data)) > object.MaxPutSize {
		return object.Info{}, s3err.Errorf(s3err.InvalidRequest, "source is larger than the maximum copy size")
	}
	b, err := l.bucketLocked(dstBucket)
	if err != nil {
		return object.Info{}, err
	}
	exists, etag := l.currentLocked(b, dstKey)
	if err := opts.Put.Conditions.EvaluateWrite(exists, etag); err != nil {
		return object.Info{}, err
	}
	data := bytes.Clone(srcContent.data)
	info := newInfo(dstBucket, dstKey, l.now(), md5.Sum(data), opts.Put)
	if opts.Directive == object.MetadataCopy {
		copyAttributes(info, srcInfo)
	}
	info.Size = int64(len(data))
	info.Checksum, err = copyChecksum(data, opts, srcInfo)
	if err != nil {
		return object.Info{}, err
	}
	if l.quorumFail && int64(len(data)) > object.DefaultInlineThreshold {
		return object.Info{}, quorumError()
	}
	b.commit(dstKey, info, &content{data: data})
	return *info, nil
}

// readableLocked resolves a key for a read: the object must exist, must
// not be a delete marker (not applicable here), and must satisfy the
// conditions. It returns a copy of the info and the content.
func (l *Layer) readableLocked(bucketName, key string, opts object.GetOptions) (*object.Info, *content, error) {
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return nil, nil, err
	}
	info, ok := b.objects[key]
	if !ok {
		return nil, nil, s3err.Errorf(s3err.NoSuchKey, "no such key")
	}
	if err := opts.Conditions.EvaluateRead(*info); err != nil {
		return nil, nil, err
	}
	cp := *info
	return &cp, b.content[key], nil
}

// window is the addressed byte range of an object or part.
type window struct {
	base   int64 // offset of the selected part within the object
	off    int64 // absolute offset within the stored data
	length int64
	size   int64 // size of the selected content
}

// selectWindow resolves PartNumber and Range against a stored object.
func selectWindow(c *content, partNumber int, rng *object.HTTPRange) (window, error) {
	w := window{off: 0, length: int64(len(c.data)), size: int64(len(c.data))}
	if partNumber > 0 {
		if c.parts == nil {
			if partNumber != 1 {
				return window{}, s3err.Errorf(s3err.InvalidArgument, "part %d does not exist", partNumber)
			}
		} else {
			found := false
			for _, p := range c.parts {
				if p.number == partNumber {
					w.base, w.off, w.size, w.length = p.off, p.off, p.size, p.size
					found = true
					break
				}
			}
			if !found {
				return window{}, s3err.Errorf(s3err.InvalidArgument, "part %d does not exist", partNumber)
			}
		}
	}
	if rng != nil {
		off, length, err := rng.Resolve(w.size)
		if err != nil {
			return window{}, err
		}
		w.off = w.base + off
		w.length = length
	}
	return w, nil
}

func checkPartNumberOpt(n int) error {
	if n == 0 {
		return nil
	}
	return checkPartNumber(n)
}

func (l *Layer) bucketLocked(name string) (*bucket, error) {
	b, ok := l.buckets[name]
	if !ok {
		return nil, s3err.Errorf(s3err.NoSuchBucket, "no such bucket")
	}
	return b, nil
}

// currentLocked reports whether key exists and its ETag.
func (l *Layer) currentLocked(b *bucket, key string) (bool, string) {
	info, ok := b.objects[key]
	if !ok {
		return false, ""
	}
	return true, info.ETag
}

func (l *Layer) hasUploadsLocked(bucketName string) bool {
	for _, u := range l.uploads {
		if u.bucket == bucketName {
			return true
		}
	}
	return false
}

// commit stores an object version, replacing the previous one.
func (b *bucket) commit(key string, info *object.Info, c *content) {
	b.objects[key] = info
	b.content[key] = c
}

func quorumError() error {
	return fmt.Errorf("objecttest: write quorum: %w", erasure.ErrWriteQuorum)
}

// newInfo builds the info of a new version from the put options.
func newInfo(bucketName, key string, now time.Time, md5sum [16]byte, opts object.PutOptions) *object.Info {
	info := &object.Info{
		Bucket:             bucketName,
		Key:                key,
		IsLatest:           true,
		ETag:               hex.EncodeToString(md5sum[:]),
		ModTime:            now,
		ContentType:        opts.ContentType,
		ContentEncoding:    opts.ContentEncoding,
		ContentDisposition: opts.ContentDisposition,
		ContentLanguage:    opts.ContentLanguage,
		CacheControl:       opts.CacheControl,
		Expires:            opts.Expires,
		UserMetadata:       cloneMap(opts.UserMetadata),
		Tags:               opts.Tags,
		StorageClass:       opts.StorageClass,
	}
	return info
}

// copyAttributes copies the client-visible metadata of src into dst.
func copyAttributes(dst, src *object.Info) {
	dst.ContentType = src.ContentType
	dst.ContentEncoding = src.ContentEncoding
	dst.ContentDisposition = src.ContentDisposition
	dst.ContentLanguage = src.ContentLanguage
	dst.CacheControl = src.CacheControl
	dst.Expires = src.Expires
	dst.UserMetadata = cloneMap(src.UserMetadata)
	dst.Tags = src.Tags
	dst.StorageClass = src.StorageClass
}

// copyChecksum returns the checksum of a copied object: the requested
// algorithm, else the source's full-object checksum under the copy directive.
func copyChecksum(data []byte, opts object.CopyOptions, src *object.Info) (object.Checksum, error) {
	alg := opts.Put.ChecksumAlgorithm
	if alg == object.ChecksumNone && opts.Directive == object.MetadataCopy &&
		src.Checksum.Type == object.ChecksumFullObject {
		alg = src.Checksum.Algorithm
	}
	if alg == object.ChecksumNone {
		return object.Checksum{}, nil
	}
	return fullChecksum(alg, data)
}

func fullChecksum(alg object.ChecksumAlgorithm, data []byte) (object.Checksum, error) {
	h, err := digest.New(alg)
	if err != nil {
		return object.Checksum{}, err
	}
	h.Write(data)
	return object.Checksum{Algorithm: alg, Type: object.ChecksumFullObject, Value: h.Sum(nil)}, nil
}

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
