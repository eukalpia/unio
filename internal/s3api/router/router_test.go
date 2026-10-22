package router

import (
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

func mustRouter(t *testing.T, domains ...string) *Router {
	t.Helper()
	r, err := New(domains)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMatch(t *testing.T) {
	const domain = "s3.unio.local"
	type want struct {
		op     Op
		bucket string
		key    string
		err    s3err.Code
	}
	cases := []struct {
		name string
		req  Request
		want want
	}{
		// Service level.
		{"list buckets", Request{Method: "GET", Path: "/"}, want{op: OpListBuckets}},
		{"service put", Request{Method: "PUT", Path: "/"}, want{err: s3err.MethodNotAllowed}},
		{"service head", Request{Method: "HEAD", Path: "/"}, want{err: s3err.MethodNotAllowed}},

		// Path style, bucket level.
		{"create bucket", Request{Method: "PUT", Path: "/photos"}, want{op: OpCreateBucket, bucket: "photos"}},
		{"create bucket trailing slash", Request{Method: "PUT", Path: "/photos/"}, want{op: OpCreateBucket, bucket: "photos"}},
		{"head bucket", Request{Method: "HEAD", Path: "/photos"}, want{op: OpHeadBucket, bucket: "photos"}},
		{"delete bucket", Request{Method: "DELETE", Path: "/photos"}, want{op: OpDeleteBucket, bucket: "photos"}},
		{"get location", Request{Method: "GET", Path: "/photos", Query: "location="}, want{op: OpGetBucketLocation, bucket: "photos"}},
		{"list v1", Request{Method: "GET", Path: "/photos"}, want{op: OpListObjectsV1, bucket: "photos"}},
		{"list v1 with prefix", Request{Method: "GET", Path: "/photos", Query: "prefix=a%2Fb&delimiter=%2F"}, want{op: OpListObjectsV1, bucket: "photos"}},
		{"list v2", Request{Method: "GET", Path: "/photos", Query: "list-type=2"}, want{op: OpListObjectsV2, bucket: "photos"}},
		{"list v2 bad type", Request{Method: "GET", Path: "/photos", Query: "list-type=1"}, want{err: s3err.InvalidArgument}},
		{"list uploads", Request{Method: "GET", Path: "/photos", Query: "uploads="}, want{op: OpListMultipartUploads, bucket: "photos"}},
		{"delete objects", Request{Method: "POST", Path: "/photos", Query: "delete="}, want{op: OpDeleteObjects, bucket: "photos"}},
		{"post bucket without delete", Request{Method: "POST", Path: "/photos"}, want{err: s3err.NotImplemented}},
		{"bucket put with upload id", Request{Method: "PUT", Path: "/photos", Query: "uploadId=x"}, want{err: s3err.InvalidRequest}},
		{"bucket patch", Request{Method: "PATCH", Path: "/photos"}, want{err: s3err.MethodNotAllowed}},

		// Path style, object level.
		{"put object", Request{Method: "PUT", Path: "/photos/a/b.txt"}, want{op: OpPutObject, bucket: "photos", key: "a/b.txt"}},
		{"put object encoded key", Request{Method: "PUT", Path: "/photos/a%20b%2Bc.txt"}, want{op: OpPutObject, bucket: "photos", key: "a b+c.txt"}},
		{"put object plus stays plus", Request{Method: "PUT", Path: "/photos/a+b"}, want{op: OpPutObject, bucket: "photos", key: "a+b"}},
		{"put object unicode", Request{Method: "PUT", Path: "/photos/%D1%84.txt"}, want{op: OpPutObject, bucket: "photos", key: "ф.txt"}},
		{"copy object", Request{Method: "PUT", Path: "/photos/dst", CopySource: true}, want{op: OpCopyObject, bucket: "photos", key: "dst"}},
		{"get object", Request{Method: "GET", Path: "/photos/k"}, want{op: OpGetObject, bucket: "photos", key: "k"}},
		{"head object", Request{Method: "HEAD", Path: "/photos/k"}, want{op: OpHeadObject, bucket: "photos", key: "k"}},
		{"delete object", Request{Method: "DELETE", Path: "/photos/k"}, want{op: OpDeleteObject, bucket: "photos", key: "k"}},
		{"create multipart", Request{Method: "POST", Path: "/photos/k", Query: "uploads="}, want{op: OpCreateMultipartUpload, bucket: "photos", key: "k"}},
		{"complete multipart", Request{Method: "POST", Path: "/photos/k", Query: "uploadId=u1"}, want{op: OpCompleteMultipartUpload, bucket: "photos", key: "k"}},
		{"upload part", Request{Method: "PUT", Path: "/photos/k", Query: "partNumber=2&uploadId=u1"}, want{op: OpUploadPart, bucket: "photos", key: "k"}},
		{"upload part copy", Request{Method: "PUT", Path: "/photos/k", Query: "partNumber=2&uploadId=u1", CopySource: true}, want{op: OpUploadPartCopy, bucket: "photos", key: "k"}},
		{"upload part without upload id", Request{Method: "PUT", Path: "/photos/k", Query: "partNumber=2"}, want{err: s3err.InvalidArgument}},
		{"upload id without part number", Request{Method: "PUT", Path: "/photos/k", Query: "uploadId=u1"}, want{err: s3err.InvalidArgument}},
		{"list parts", Request{Method: "GET", Path: "/photos/k", Query: "uploadId=u1&max-parts=10"}, want{op: OpListParts, bucket: "photos", key: "k"}},
		{"abort multipart", Request{Method: "DELETE", Path: "/photos/k", Query: "uploadId=u1"}, want{op: OpAbortMultipartUpload, bucket: "photos", key: "k"}},
		{"post object form", Request{Method: "POST", Path: "/photos/k"}, want{err: s3err.NotImplemented}},
		{"object options", Request{Method: "OPTIONS", Path: "/photos/k"}, want{err: s3err.MethodNotAllowed}},
		{"trailing slash key is bucket level", Request{Method: "GET", Path: "/photos/"}, want{op: OpListObjectsV1, bucket: "photos"}},
		{"double slash keeps leading slash", Request{Method: "PUT", Path: "/photos//k"}, want{op: OpPutObject, bucket: "photos", key: "/k"}},
		{"encoded bucket", Request{Method: "HEAD", Path: "/ph%6Ftos"}, want{op: OpHeadBucket, bucket: "photos"}},
		{"empty bucket segment", Request{Method: "GET", Path: "//0"}, want{err: s3err.InvalidRequest}},

		// Unsupported subresources.
		{"get acl", Request{Method: "GET", Path: "/photos/k", Query: "acl="}, want{err: s3err.NotImplemented}},
		{"get versioning", Request{Method: "GET", Path: "/photos", Query: "versioning="}, want{err: s3err.NotImplemented}},
		{"put versioning", Request{Method: "PUT", Path: "/photos", Query: "versioning="}, want{err: s3err.NotImplemented}},
		{"put tagging", Request{Method: "PUT", Path: "/photos/k", Query: "tagging="}, want{op: OpPutObjectTagging, bucket: "photos", key: "k"}},
		{"get object tagging", Request{Method: "GET", Path: "/photos/k", Query: "tagging=&versionId=v1"}, want{op: OpGetObjectTagging, bucket: "photos", key: "k"}},
		{"delete object tagging", Request{Method: "DELETE", Path: "/photos/k", Query: "tagging="}, want{op: OpDeleteObjectTagging, bucket: "photos", key: "k"}},
		{"post object tagging", Request{Method: "POST", Path: "/photos/k", Query: "tagging="}, want{err: s3err.MethodNotAllowed}},
		{"head object tagging", Request{Method: "HEAD", Path: "/photos/k", Query: "tagging="}, want{err: s3err.MethodNotAllowed}},
		{"tagging with acl", Request{Method: "GET", Path: "/photos/k", Query: "tagging=&acl="}, want{err: s3err.NotImplemented}},
		{"tagging with upload id", Request{Method: "PUT", Path: "/photos/k", Query: "tagging=&uploadId=u&partNumber=1"}, want{err: s3err.InvalidRequest}},
		{"get bucket tagging", Request{Method: "GET", Path: "/photos", Query: "tagging="}, want{err: s3err.NotImplemented}},
		{"put bucket tagging", Request{Method: "PUT", Path: "/photos", Query: "tagging="}, want{err: s3err.NotImplemented}},
		{"delete policy", Request{Method: "DELETE", Path: "/photos", Query: "policy="}, want{err: s3err.NotImplemented}},
		{"list versions", Request{Method: "GET", Path: "/photos", Query: "versions="}, want{err: s3err.NotImplemented}},
		{"select", Request{Method: "POST", Path: "/photos/k", Query: "select=&select-type=2"}, want{err: s3err.NotImplemented}},

		// Reserved service namespace: never a bucket lookup.
		{"health live", Request{Method: "GET", Path: "/unio/health/live"}, want{op: OpHealthLive}},
		{"health live head", Request{Method: "HEAD", Path: "/unio/health/live"}, want{op: OpHealthLive}},
		{"health ready", Request{Method: "GET", Path: "/unio/health/ready"}, want{op: OpHealthReady}},
		{"metrics", Request{Method: "GET", Path: "/unio/metrics"}, want{op: OpMetrics}},
		{"health wrong method", Request{Method: "PUT", Path: "/unio/health/live"}, want{err: s3err.MethodNotAllowed}},
		{"metrics wrong method", Request{Method: "DELETE", Path: "/unio/metrics"}, want{err: s3err.MethodNotAllowed}},
		{"reserved admin prefix", Request{Method: "GET", Path: "/unio/admin/v1/users"}, want{err: s3err.NoSuchKey}},
		{"reserved unknown", Request{Method: "GET", Path: "/unio/anything"}, want{err: s3err.NoSuchKey}},
		{"reserved bare prefix", Request{Method: "GET", Path: "/unio"}, want{err: s3err.NoSuchKey}},
		{"reserved trailing slash", Request{Method: "GET", Path: "/unio/"}, want{err: s3err.NoSuchKey}},
		{"reserved bucket put", Request{Method: "PUT", Path: "/unio/photos"}, want{err: s3err.NoSuchKey}},
		{"reserved bucket name create", Request{Method: "PUT", Path: "/unio"}, want{op: OpCreateBucket, bucket: "unio"}},
		{"reserved bucket name create trailing slash", Request{Method: "PUT", Path: "/unio/"}, want{op: OpCreateBucket, bucket: "unio"}},
		{"virtual host object under unio prefix", Request{Method: "GET", Host: "photos.s3.unio.local", Path: "/unio/health/live"}, want{op: OpGetObject, bucket: "photos", key: "unio/health/live"}},
		{"virtual host bucket unio is an object key", Request{Method: "GET", Host: "unio.s3.unio.local", Path: "/unio/metrics"}, want{op: OpGetObject, bucket: "unio", key: "unio/metrics"}},

		// Malformed input.
		{"relative path", Request{Method: "GET", Path: "photos"}, want{err: s3err.InvalidRequest}},
		{"bad percent in key", Request{Method: "GET", Path: "/photos/%zz"}, want{err: s3err.InvalidArgument}},
		{"bad percent in bucket", Request{Method: "GET", Path: "/%zz"}, want{err: s3err.InvalidArgument}},
		{"bad query", Request{Method: "GET", Path: "/photos", Query: "a=%zz"}, want{err: s3err.InvalidRequest}},
		{"semicolon in query", Request{Method: "GET", Path: "/photos", Query: "a=1;b=2"}, want{err: s3err.InvalidRequest}},

		// Virtual-hosted style.
		{"vhost list", Request{Method: "GET", Host: "photos.s3.unio.local", Path: "/"}, want{op: OpListObjectsV1, bucket: "photos"}},
		{"vhost host with port", Request{Method: "GET", Host: "photos.s3.unio.local:9000", Path: "/"}, want{op: OpListObjectsV1, bucket: "photos"}},
		{"vhost uppercase host", Request{Method: "HEAD", Host: "Photos.S3.Unio.Local", Path: "/"}, want{op: OpHeadBucket, bucket: "photos"}},
		{"vhost put object", Request{Method: "PUT", Host: "photos.s3.unio.local", Path: "/a/b"}, want{op: OpPutObject, bucket: "photos", key: "a/b"}},
		{"vhost dotted bucket", Request{Method: "GET", Host: "my.photos.s3.unio.local", Path: "/k"}, want{op: OpGetObject, bucket: "my.photos", key: "k"}},
		{"vhost key looks like bucket", Request{Method: "GET", Host: "photos.s3.unio.local", Path: "/other/k"}, want{op: OpGetObject, bucket: "photos", key: "other/k"}},
		{"path style on domain host", Request{Method: "GET", Host: "s3.unio.local", Path: "/photos/k"}, want{op: OpGetObject, bucket: "photos", key: "k"}},
		{"unknown host is path style", Request{Method: "GET", Host: "127.0.0.1:9000", Path: "/photos/k"}, want{op: OpGetObject, bucket: "photos", key: "k"}},
		{"host suffix without dot", Request{Method: "GET", Host: "evils3.unio.local", Path: "/photos/k"}, want{op: OpGetObject, bucket: "photos", key: "k"}},
		{"ipv6 host", Request{Method: "GET", Host: "[::1]:9000", Path: "/photos/k"}, want{op: OpGetObject, bucket: "photos", key: "k"}},
	}
	r := mustRouter(t, domain)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Match(tc.req)
			if tc.want.err != "" {
				if !s3err.Is(err, tc.want.err) {
					t.Fatalf("err = %v, want code %s", err, tc.want.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Op != tc.want.op || got.Bucket != tc.want.bucket || got.Key != tc.want.key {
				t.Fatalf("got {%v %q %q}, want {%v %q %q}", got.Op, got.Bucket, got.Key, tc.want.op, tc.want.bucket, tc.want.key)
			}
		})
	}
}

func TestMatchQueryIsParsed(t *testing.T) {
	r := mustRouter(t)
	got, err := r.Match(Request{Method: "GET", Path: "/photos", Query: "list-type=2&prefix=a%20b&continuation-token=x%3D%3D"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Query.Get("prefix") != "a b" || got.Query.Get("continuation-token") != "x==" {
		t.Fatalf("query = %v", got.Query)
	}
}

func TestNewRejectsBadDomains(t *testing.T) {
	for _, d := range [][]string{{""}, {"."}, {".s3.unio.local"}, {"s3.unio.local."}, {"s3.unio.local:9000"}, {"s3 .unio"}} {
		if _, err := New(d); err == nil {
			t.Fatalf("New(%q) accepted an invalid domain", d)
		}
	}
	if _, err := New([]string{"S3.Unio.Local"}); err != nil {
		t.Fatalf("mixed-case domain must be accepted: %v", err)
	}
}

func TestMatchPrefersLongestDomain(t *testing.T) {
	r := mustRouter(t, "unio.local", "s3.unio.local")
	got, err := r.Match(Request{Method: "GET", Host: "b.s3.unio.local", Path: "/k"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bucket != "b" || got.Key != "k" {
		t.Fatalf("got %q %q, want bucket b key k", got.Bucket, got.Key)
	}
}

func TestOpString(t *testing.T) {
	if OpPutObject.String() != "PutObject" || Op(0).String() != "Unknown" {
		t.Fatal("unexpected op names")
	}
}
