package fakes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

const copySourceBody = "source body"

// newCopyFixture returns a layer with testBucket, archive-bucket and an
// object "src" carrying content attributes, user metadata and tags.
func newCopyFixture(t *testing.T) *Layer {
	t.Helper()
	ctx := context.Background()
	l := newTestLayer(t)
	if _, err := l.MakeBucket(ctx, "archive-bucket", object.MakeBucketOptions{}); err != nil {
		t.Fatalf("MakeBucket: %v", err)
	}
	_, err := l.PutObject(ctx, testBucket, "src", newTestBody([]byte(copySourceBody)), object.PutOptions{
		ContentType:  "text/plain",
		UserMetadata: map[string]string{"k": "v"},
		Tags:         "a=b",
		StorageClass: "REDUCED_REDUNDANCY",
	})
	wantCode(t, err, "")
	return l
}

func TestCopyObjectDirectives(t *testing.T) {
	tests := []struct {
		name      string
		directive object.MetadataDirective
		put       object.PutOptions
		wantType  string
		wantMeta  map[string]string
		wantTags  string
		wantClass string
	}{
		{
			name:      "COPY takes source attributes",
			directive: object.MetadataCopy,
			wantType:  "text/plain",
			wantMeta:  map[string]string{"k": "v"},
			wantTags:  "a=b",
			wantClass: "REDUCED_REDUNDANCY",
		},
		{
			name:      "REPLACE takes destination attributes",
			directive: object.MetadataReplace,
			put:       object.PutOptions{ContentType: "application/json", UserMetadata: map[string]string{"x": "y"}},
			wantType:  "application/json",
			wantMeta:  map[string]string{"x": "y"},
			wantClass: "STANDARD",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newCopyFixture(t)
			info, err := l.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "src"}, "archive-bucket", "dst",
				object.CopyOptions{Directive: tt.directive, Put: tt.put})
			wantCode(t, err, "")

			if info.Bucket != "archive-bucket" || info.Key != "dst" {
				t.Errorf("destination = %s/%s, want archive-bucket/dst", info.Bucket, info.Key)
			}
			if info.ETag != md5Hex([]byte(copySourceBody)) || info.Size != int64(len(copySourceBody)) {
				t.Errorf("ETag/Size = %q/%d, want the source ETag and size", info.ETag, info.Size)
			}
			if info.ContentType != tt.wantType || info.Tags != tt.wantTags || info.StorageClass != tt.wantClass {
				t.Errorf("attributes = type %q tags %q class %q, want type %q tags %q class %q",
					info.ContentType, info.Tags, info.StorageClass, tt.wantType, tt.wantTags, tt.wantClass)
			}
			if diff := cmp.Diff(tt.wantMeta, info.UserMetadata, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("user metadata mismatch (-want +got):\n%s", diff)
			}

			r, err := l.GetObject(ctx, "archive-bucket", "dst", object.GetOptions{})
			wantCode(t, err, "")
			defer func() { _ = r.Close() }()
			if got := mustReadFrom(t, r); got != copySourceBody {
				t.Errorf("copied content = %q, want %q", got, copySourceBody)
			}
		})
	}
}

// mustReadFrom reads a whole Reader and fails the test on error.
func mustReadFrom(t *testing.T, r *object.Reader) string {
	t.Helper()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("reading copy: %v", err)
	}
	return buf.String()
}

func TestCopyObjectErrors(t *testing.T) {
	sourceETag := md5Hex([]byte(copySourceBody))
	tests := []struct {
		name        string
		src         object.Ref
		dstBucket   string
		dstKey      string
		srcCond     object.Conditions
		dstCond     object.Conditions
		existingDst bool
		wantCode    s3err.Code
	}{
		{name: "missing source bucket", src: object.Ref{Bucket: "nope", Key: "src"}, dstBucket: testBucket, dstKey: "dst", wantCode: s3err.NoSuchBucket},
		{name: "missing source key", src: object.Ref{Bucket: testBucket, Key: "absent"}, dstBucket: testBucket, dstKey: "dst", wantCode: s3err.NoSuchKey},
		{name: "source version", src: object.Ref{Bucket: testBucket, Key: "src", VersionID: "v1"}, dstBucket: testBucket, dstKey: "dst", wantCode: s3err.NoSuchVersion},
		{
			name:      "source none-match matches maps to precondition failed",
			src:       object.Ref{Bucket: testBucket, Key: "src"},
			dstBucket: testBucket,
			dstKey:    "dst",
			srcCond:   object.Conditions{IfNoneMatch: sourceETag},
			wantCode:  s3err.PreconditionFailed,
		},
		{
			name:      "source match differs",
			src:       object.Ref{Bucket: testBucket, Key: "src"},
			dstBucket: testBucket,
			dstKey:    "dst",
			srcCond:   object.Conditions{IfMatch: "other"},
			wantCode:  s3err.PreconditionFailed,
		},
		{
			name:      "source match holds",
			src:       object.Ref{Bucket: testBucket, Key: "src"},
			dstBucket: testBucket,
			dstKey:    "dst",
			srcCond:   object.Conditions{IfMatch: sourceETag},
		},
		{name: "missing destination bucket", src: object.Ref{Bucket: testBucket, Key: "src"}, dstBucket: "nope", dstKey: "dst", wantCode: s3err.NoSuchBucket},
		{name: "invalid destination key", src: object.Ref{Bucket: testBucket, Key: "src"}, dstBucket: testBucket, dstKey: "", wantCode: s3err.InvalidArgument},
		{
			name:        "destination none-match star on existing key",
			src:         object.Ref{Bucket: testBucket, Key: "src"},
			dstBucket:   testBucket,
			dstKey:      "dst",
			dstCond:     object.Conditions{IfNoneMatch: "*"},
			existingDst: true,
			wantCode:    s3err.PreconditionFailed,
		},
		{
			name:      "destination match on missing key",
			src:       object.Ref{Bucket: testBucket, Key: "src"},
			dstBucket: testBucket,
			dstKey:    "dst",
			dstCond:   object.Conditions{IfMatch: sourceETag},
			wantCode:  s3err.NoSuchKey,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newCopyFixture(t)
			if tt.existingDst {
				mustPut(t, l, "dst", []byte("old"))
			}
			_, err := l.CopyObject(ctx, tt.src, tt.dstBucket, tt.dstKey, object.CopyOptions{
				SourceConditions: tt.srcCond,
				Put:              object.PutOptions{Conditions: tt.dstCond},
			})
			wantCode(t, err, tt.wantCode)
		})
	}
}

func TestCopyObjectChecksums(t *testing.T) {
	ctx := context.Background()
	data := []byte(copySourceBody)

	t.Run("REPLACE computes the requested checksum", func(t *testing.T) {
		l := newCopyFixture(t)
		info, err := l.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "src"}, testBucket, "dst", object.CopyOptions{
			Directive: object.MetadataReplace,
			Put:       object.PutOptions{ChecksumAlgorithm: object.ChecksumSHA256},
		})
		wantCode(t, err, "")
		sum := sha256.Sum256(data)
		if info.Checksum.Algorithm != object.ChecksumSHA256 || info.Checksum.Type != object.ChecksumFullObject ||
			!bytes.Equal(info.Checksum.Value, sum[:]) {
			t.Errorf("Checksum = %+v, want the SHA-256 of the content", info.Checksum)
		}
	})

	t.Run("COPY keeps the source checksum", func(t *testing.T) {
		l := NewLayer()
		if _, err := l.MakeBucket(ctx, testBucket, object.MakeBucketOptions{}); err != nil {
			t.Fatalf("MakeBucket: %v", err)
		}
		body := newTestBody(data)
		body.checksum = object.Checksum{Algorithm: object.ChecksumCRC32, Value: []byte{1, 2, 3, 4}}
		if _, err := l.PutObject(ctx, testBucket, "src", body, object.PutOptions{}); err != nil {
			t.Fatalf("PutObject: %v", err)
		}
		info, err := l.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "src"}, testBucket, "dst", object.CopyOptions{})
		wantCode(t, err, "")
		if diff := cmp.Diff(body.checksum, info.Checksum); diff != "" {
			t.Errorf("checksum mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestCopyObjectIsIndependentOfLaterWrites(t *testing.T) {
	ctx := context.Background()
	l := newCopyFixture(t)
	if _, err := l.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "src"}, testBucket, "dst", object.CopyOptions{}); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	mustPut(t, l, "src", []byte("changed"))
	if got := mustRead(t, l, "dst"); string(got) != copySourceBody {
		t.Errorf("copy content = %q after source overwrite, want %q", got, copySourceBody)
	}
}
