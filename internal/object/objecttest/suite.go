package objecttest

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine/digest"
	"github.com/eukalpia/unio/internal/s3err"
)

const (
	testBucket = "conformance"
	mib        = 1 << 20
)

// inlineSize is the largest object the default inline threshold keeps in
// metadata, and the first size that uses the erasure path.
var inlineSize = int(object.DefaultInlineThreshold)

// RunLayerSuite runs the conformance suite. newLayer must return a fresh,
// empty layer for each call; the suite calls it once per subtest. The suite
// covers buckets, objects, metadata, conditional requests, ranges, copies,
// listings, multipart uploads and failure injection.
//
// Optional capabilities (Flusher, DataCounter, QuorumFaults) enable the
// checks for asynchronous cleanup, leaked data and lost write quorums.
func RunLayerSuite(t *testing.T, newLayer func(t *testing.T) object.Layer) {
	t.Helper()
	groups := []struct {
		name string
		run  func(*testing.T, func(*testing.T) object.Layer)
	}{
		{"buckets", suiteBuckets},
		{"objects", suiteObjects},
		{"conditional", suiteConditional},
		{"ranges", suiteRanges},
		{"copy", suiteCopy},
		{"list", suiteList},
		{"multipart", suiteMultipart},
		{"tags", suiteTags},
		{"failures", suiteFailures},
		{"aliasing", suiteAliasing},
	}
	for _, g := range groups {
		t.Run(g.name, func(t *testing.T) { g.run(t, newLayer) })
	}
}

func suiteBuckets(t *testing.T, newLayer func(*testing.T) object.Layer) {
	t.Run("lifecycle", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		info, err := l.MakeBucket(ctx, "bucket-a", object.MakeBucketOptions{Owner: "alice"})
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != "bucket-a" || info.Owner != "alice" || info.Created.IsZero() {
			t.Fatalf("bucket info = %+v", info)
		}
		_, err = l.MakeBucket(ctx, "bucket-a", object.MakeBucketOptions{Owner: "alice"})
		expectCode(t, err, s3err.BucketAlreadyOwnedByYou)
		_, err = l.MakeBucket(ctx, "bucket-a", object.MakeBucketOptions{Owner: "bob"})
		expectCode(t, err, s3err.BucketAlreadyExists)
		_, err = l.MakeBucket(ctx, "Bad_Name", object.MakeBucketOptions{Owner: "alice"})
		expectCode(t, err, s3err.InvalidBucketName)

		if _, err := l.MakeBucket(ctx, "bucket-b", object.MakeBucketOptions{Owner: "alice"}); err != nil {
			t.Fatal(err)
		}
		all, err := l.ListBuckets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 2 || all[0].Name != "bucket-a" || all[1].Name != "bucket-b" {
			t.Fatalf("ListBuckets = %+v, want bucket-a, bucket-b in order", names(all))
		}
		got, err := l.GetBucketInfo(ctx, "bucket-b")
		if err != nil || got.Name != "bucket-b" {
			t.Fatalf("GetBucketInfo = %+v, %v", got, err)
		}
		_, err = l.GetBucketInfo(ctx, "missing-bucket")
		expectCode(t, err, s3err.NoSuchBucket)

		expectCode(t, l.DeleteBucket(ctx, "bucket-a", object.DeleteBucketOptions{}), "")
		expectCode(t, l.DeleteBucket(ctx, "bucket-a", object.DeleteBucketOptions{}), s3err.NoSuchBucket)
		_, err = l.GetBucketInfo(ctx, "bucket-a")
		expectCode(t, err, s3err.NoSuchBucket)
	})

	t.Run("not empty and force", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		mustPut(t, l, "k", []byte("v"))
		expectCode(t, l.DeleteBucket(ctx, bucket, object.DeleteBucketOptions{}), s3err.BucketNotEmpty)
		expectCode(t, l.DeleteBucket(ctx, bucket, object.DeleteBucketOptions{Force: true}), "")
		_, err := l.GetBucketInfo(ctx, bucket)
		expectCode(t, err, s3err.NoSuchBucket)
	})

	t.Run("invalid bucket names are rejected everywhere", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		_, err := l.ListObjects(ctx, "ab", object.ListOptions{})
		expectCode(t, err, s3err.InvalidBucketName)
		_, err = l.PutObject(ctx, "A", "k", bodyOf([]byte("x")), object.PutOptions{})
		expectCode(t, err, s3err.InvalidBucketName)
	})
}

func suiteObjects(t *testing.T, newLayer func(*testing.T) object.Layer) {
	sizes := []int{0, inlineSize, inlineSize + 1, 5*mib + 3}
	for _, size := range sizes {
		t.Run("roundtrip "+sizeName(size), func(t *testing.T) {
			l := newLayer(t)
			ctx := t.Context()
			bucket := mustBucket(t, l)
			data := payload(size, seedOf(size))
			info := mustPut(t, l, "obj", data)
			if info.Size != int64(size) || info.ETag != md5Hex(data) || info.Key != "obj" || info.Bucket != bucket {
				t.Fatalf("put info = %+v", info)
			}
			if info.ModTime.IsZero() {
				t.Fatal("put info has no modification time")
			}
			head, err := l.HeadObject(ctx, bucket, "obj", object.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if head.Size != int64(size) || head.ETag != info.ETag || !head.IsLatest {
				t.Fatalf("head = %+v", head)
			}
			if got := mustRead(t, l, "obj", object.GetOptions{}); !bytes.Equal(got, data) {
				t.Fatalf("get returned %d bytes that differ from the %d written", len(got), size)
			}
			r, err := l.GetObject(ctx, bucket, "obj", object.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { closeReader(t, r) })
			if r.Offset != 0 || r.Length != int64(size) || r.Info.ETag != info.ETag {
				t.Fatalf("reader window = [%d,+%d) info=%+v", r.Offset, r.Length, r.Info)
			}
		})
	}

	t.Run("metadata round trip", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		opts := object.PutOptions{
			ContentType:        "text/plain",
			ContentEncoding:    "gzip",
			ContentDisposition: `attachment; filename="a.txt"`,
			ContentLanguage:    "en",
			CacheControl:       "max-age=60",
			Expires:            "Wed, 21 Oct 2037 07:28:00 GMT",
			UserMetadata:       map[string]string{"owner": "team-a", "rev": "7"},
			Tags:               "env=prod&tier=gold",
			StorageClass:       "STANDARD",
			ChecksumAlgorithm:  object.ChecksumCRC32C,
		}
		if _, err := l.PutObject(ctx, bucket, "meta", bodyOf([]byte("hello"), WithChecksum(object.ChecksumCRC32C)), opts); err != nil {
			t.Fatal(err)
		}
		head, err := l.HeadObject(ctx, bucket, "meta", object.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		checkMetadata(t, head, opts)
		r, err := l.GetObject(ctx, bucket, "meta", object.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		checkMetadata(t, r.Info, opts)
		closeReader(t, r)
	})

	t.Run("overwrite returns the new version and frees the old data", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		first := payload(5*mib, 1)
		second := payload(5*mib+1, 2)
		mustPut(t, l, "k", first)
		base := dataCount(t, l)
		info := mustPut(t, l, "k", second)
		if info.ETag != md5Hex(second) {
			t.Fatalf("overwrite ETag = %s", info.ETag)
		}
		if got := mustRead(t, l, "k", object.GetOptions{}); !bytes.Equal(got, second) {
			t.Fatal("overwrite not visible")
		}
		if _, err := l.HeadObject(ctx, bucket, "k", object.GetOptions{}); err != nil {
			t.Fatal(err)
		}
		flush(t, l)
		if base >= 0 {
			if got := dataCount(t, l); got != base {
				t.Fatalf("stored data units after overwrite = %d, want %d (old data must be freed)", got, base)
			}
		}
	})

	t.Run("keys", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		long := string(bytes.Repeat([]byte("k"), object.MaxKeyLength))
		for _, key := range []string{"dir/sub/file.txt", "ünïcode key", long} {
			mustPut(t, l, key, []byte(key))
			if got := mustRead(t, l, key, object.GetOptions{}); string(got) != key {
				t.Fatalf("key %q: got %q", key, got)
			}
		}
		_, err := l.PutObject(ctx, bucket, "", bodyOf([]byte("x")), object.PutOptions{})
		expectCode(t, err, s3err.InvalidArgument)
		_, err = l.PutObject(ctx, bucket, string(bytes.Repeat([]byte("k"), object.MaxKeyLength+1)), bodyOf([]byte("x")), object.PutOptions{})
		expectCode(t, err, s3err.KeyTooLongError)
		_, err = l.HeadObject(ctx, bucket, "a\x00b", object.GetOptions{})
		expectCode(t, err, s3err.InvalidArgument)
	})

	t.Run("metadata size limit", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		big := map[string]string{"k": string(bytes.Repeat([]byte("v"), object.MaxUserMetadataSize))}
		_, err := l.PutObject(t.Context(), testBucket, "k", bodyOf([]byte("x")), object.PutOptions{UserMetadata: big})
		expectCode(t, err, s3err.MetadataTooLarge)
	})

	t.Run("missing bucket and key", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		_, err := l.PutObject(ctx, "no-bucket", "k", bodyOf([]byte("x")), object.PutOptions{})
		expectCode(t, err, s3err.NoSuchBucket)
		_, err = l.GetObject(ctx, "no-bucket", "k", object.GetOptions{})
		expectCode(t, err, s3err.NoSuchBucket)
		mustBucket(t, l)
		_, err = l.GetObject(ctx, testBucket, "absent", object.GetOptions{})
		expectCode(t, err, s3err.NoSuchKey)
		_, err = l.HeadObject(ctx, testBucket, "absent", object.GetOptions{})
		expectCode(t, err, s3err.NoSuchKey)
	})

	t.Run("delete", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		mustPut(t, l, "big", payload(5*mib, 3))
		mustPut(t, l, "small", []byte("s"))
		base := dataCount(t, l)
		res, err := l.DeleteObject(ctx, bucket, "big", object.DeleteOptions{})
		if err != nil || res.Key != "big" || res.Err != nil {
			t.Fatalf("delete existing: %+v, %v", res, err)
		}
		_, err = l.GetObject(ctx, bucket, "big", object.GetOptions{})
		expectCode(t, err, s3err.NoSuchKey)
		_, err = l.HeadObject(ctx, bucket, "big", object.GetOptions{})
		expectCode(t, err, s3err.NoSuchKey)
		if _, err := l.DeleteObject(ctx, bucket, "big", object.DeleteOptions{}); err != nil {
			t.Fatalf("deleting a missing key must succeed: %v", err)
		}
		_, err = l.DeleteObject(ctx, "no-bucket", "big", object.DeleteOptions{})
		expectCode(t, err, s3err.NoSuchBucket)
		flush(t, l)
		if base >= 0 {
			if got := dataCount(t, l); got != base-1 {
				t.Fatalf("stored data units after delete = %d, want %d", got, base-1)
			}
		}
		if got := mustRead(t, l, "small", object.GetOptions{}); string(got) != "s" {
			t.Fatal("delete removed another object")
		}
	})

	t.Run("delete objects", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		mustPut(t, l, "a", []byte("1"))
		mustPut(t, l, "b", []byte("2"))
		results, err := l.DeleteObjects(ctx, bucket, []object.ToDelete{
			{Key: "a"}, {Key: "missing"}, {Key: ""}, {Key: "b"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 4 {
			t.Fatalf("got %d results, want 4", len(results))
		}
		if results[0].Key != "a" || results[0].Err != nil {
			t.Fatalf("result a = %+v", results[0])
		}
		if results[1].Err != nil {
			t.Fatalf("deleting a missing key must succeed: %v", results[1].Err)
		}
		if !s3err.Is(results[2].Err, s3err.InvalidArgument) {
			t.Fatalf("empty key result = %v, want InvalidArgument", results[2].Err)
		}
		if results[3].Err != nil {
			t.Fatalf("result b = %+v", results[3])
		}
		_, err = l.HeadObject(ctx, bucket, "a", object.GetOptions{})
		expectCode(t, err, s3err.NoSuchKey)

		tooMany := make([]object.ToDelete, object.MaxDeleteObjects+1)
		for i := range tooMany {
			tooMany[i].Key = "k"
		}
		_, err = l.DeleteObjects(ctx, bucket, tooMany)
		if err == nil {
			t.Fatal("more than 1000 keys must fail the request")
		}
		_, err = l.DeleteObjects(ctx, "no-bucket", []object.ToDelete{{Key: "a"}})
		expectCode(t, err, s3err.NoSuchBucket)
	})

	t.Run("cancelled context stores nothing", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := l.PutObject(ctx, testBucket, "k", bodyOf([]byte("x")), object.PutOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("put with cancelled context: err = %v", err)
		}
		_, err = l.HeadObject(t.Context(), testBucket, "k", object.GetOptions{})
		expectCode(t, err, s3err.NoSuchKey)
	})
}

// checkMetadata compares the attributes of info with the put options.
func checkMetadata(t *testing.T, info object.Info, opts object.PutOptions) {
	t.Helper()
	if info.ContentType != opts.ContentType || info.ContentEncoding != opts.ContentEncoding ||
		info.ContentDisposition != opts.ContentDisposition || info.ContentLanguage != opts.ContentLanguage ||
		info.CacheControl != opts.CacheControl || info.Expires != opts.Expires ||
		info.Tags != opts.Tags || info.StorageClass != opts.StorageClass {
		t.Fatalf("metadata = %+v, want %+v", info, opts)
	}
	if len(info.UserMetadata) != len(opts.UserMetadata) {
		t.Fatalf("user metadata = %v, want %v", info.UserMetadata, opts.UserMetadata)
	}
	for k, v := range opts.UserMetadata {
		if info.UserMetadata[k] != v {
			t.Fatalf("user metadata %q = %q, want %q", k, info.UserMetadata[k], v)
		}
	}
	if info.Checksum.Algorithm != object.ChecksumCRC32C || info.Checksum.Type != object.ChecksumFullObject {
		t.Fatalf("checksum = %+v, want a full-object CRC32C", info.Checksum)
	}
}

// mustBucket creates the suite bucket.
func mustBucket(t *testing.T, l object.Layer) string {
	t.Helper()
	if _, err := l.MakeBucket(t.Context(), testBucket, object.MakeBucketOptions{Owner: "owner"}); err != nil {
		t.Fatal(err)
	}
	return testBucket
}

// mustPut stores data under key in the suite bucket.
func mustPut(t *testing.T, l object.Layer, key string, data []byte) object.Info {
	t.Helper()
	info, err := l.PutObject(t.Context(), testBucket, key, bodyOf(data), object.PutOptions{})
	if err != nil {
		t.Fatalf("put %q: %v", key, err)
	}
	return info
}

// mustRead reads the whole object and checks the reader length.
func mustRead(t *testing.T, l object.Layer, key string, opts object.GetOptions) []byte {
	t.Helper()
	r, err := l.GetObject(t.Context(), testBucket, key, opts)
	if err != nil {
		t.Fatalf("get %q: %v", key, err)
	}
	t.Cleanup(func() { closeReader(t, r) })
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	if int64(len(got)) != r.Length {
		t.Fatalf("read %d bytes, reader says %d", len(got), r.Length)
	}
	return got
}

// bodyOf wraps data in a clean test body.
func bodyOf(data []byte, opts ...BodyOption) *Body {
	return NewBody(data, opts...)
}

// expectCode checks that err carries code, or is nil when code is empty.
func expectCode(t *testing.T, err error, code s3err.Code) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if !s3err.Is(err, code) {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

// flush waits for asynchronous cleanup when the layer supports it.
func flush(t *testing.T, l object.Layer) {
	t.Helper()
	if f, ok := l.(Flusher); ok {
		if err := f.Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

// dataCount returns the stored data units, or -1 when the layer does not
// report them.
func dataCount(t *testing.T, l object.Layer) int {
	t.Helper()
	if c, ok := l.(DataCounter); ok {
		return c.StoredDataCount()
	}
	return -1
}

// payload returns n deterministic pseudo-random bytes.
func payload(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, 0x756e696f)) // #nosec G404 -- deterministic test data, not a security boundary
	b := make([]byte, n)
	var word [8]byte
	for i := 0; i < n; i += len(word) {
		binary.LittleEndian.PutUint64(word[:], r.Uint64())
		copy(b[i:], word[:])
	}
	return b
}

// seedOf derives the payload seed of a size. Sizes are never negative; the
// check keeps that explicit before the conversion.
func seedOf(n int) uint64 {
	if n < 0 {
		return 0
	}
	return uint64(n) // #nosec G115 -- n is non-negative, checked above
}

// closeReader closes r and fails the test if Close reports an error, so that a
// broken reader cannot pass unnoticed.
func closeReader(t *testing.T, r *object.Reader) {
	t.Helper()
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
}

func md5Hex(data []byte) string {
	sum := digest.SumMD5(data)
	return hex.EncodeToString(sum[:])
}

func names(bs []object.BucketInfo) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Name
	}
	return out
}

func sizeName(n int) string {
	switch n {
	case 0:
		return "0 bytes"
	case inlineSize:
		return "at inline threshold"
	case inlineSize + 1:
		return "above inline threshold"
	case 5*mib + 3:
		return "5 MiB plus 3"
	}
	return fmt.Sprintf("%d bytes", n)
}
