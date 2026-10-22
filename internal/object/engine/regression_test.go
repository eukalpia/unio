package engine_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/engine/internal/testbackend"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3err"
)

// wrappedRegistry resolves erasure sets through reg and passes each one through
// wrap, to change the behaviour of the sets under test.
type wrappedRegistry struct {
	erasure.Registry
	wrap func(erasure.Set) erasure.Set
}

func (r wrappedRegistry) Place(ctx context.Context, bucket, key string, size int64) (erasure.Set, error) {
	s, err := r.Registry.Place(ctx, bucket, key, size)
	if err != nil {
		return nil, err
	}
	return r.wrap(s), nil
}

func (r wrappedRegistry) Get(id ids.SetID) (erasure.Set, error) {
	s, err := r.Registry.Get(id)
	if err != nil {
		return nil, err
	}
	return r.wrap(s), nil
}

// opaqueSet hides the identity of the errors its set returns, as a set that
// formats its errors with %v would.
type opaqueSet struct{ erasure.Set }

func (s opaqueSet) Write(ctx context.Context, id ids.DataID, r io.Reader, size int64) (erasure.Layout, error) {
	layout, err := s.Set.Write(ctx, id, r, size)
	if err != nil {
		return layout, fmt.Errorf("opaque set: %v", err)
	}
	return layout, nil
}

// stalledSet returns readers that never make progress.
type stalledSet struct{ erasure.Set }

func (s stalledSet) Read(context.Context, ids.DataID, erasure.Layout, int64, int64, int64) (io.ReadCloser, error) {
	return io.NopCloser(stalledReader{}), nil
}

type stalledReader struct{}

func (stalledReader) Read([]byte) (int, error) { return 0, nil }

// ambiguousStore applies every commit and then reports a lost
// acknowledgement, as a consensus store does when a proposal times out after
// it was accepted.
type ambiguousStore struct{ *testbackend.Store }

func (s ambiguousStore) PutObject(ctx context.Context, rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, error) {
	garbage, err := s.Store.PutObject(ctx, rec, cond)
	if err != nil {
		return garbage, err
	}
	return garbage, errors.New("store: acknowledgement lost after commit")
}

// flippingStore changes the versioning state of the bucket just before the
// first commit, as a concurrent administrative call would.
type flippingStore struct {
	*testbackend.Store
	flipped bool
}

func (s *flippingStore) PutObject(ctx context.Context, rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, error) {
	if !s.flipped {
		s.flipped = true
		if err := s.Store.SetBucketVersioning(ctx, rec.Bucket, object.VersioningEnabled); err != nil {
			return nil, err
		}
	}
	return s.Store.PutObject(ctx, rec, cond)
}

func versioningName(state object.VersioningState) string {
	switch state {
	case object.VersioningEnabled:
		return "enabled"
	case object.VersioningSuspended:
		return "suspended"
	}
	return "unversioned"
}

// TestForcedDeleteRefusesVersionedBucketWithoutMutating covers a forced
// deletion of a versioned or suspended bucket that still has an object. The
// call must fail before it changes anything: the object stays readable and
// its data stays stored.
func TestForcedDeleteRefusesVersionedBucketWithoutMutating(t *testing.T) {
	for _, state := range []object.VersioningState{object.VersioningEnabled, object.VersioningSuspended} {
		t.Run(versioningName(state), func(t *testing.T) {
			h := newHarness(t, engine.Options{})
			ctx := t.Context()
			if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o", Versioning: state}); err != nil {
				t.Fatal(err)
			}
			data := payloadOf(5000, 2)
			if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody(data), object.PutOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := h.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			before := h.reg.StoredData()

			err := h.DeleteBucket(ctx, testBucket, object.DeleteBucketOptions{Force: true})
			if !s3err.Is(err, s3err.BucketNotEmpty) {
				t.Fatalf("forced delete: err = %v, want BucketNotEmpty", err)
			}
			if got := mustReadKey(t, h, "k"); !bytes.Equal(got, data) {
				t.Fatal("object changed by a refused forced delete")
			}
			if err := h.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if got := h.reg.StoredData(); got != before {
				t.Fatalf("stored data %d after refused delete, want %d", got, before)
			}
		})
	}
}

// TestForcedDeleteOfEmptyVersionedBucketSucceeds checks that forced deletion
// still works when a versioned bucket has nothing left in it.
func TestForcedDeleteOfEmptyVersionedBucketSucceeds(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o", Versioning: object.VersioningEnabled}); err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteBucket(ctx, testBucket, object.DeleteBucketOptions{Force: true}); err != nil {
		t.Fatalf("forced delete of an empty versioned bucket: %v", err)
	}
	if _, err := h.GetBucketInfo(ctx, testBucket); !s3err.Is(err, s3err.NoSuchBucket) {
		t.Fatalf("bucket still exists: %v", err)
	}
}

// TestAmbiguousCommitKeepsCommittedData covers a commit that the store
// applied but reported as failed. The object is visible, so its data must not
// be deleted, even though the client saw an error.
func TestAmbiguousCommitKeepsCommittedData(t *testing.T) {
	store := testbackend.NewStore()
	reg := testbackend.NewRegistry(2, 2, 1)
	h := newHarnessWith(t, ambiguousStore{store}, reg, reg, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	data := payloadOf(5000, 6)
	if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody(data), object.PutOptions{}); err == nil {
		t.Fatal("put with a lost acknowledgement reported success")
	}
	head, err := h.HeadObject(ctx, testBucket, "k", object.GetOptions{})
	if err != nil || head.Size != int64(len(data)) {
		t.Fatalf("committed object not visible: %+v, %v", head, err)
	}
	if got := mustReadKey(t, h, "k"); !bytes.Equal(got, data) {
		t.Fatal("data of a committed object was deleted after an ambiguous commit")
	}
	if err := h.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := reg.StoredData(); n != 1 {
		t.Fatalf("stored data units = %d, want 1", n)
	}
}

// TestShortBodyUnderOpaqueSetErrorIsIncomplete covers a body that ends early
// while the erasure set reports the failure without wrapping it. The client
// must get IncompleteBody, not an internal error.
func TestShortBodyUnderOpaqueSetErrorIsIncomplete(t *testing.T) {
	reg := testbackend.NewRegistry(2, 2, 1)
	sets := wrappedRegistry{Registry: reg, wrap: func(s erasure.Set) erasure.Set { return opaqueSet{s} }}
	h := newHarnessWith(t, testbackend.NewStore(), reg, sets, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	body := &longBody{data: payloadOf(2000, 4), declared: 5000}
	_, err := h.PutObject(ctx, testBucket, "k", body, object.PutOptions{})
	if !s3err.Is(err, s3err.IncompleteBody) {
		t.Fatalf("err = %v, want IncompleteBody", err)
	}
	if _, err := h.HeadObject(ctx, testBucket, "k", object.GetOptions{}); !s3err.Is(err, s3err.NoSuchKey) {
		t.Fatalf("object visible after a short body: %v", err)
	}
}

// TestNilBodyIsRejected checks that a missing body is an error, not a panic.
func TestNilBodyIsRejected(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	_, err := h.PutObject(ctx, testBucket, "k", nil, object.PutOptions{})
	if !s3err.Is(err, s3err.InvalidArgument) {
		t.Fatalf("put with nil body: err = %v, want InvalidArgument", err)
	}
	id, err := h.NewMultipartUpload(ctx, testBucket, "k", object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.PutObjectPart(ctx, testBucket, "k", id, 1, nil)
	if !s3err.Is(err, s3err.InvalidArgument) {
		t.Fatalf("part with nil body: err = %v, want InvalidArgument", err)
	}
}

// TestVersioningChangeDuringWriteIsRetryable covers a bucket whose versioning
// changes between the preparation of a write and its commit. The client must
// get a retryable conflict, and the object must not become visible.
func TestVersioningChangeDuringWriteIsRetryable(t *testing.T) {
	store := &flippingStore{Store: testbackend.NewStore()}
	reg := testbackend.NewRegistry(2, 2, 1)
	h := newHarnessWith(t, store, reg, reg, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	_, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("inline")), object.PutOptions{})
	if !s3err.Is(err, s3err.ConditionalRequestConflict) {
		t.Fatalf("err = %v, want a retryable ConditionalRequestConflict", err)
	}
	if _, err := h.HeadObject(ctx, testBucket, "k", object.GetOptions{}); !s3err.Is(err, s3err.NoSuchKey) {
		t.Fatalf("object visible after a rejected write: %v", err)
	}
}

// TestCompletionRejectsForeignChecksumAlgorithm covers a completion whose
// part checksum names another algorithm than the upload's, even when the
// bytes happen to match.
func TestCompletionRejectsForeignChecksumAlgorithm(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	id, err := h.NewMultipartUpload(ctx, testBucket, "k", object.PutOptions{ChecksumAlgorithm: object.ChecksumCRC32C})
	if err != nil {
		t.Fatal(err)
	}
	info, err := h.PutObjectPart(ctx, testBucket, "k", id, 1,
		objecttest.NewBody([]byte("tail"), objecttest.WithChecksum(object.ChecksumCRC32C)))
	if err != nil {
		t.Fatal(err)
	}
	forged := object.CompletePart{
		PartNumber: 1,
		ETag:       info.ETag,
		Checksum:   object.Checksum{Algorithm: object.ChecksumSHA256, Value: info.Checksum.Value},
	}
	_, err = h.CompleteMultipartUpload(ctx, testBucket, "k", id, []object.CompletePart{forged}, object.PutOptions{})
	if !s3err.Is(err, s3err.InvalidPart) {
		t.Fatalf("forged algorithm: err = %v, want InvalidPart", err)
	}
	genuine := object.CompletePart{PartNumber: 1, ETag: info.ETag, Checksum: info.Checksum}
	if _, err := h.CompleteMultipartUpload(ctx, testBucket, "k", id, []object.CompletePart{genuine}, object.PutOptions{}); err != nil {
		t.Fatalf("genuine checksum rejected: %v", err)
	}
}

// TestStalledPartReaderIsBounded covers a part reader that returns neither
// data nor an error. The read must fail instead of spinning.
func TestStalledPartReaderIsBounded(t *testing.T) {
	reg := testbackend.NewRegistry(2, 2, 1)
	sets := wrappedRegistry{Registry: reg, wrap: func(s erasure.Set) erasure.Set { return stalledSet{s} }}
	h := newHarnessWith(t, testbackend.NewStore(), reg, sets, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody(payloadOf(5000, 8)), object.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	r, err := h.GetObject(ctx, testBucket, "k", object.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := io.ReadAll(r); !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("read from a stalled part: err = %v, want io.ErrNoProgress", err)
	}
}

// TestMetadataIsNotSharedWithCallers checks that the user metadata returned to
// a caller is a copy, so that changing it cannot alter the stored object.
func TestMetadataIsNotSharedWithCallers(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("x")),
		object.PutOptions{UserMetadata: map[string]string{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	first, err := h.HeadObject(ctx, testBucket, "k", object.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first.UserMetadata["k"] = "changed"
	second, err := h.HeadObject(ctx, testBucket, "k", object.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.UserMetadata["k"] != "v" {
		t.Fatalf("stored metadata changed through a returned value: %q", second.UserMetadata["k"])
	}
}

// mustReadKey reads the whole object key from the test bucket.
func mustReadKey(t *testing.T, h *harness, key string) []byte {
	t.Helper()
	r, err := h.GetObject(t.Context(), testBucket, key, object.GetOptions{})
	if err != nil {
		t.Fatalf("get %q: %v", key, err)
	}
	return readAll(t, r)
}

// cachingStore returns the same listing slices on every call, as a store that
// keeps its results would. The engine must hand callers copies of them.
type cachingStore struct {
	*testbackend.Store
	prefixes       []string
	uploadPrefixes []string
}

func (s *cachingStore) ListObjects(ctx context.Context, bucket string, opts object.ListOptions) (meta.ListResult, error) {
	res, err := s.Store.ListObjects(ctx, bucket, opts)
	if err != nil {
		return res, err
	}
	if s.prefixes == nil {
		s.prefixes = res.CommonPrefixes
	}
	res.CommonPrefixes = s.prefixes
	return res, nil
}

func (s *cachingStore) ListUploads(ctx context.Context, bucket string, opts object.ListUploadsOptions) (object.ListUploadsResult, error) {
	res, err := s.Store.ListUploads(ctx, bucket, opts)
	if err != nil {
		return res, err
	}
	if s.uploadPrefixes == nil {
		s.uploadPrefixes = res.CommonPrefixes
	}
	res.CommonPrefixes = s.uploadPrefixes
	return res, nil
}

// TestListingResultsDoNotAliasTheStore checks that changing a listing a caller
// received cannot change the next listing, even when the store reuses its
// result slices.
func TestListingResultsDoNotAliasTheStore(t *testing.T) {
	store := &cachingStore{Store: testbackend.NewStore()}
	reg := testbackend.NewRegistry(2, 2, 1)
	h := newHarnessWith(t, store, reg, reg, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "a/1", objecttest.NewBody([]byte("x")), object.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	opts := object.ListOptions{Delimiter: "/"}
	res, err := h.ListObjects(ctx, testBucket, opts)
	if err != nil || len(res.CommonPrefixes) != 1 {
		t.Fatalf("list: %+v, %v", res, err)
	}
	res.CommonPrefixes[0] = "zzz"
	again, err := h.ListObjects(ctx, testBucket, opts)
	if err != nil || len(again.CommonPrefixes) != 1 || again.CommonPrefixes[0] != "a/" {
		t.Fatalf("listing changed by the caller: %+v, %v", again.CommonPrefixes, err)
	}

	if _, err := h.NewMultipartUpload(ctx, testBucket, "d/x", object.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	up, err := h.ListMultipartUploads(ctx, testBucket, object.ListUploadsOptions{Delimiter: "/"})
	if err != nil || len(up.CommonPrefixes) != 1 {
		t.Fatalf("list uploads: %+v, %v", up, err)
	}
	up.CommonPrefixes[0] = "zzz"
	upAgain, err := h.ListMultipartUploads(ctx, testBucket, object.ListUploadsOptions{Delimiter: "/"})
	if err != nil || len(upAgain.CommonPrefixes) != 1 || upAgain.CommonPrefixes[0] != "d/" {
		t.Fatalf("upload listing changed by the caller: %+v, %v", upAgain.CommonPrefixes, err)
	}
}
