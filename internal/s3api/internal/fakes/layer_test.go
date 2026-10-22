package fakes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// layerCalls returns one call per object.Layer method, each operating on
// testBucket/"k" with the upload id given. The context argument is the one
// the test controls.
func layerCalls(l *Layer, uploadID string) map[string]func(ctx context.Context, bucket string) error {
	const key = "k"
	return map[string]func(ctx context.Context, bucket string) error{
		"MakeBucket": func(ctx context.Context, bucket string) error {
			_, err := l.MakeBucket(ctx, bucket, object.MakeBucketOptions{})
			return err
		},
		"GetBucketInfo": func(ctx context.Context, bucket string) error {
			_, err := l.GetBucketInfo(ctx, bucket)
			return err
		},
		"ListBuckets": func(ctx context.Context, _ string) error {
			_, err := l.ListBuckets(ctx)
			return err
		},
		"DeleteBucket": func(ctx context.Context, bucket string) error {
			return l.DeleteBucket(ctx, bucket, object.DeleteBucketOptions{})
		},
		"PutObject": func(ctx context.Context, bucket string) error {
			_, err := l.PutObject(ctx, bucket, key, newTestBody([]byte("v")), object.PutOptions{})
			return err
		},
		"GetObject": func(ctx context.Context, bucket string) error {
			_, err := l.GetObject(ctx, bucket, key, object.GetOptions{})
			return err
		},
		"HeadObject": func(ctx context.Context, bucket string) error {
			_, err := l.HeadObject(ctx, bucket, key, object.GetOptions{})
			return err
		},
		"DeleteObject": func(ctx context.Context, bucket string) error {
			_, err := l.DeleteObject(ctx, bucket, key, object.DeleteOptions{})
			return err
		},
		"DeleteObjects": func(ctx context.Context, bucket string) error {
			_, err := l.DeleteObjects(ctx, bucket, []object.ToDelete{{Key: key}})
			return err
		},
		"CopyObject": func(ctx context.Context, bucket string) error {
			_, err := l.CopyObject(ctx, object.Ref{Bucket: bucket, Key: key}, bucket, "copy", object.CopyOptions{})
			return err
		},
		"ListObjects": func(ctx context.Context, bucket string) error {
			_, err := l.ListObjects(ctx, bucket, object.ListOptions{})
			return err
		},
		"NewMultipartUpload": func(ctx context.Context, bucket string) error {
			_, err := l.NewMultipartUpload(ctx, bucket, key, object.PutOptions{})
			return err
		},
		"PutObjectPart": func(ctx context.Context, bucket string) error {
			_, err := l.PutObjectPart(ctx, bucket, key, uploadID, 1, newTestBody([]byte("v")))
			return err
		},
		"CopyObjectPart": func(ctx context.Context, bucket string) error {
			_, err := l.CopyObjectPart(ctx, object.Ref{Bucket: bucket, Key: key}, bucket, key, uploadID, 1, object.CopyOptions{})
			return err
		},
		"ListParts": func(ctx context.Context, bucket string) error {
			_, err := l.ListParts(ctx, bucket, key, uploadID, object.ListPartsOptions{})
			return err
		},
		"CompleteMultipartUpload": func(ctx context.Context, bucket string) error {
			_, err := l.CompleteMultipartUpload(ctx, bucket, key, uploadID, []object.CompletePart{{PartNumber: 1}}, object.PutOptions{})
			return err
		},
		"AbortMultipartUpload": func(ctx context.Context, bucket string) error {
			return l.AbortMultipartUpload(ctx, bucket, key, uploadID)
		},
		"ListMultipartUploads": func(ctx context.Context, bucket string) error {
			_, err := l.ListMultipartUploads(ctx, bucket, object.ListUploadsOptions{})
			return err
		},
	}
}

func TestCanceledContextIsReturnedUnchanged(t *testing.T) {
	l := newTestLayer(t)
	mustPut(t, l, "k", []byte("v"))
	id := mustUpload(t, l, "k", object.PutOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, call := range layerCalls(l, id) {
		t.Run(name, func(t *testing.T) {
			if err := call(ctx, testBucket); !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestMissingBucketReturnsNoSuchBucket(t *testing.T) {
	l := newTestLayer(t)
	mustPut(t, l, "k", []byte("v"))
	id := mustUpload(t, l, "k", object.PutOptions{})

	noBucket := map[string]bool{"MakeBucket": true, "ListBuckets": true}
	for name, call := range layerCalls(l, id) {
		if noBucket[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			wantCode(t, call(context.Background(), "missing-bucket"), s3err.NoSuchBucket)
		})
	}
}

func TestBucketCheckPrecedesArgumentChecks(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	id := mustUpload(t, l, "k", object.PutOptions{})

	_, err := l.ListObjects(ctx, "missing-bucket", object.ListOptions{MaxKeys: -1})
	wantCode(t, err, s3err.NoSuchBucket)
	_, err = l.ListMultipartUploads(ctx, "missing-bucket", object.ListUploadsOptions{MaxUploads: -1})
	wantCode(t, err, s3err.NoSuchBucket)
	_, err = l.ListParts(ctx, "missing-bucket", "k", id, object.ListPartsOptions{MaxParts: -1})
	wantCode(t, err, s3err.NoSuchBucket)
	_, err = l.PutObjectPart(ctx, "missing-bucket", "k", id, 0, newTestBody(nil))
	wantCode(t, err, s3err.NoSuchBucket)
	_, err = l.CopyObjectPart(ctx, object.Ref{Bucket: testBucket, Key: "k"}, "missing-bucket", "k", id, 0, object.CopyOptions{})
	wantCode(t, err, s3err.NoSuchBucket)
}

func TestUnknownUploadReturnsNoSuchUpload(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	const id = "00000000000000000000000000000000"
	_, err := l.PutObjectPart(ctx, testBucket, "k", id, 1, newTestBody([]byte("v")))
	wantCode(t, err, s3err.NoSuchUpload)
	_, err = l.ListParts(ctx, testBucket, "k", id, object.ListPartsOptions{})
	wantCode(t, err, s3err.NoSuchUpload)
	_, err = l.CompleteMultipartUpload(ctx, testBucket, "k", id, []object.CompletePart{{PartNumber: 1}}, object.PutOptions{})
	wantCode(t, err, s3err.NoSuchUpload)
	wantCode(t, l.AbortMultipartUpload(ctx, testBucket, "k", id), s3err.NoSuchUpload)
}

func TestNilBodyIsRejected(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	_, err := l.PutObject(ctx, testBucket, "k", nil, object.PutOptions{})
	wantCode(t, err, s3err.InvalidRequest)
	id := mustUpload(t, l, "k", object.PutOptions{})
	_, err = l.PutObjectPart(ctx, testBucket, "k", id, 1, nil)
	wantCode(t, err, s3err.InvalidRequest)
}

func TestZeroValueLayerIsUsable(t *testing.T) {
	var l Layer
	if _, err := l.MakeBucket(context.Background(), testBucket, object.MakeBucketOptions{}); err != nil {
		t.Fatalf("MakeBucket on zero value: %v", err)
	}
	mustPut(t, &l, "k", []byte("v"))
	if got := mustRead(t, &l, "k"); string(got) != "v" {
		t.Errorf("content = %q, want v", got)
	}
}

func TestConcurrentObjectOperations(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	const workers, perWorker = 8, 50

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				key := fmt.Sprintf("w%d/k%03d", w, i)
				data := []byte(key)
				if _, err := l.PutObject(ctx, testBucket, key, newTestBody(data), object.PutOptions{}); err != nil {
					t.Errorf("PutObject(%q): %v", key, err)
					return
				}
				r, err := l.GetObject(ctx, testBucket, key, object.GetOptions{})
				if err != nil {
					t.Errorf("GetObject(%q): %v", key, err)
					return
				}
				got, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil || !bytes.Equal(got, data) {
					t.Errorf("GetObject(%q) content = %q err %v, want %q", key, got, err, data)
					return
				}
				if _, err := l.ListObjects(ctx, testBucket, object.ListOptions{Prefix: fmt.Sprintf("w%d/", w)}); err != nil {
					t.Errorf("ListObjects: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	res, err := l.ListObjects(ctx, testBucket, object.ListOptions{})
	wantCode(t, err, "")
	if len(res.Objects) != workers*perWorker || res.IsTruncated {
		t.Errorf("listing has %d objects truncated=%v, want %d", len(res.Objects), res.IsTruncated, workers*perWorker)
	}
}
