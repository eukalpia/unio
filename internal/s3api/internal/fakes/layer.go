package fakes

import (
	"sync"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// DefaultOwner is the bucket owner used when MakeBucketOptions.Owner is empty.
const DefaultOwner = "fake-owner"

// defaultStorageClass is recorded when PutOptions.StorageClass is empty.
const defaultStorageClass = "STANDARD"

var _ object.Layer = (*Layer)(nil)

// Layer is an in-memory object.Layer for tests. All state lives behind one
// mutex. Object content is held in byte slices that are never modified after
// they are stored: an overwrite or delete replaces the entry, so a Reader
// keeps serving the content it was opened on. Layer is safe for concurrent
// use. The zero value is ready to use; NewLayer returns one for convenience.
type Layer struct {
	mu      sync.Mutex
	nextID  uint64
	buckets map[string]*bucket
}

// NewLayer returns an empty in-memory Layer.
func NewLayer() *Layer {
	return &Layer{}
}

// bucket is the state of one bucket. Its fields are guarded by Layer.mu.
type bucket struct {
	info    object.BucketInfo
	objects map[string]*storedObject
	uploads map[string]*upload
}

// storedObject is the current version of one key. It is immutable once stored.
type storedObject struct {
	info object.Info
	data []byte
}

// upload is an in-progress multipart upload. Its fields are guarded by
// Layer.mu.
type upload struct {
	key       string
	id        string
	opts      object.PutOptions
	initiated int64
	parts     map[int]*storedPart
}

// storedPart is one uploaded part. It is immutable once stored.
type storedPart struct {
	info   object.PartInfo
	digest [16]byte
	data   []byte
}

// bucketLocked returns the named bucket, or NoSuchBucket. l.mu must be held.
func (l *Layer) bucketLocked(name string) (*bucket, error) {
	b, ok := l.buckets[name]
	if !ok {
		return nil, s3err.Errorf(s3err.NoSuchBucket, "bucket %q does not exist", name)
	}
	return b, nil
}

// uploadLocked resolves bucketName and checks that uploadID names an upload
// of key in it. l.mu must be held.
func (l *Layer) uploadLocked(bucketName, key, uploadID string) (*bucket, *upload, error) {
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return nil, nil, err
	}
	if err := object.ValidateKey(key); err != nil {
		return nil, nil, err
	}
	u, ok := b.uploads[uploadID]
	if !ok || u.key != key {
		return nil, nil, s3err.Errorf(s3err.NoSuchUpload, "upload %q does not exist", uploadID)
	}
	return b, u, nil
}

// checkBucketKey verifies that bucketName exists and key is valid. It takes
// l.mu.
func (l *Layer) checkBucketKey(bucketName, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.bucketLocked(bucketName); err != nil {
		return err
	}
	return object.ValidateKey(key)
}
