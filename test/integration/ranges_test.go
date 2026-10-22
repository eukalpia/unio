package integration

import (
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/go-cmp/cmp"
)

// rangeObjectSize is the length of the object used by the range tests.
const rangeObjectSize = 1000

func TestObjectRangeGet(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "ranged"
	data := make([]byte, rangeObjectSize)
	for i := range data {
		data[i] = byte(i % 251)
	}
	putObject(ctx, t, cli, bucket, key, data)

	tests := []struct {
		name             string
		rangeHeader      string
		first, last      int // inclusive byte offsets the response must carry
		wantContentRange string
	}{
		{name: "first byte", rangeHeader: "bytes=0-0", first: 0, last: 0, wantContentRange: "bytes 0-0/1000"},
		{name: "last byte", rangeHeader: "bytes=999-999", first: 999, last: 999, wantContentRange: "bytes 999-999/1000"},
		{name: "middle", rangeHeader: "bytes=10-19", first: 10, last: 19, wantContentRange: "bytes 10-19/1000"},
		{name: "open ended", rangeHeader: "bytes=990-", first: 990, last: 999, wantContentRange: "bytes 990-999/1000"},
		{name: "suffix", rangeHeader: "bytes=-100", first: 900, last: 999, wantContentRange: "bytes 900-999/1000"},
		{name: "suffix longer than object", rangeHeader: "bytes=-5000", first: 0, last: 999, wantContentRange: "bytes 0-999/1000"},
		{name: "end past object", rangeHeader: "bytes=900-5000", first: 900, last: 999, wantContentRange: "bytes 900-999/1000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := cli.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(key),
				Range:  aws.String(tc.rangeHeader),
			})
			if err != nil {
				t.Fatalf("GetObject Range %s: %v", tc.rangeHeader, err)
			}
			want := data[tc.first : tc.last+1]
			if diff := cmp.Diff(want, bodyOf(t, out)); diff != "" {
				t.Errorf("range body mismatch (-want +got):\n%s", diff)
			}
			if got := aws.ToString(out.ContentRange); got != tc.wantContentRange {
				t.Errorf("Content-Range = %q, want %q", got, tc.wantContentRange)
			}
			if got := aws.ToInt64(out.ContentLength); got != int64(len(want)) {
				t.Errorf("Content-Length = %d, want %d", got, len(want))
			}
		})
	}
}

func TestObjectRangeNotSatisfiable(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "ranged"
	putObject(ctx, t, cli, bucket, key, make([]byte, rangeObjectSize))

	tests := []struct {
		name        string
		rangeHeader string
	}{
		{name: "start equals size", rangeHeader: "bytes=1000-"},
		{name: "start past size", rangeHeader: "bytes=2000-3000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := cli.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(key),
				Range:  aws.String(tc.rangeHeader),
			})
			requireAPIError(t, err, http.StatusRequestedRangeNotSatisfiable, "InvalidRange")
		})
	}
}
