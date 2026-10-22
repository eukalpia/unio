package fakes

import (
	"context"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
	"github.com/google/go-cmp/cmp"
)

func TestMakeBucket(t *testing.T) {
	tests := []struct {
		name      string
		existing  string // owner of a bucket created before the call; "" for none
		bucket    string
		opts      object.MakeBucketOptions
		wantOwner string
		wantCode  s3err.Code
	}{
		{name: "default owner", bucket: "photos", wantOwner: DefaultOwner},
		{name: "explicit owner", bucket: "photos", opts: object.MakeBucketOptions{Owner: "alice"}, wantOwner: "alice"},
		{name: "invalid name", bucket: "Bad_Name", wantCode: s3err.InvalidBucketName},
		{name: "same owner again", existing: DefaultOwner, bucket: "photos", wantCode: s3err.BucketAlreadyOwnedByYou},
		{name: "other owner", existing: "bob", bucket: "photos", wantCode: s3err.BucketAlreadyExists},
		{
			name:     "versioning not implemented",
			bucket:   "photos",
			opts:     object.MakeBucketOptions{Versioning: object.VersioningEnabled},
			wantCode: s3err.NotImplemented,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := NewLayer()
			if tt.existing != "" {
				if _, err := l.MakeBucket(ctx, tt.bucket, object.MakeBucketOptions{Owner: tt.existing}); err != nil {
					t.Fatalf("setup MakeBucket: %v", err)
				}
			}
			info, err := l.MakeBucket(ctx, tt.bucket, tt.opts)
			wantCode(t, err, tt.wantCode)
			if tt.wantCode == "" && (info.Name != tt.bucket || info.Owner != tt.wantOwner) {
				t.Errorf("MakeBucket info = {Name: %q, Owner: %q}, want {Name: %q, Owner: %q}",
					info.Name, info.Owner, tt.bucket, tt.wantOwner)
			}
		})
	}
}

func TestBucketInfoAndListing(t *testing.T) {
	ctx := context.Background()
	l := NewLayer()

	_, err := l.GetBucketInfo(ctx, "missing-bucket")
	wantCode(t, err, s3err.NoSuchBucket)
	for _, name := range []string{"gamma-bucket", "alpha-bucket", "beta-bucket"} {
		if _, err := l.MakeBucket(ctx, name, object.MakeBucketOptions{Owner: "alice"}); err != nil {
			t.Fatalf("MakeBucket(%q): %v", name, err)
		}
	}

	info, err := l.GetBucketInfo(ctx, "beta-bucket")
	wantCode(t, err, "")
	if info.Name != "beta-bucket" || info.Owner != "alice" || info.ID == 0 {
		t.Errorf("GetBucketInfo = %+v, want name beta-bucket, owner alice and a non-zero ID", info)
	}

	list, err := l.ListBuckets(ctx)
	wantCode(t, err, "")
	var names []string
	for _, b := range list {
		names = append(names, b.Name)
	}
	want := []string{"alpha-bucket", "beta-bucket", "gamma-bucket"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("ListBuckets names mismatch (-want +got):\n%s", diff)
	}
}

func TestDeleteBucket(t *testing.T) {
	tests := []struct {
		name     string
		bucket   string
		object   bool // store an object before deleting
		upload   bool // start a multipart upload before deleting
		force    bool
		wantCode s3err.Code
		gone     bool // the bucket must be absent afterwards
	}{
		{name: "missing bucket", bucket: "missing-bucket", wantCode: s3err.NoSuchBucket},
		{name: "empty bucket", bucket: testBucket, gone: true},
		{name: "bucket with object", bucket: testBucket, object: true, wantCode: s3err.BucketNotEmpty},
		{name: "bucket with upload", bucket: testBucket, upload: true, wantCode: s3err.BucketNotEmpty},
		{name: "forced with object", bucket: testBucket, object: true, force: true, gone: true},
		{name: "forced with upload", bucket: testBucket, upload: true, force: true, gone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newTestLayer(t)
			if tt.object {
				mustPut(t, l, "k", []byte("v"))
			}
			if tt.upload {
				if _, err := l.NewMultipartUpload(ctx, testBucket, "k", object.PutOptions{}); err != nil {
					t.Fatalf("setup NewMultipartUpload: %v", err)
				}
			}
			err := l.DeleteBucket(ctx, tt.bucket, object.DeleteBucketOptions{Force: tt.force})
			wantCode(t, err, tt.wantCode)
			if tt.gone {
				_, err := l.GetBucketInfo(ctx, tt.bucket)
				wantCode(t, err, s3err.NoSuchBucket)
			}
		})
	}
}
