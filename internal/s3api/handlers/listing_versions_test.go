package handlers

import (
	"context"
	"encoding/xml"
	"net/url"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
	"github.com/eukalpia/unio/internal/s3api/router"
	"github.com/eukalpia/unio/internal/s3err"
)

// versionedLayer reports every bucket as versioned, which the fake layer
// cannot create.
type versionedLayer struct {
	*fakes.Layer
}

func (l versionedLayer) GetBucketInfo(ctx context.Context, bucket string) (object.BucketInfo, error) {
	info, err := l.Layer.GetBucketInfo(ctx, bucket)
	info.Versioning = object.VersioningEnabled
	return info, err
}

// listVersionsReply is the part of a ListVersionsResult document the tests read.
type listVersionsReply struct {
	IsTruncated         bool   `xml:"IsTruncated"`
	NextKeyMarker       string `xml:"NextKeyMarker"`
	NextVersionIDMarker string `xml:"NextVersionIdMarker"`
	Versions            []struct {
		Key       string `xml:"Key"`
		VersionID string `xml:"VersionId"`
		IsLatest  bool   `xml:"IsLatest"`
		Size      int64  `xml:"Size"`
	} `xml:"Version"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

// listVersionsCall runs ListObjectVersions on bucket with the raw query q.
func listVersionsCall(t *testing.T, layer object.Layer, bucket, q string) (listVersionsReply, error) {
	t.Helper()
	rc := app.NewContext(0)
	query, err := url.ParseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	c := &Call{
		Ctx:   context.Background(),
		RC:    rc,
		Route: router.Route{Bucket: bucket, Query: query},
	}
	env := &Env{Layer: layer, Region: "us-east-1"}
	if err := env.ListObjectVersions(c); err != nil {
		return listVersionsReply{}, err
	}
	var out listVersionsReply
	if err := xml.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("bad response: %v\n%s", err, rc.Response.Body())
	}
	return out, nil
}

// TestListObjectVersionsOfUnversionedBucket checks that every current object
// of a bucket that has never been versioned is listed as its null version, and
// that markers, delimiters and truncation follow the ListObjects rules.
func TestListObjectVersionsOfUnversionedBucket(t *testing.T) {
	layer := fakes.NewLayer()
	ctx := context.Background()
	if _, err := layer.MakeBucket(ctx, "bucket-x", object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "b/c", "d"} {
		if _, err := layer.PutObject(ctx, "bucket-x", k, objecttest.NewBody([]byte(k)), object.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := listVersionsCall(t, layer, "bucket-x", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Versions) != 3 || all.IsTruncated {
		t.Fatalf("listing = %+v, want three versions", all)
	}
	for _, v := range all.Versions {
		if v.VersionID != "null" || !v.IsLatest {
			t.Fatalf("version %s = id %q latest %v, want null and latest", v.Key, v.VersionID, v.IsLatest)
		}
	}

	grouped, err := listVersionsCall(t, layer, "bucket-x", "delimiter=%2F")
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped.Versions) != 2 || len(grouped.CommonPrefixes) != 1 || grouped.CommonPrefixes[0].Prefix != "b/" {
		t.Fatalf("delimiter listing = %+v, want keys a and d and prefix b/", grouped)
	}

	page, err := listVersionsCall(t, layer, "bucket-x", "max-keys=1")
	if err != nil {
		t.Fatal(err)
	}
	if !page.IsTruncated || page.NextKeyMarker != "a" || page.NextVersionIDMarker != "null" {
		t.Fatalf("first page = %+v, want truncated after a with the null marker", page)
	}
	next, err := listVersionsCall(t, layer, "bucket-x", "key-marker=a&version-id-marker=null")
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Versions) != 2 || next.Versions[0].Key != "b/c" {
		t.Fatalf("page after the null marker = %+v, want b/c and d", next)
	}
}

// TestListObjectVersionsRejectsBadRequests checks the argument errors of the
// version markers and the answers for buckets that are not in scope.
func TestListObjectVersionsRejectsBadRequests(t *testing.T) {
	layer := fakes.NewLayer()
	ctx := context.Background()
	if _, err := layer.MakeBucket(ctx, "bucket-x", object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		layer  object.Layer
		bucket string
		query  string
		want   s3err.Code
	}{
		{name: "version marker without key marker", layer: layer, bucket: "bucket-x", query: "version-id-marker=null", want: s3err.InvalidArgument},
		{name: "unknown version marker", layer: layer, bucket: "bucket-x", query: "key-marker=a&version-id-marker=v1", want: s3err.InvalidArgument},
		{name: "bad encoding type", layer: layer, bucket: "bucket-x", query: "encoding-type=base64", want: s3err.InvalidArgument},
		{name: "missing bucket", layer: layer, bucket: "nope", query: "", want: s3err.NoSuchBucket},
		{name: "versioned bucket", layer: versionedLayer{layer}, bucket: "bucket-x", query: "", want: s3err.NotImplemented},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := listVersionsCall(t, tt.layer, tt.bucket, tt.query)
			if got := s3err.CodeOf(err); got != tt.want {
				t.Fatalf("code = %q (err %v), want %q", got, err, tt.want)
			}
		})
	}
}
