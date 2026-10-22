package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// conditionalFixture is an object plus the validators a conditional request
// needs: its ETag and its Last-Modified time.
type conditionalFixture struct {
	bucket       string
	key          string
	body         []byte
	etag         string
	lastModified time.Time
}

// newConditionalFixture stores one object and reads back its validators.
func newConditionalFixture(ctx context.Context, t *testing.T, cli *s3.Client) conditionalFixture {
	t.Helper()
	f := conditionalFixture{
		bucket: newBucket(ctx, t, cli),
		key:    "conditional",
		body:   []byte("conditional body"),
	}
	f.etag = putObject(ctx, t, cli, f.bucket, f.key, f.body)
	head := headObject(ctx, t, cli, f.bucket, f.key)
	f.lastModified = aws.ToTime(head.LastModified)
	if f.lastModified.IsZero() {
		t.Fatal("HeadObject returned no Last-Modified time")
	}
	return f
}

// absentETag is an ETag that no stored object has.
const absentETag = `"0123456789abcdef0123456789abcdef"`

func TestObjectConditionalGet(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	f := newConditionalFixture(ctx, t, cli)

	// Both dates are in the past. RFC 7232 says a date later than the server's
	// clock must be ignored, so future dates would not test anything.
	sameSecond := f.lastModified
	earlier := f.lastModified.Add(-time.Hour)

	tests := []struct {
		name       string
		configure  func(*s3.GetObjectInput)
		wantStatus int    // 200, 304 or 412
		wantCode   string // checked only for 412
	}{
		{
			name:       "If-Match matches",
			configure:  func(in *s3.GetObjectInput) { in.IfMatch = aws.String(f.etag) },
			wantStatus: http.StatusOK,
		},
		{
			name:       "If-Match differs",
			configure:  func(in *s3.GetObjectInput) { in.IfMatch = aws.String(absentETag) },
			wantStatus: http.StatusPreconditionFailed,
			wantCode:   "PreconditionFailed",
		},
		{
			name:       "If-None-Match matches",
			configure:  func(in *s3.GetObjectInput) { in.IfNoneMatch = aws.String(f.etag) },
			wantStatus: http.StatusNotModified,
		},
		{
			name:       "If-None-Match differs",
			configure:  func(in *s3.GetObjectInput) { in.IfNoneMatch = aws.String(absentETag) },
			wantStatus: http.StatusOK,
		},
		{
			name:       "If-Modified-Since not after last change",
			configure:  func(in *s3.GetObjectInput) { in.IfModifiedSince = aws.Time(sameSecond) },
			wantStatus: http.StatusNotModified,
		},
		{
			name:       "If-Modified-Since before last change",
			configure:  func(in *s3.GetObjectInput) { in.IfModifiedSince = aws.Time(earlier) },
			wantStatus: http.StatusOK,
		},
		{
			name:       "If-Unmodified-Since before last change",
			configure:  func(in *s3.GetObjectInput) { in.IfUnmodifiedSince = aws.Time(earlier) },
			wantStatus: http.StatusPreconditionFailed,
			wantCode:   "PreconditionFailed",
		},
		{
			name:       "If-Unmodified-Since at last change",
			configure:  func(in *s3.GetObjectInput) { in.IfUnmodifiedSince = aws.Time(sameSecond) },
			wantStatus: http.StatusOK,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := &s3.GetObjectInput{Bucket: aws.String(f.bucket), Key: aws.String(f.key)}
			tc.configure(in)
			out, err := cli.GetObject(ctx, in)
			if tc.wantStatus == http.StatusOK {
				if err != nil {
					t.Fatalf("GetObject: %v", err)
				}
				if got := string(bodyOf(t, out)); got != string(f.body) {
					t.Errorf("body = %q, want %q", got, f.body)
				}
				return
			}
			requireAPIError(t, err, tc.wantStatus, tc.wantCode)
		})
	}
}

func TestObjectConditionalHead(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	f := newConditionalFixture(ctx, t, cli)

	tests := []struct {
		name       string
		configure  func(*s3.HeadObjectInput)
		wantStatus int
	}{
		{
			name:       "If-None-Match matches",
			configure:  func(in *s3.HeadObjectInput) { in.IfNoneMatch = aws.String(f.etag) },
			wantStatus: http.StatusNotModified,
		},
		{
			name:       "If-Match differs",
			configure:  func(in *s3.HeadObjectInput) { in.IfMatch = aws.String(absentETag) },
			wantStatus: http.StatusPreconditionFailed,
		},
		{
			name:       "If-Match matches",
			configure:  func(in *s3.HeadObjectInput) { in.IfMatch = aws.String(f.etag) },
			wantStatus: http.StatusOK,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := &s3.HeadObjectInput{Bucket: aws.String(f.bucket), Key: aws.String(f.key)}
			tc.configure(in)
			_, err := cli.HeadObject(ctx, in)
			if tc.wantStatus == http.StatusOK {
				if err != nil {
					t.Fatalf("HeadObject: %v", err)
				}
				return
			}
			// HEAD responses have no body, so only the status is checked.
			requireAPIError(t, err, tc.wantStatus, "")
		})
	}
}
