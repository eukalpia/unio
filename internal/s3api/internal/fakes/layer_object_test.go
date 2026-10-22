package fakes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestPutObjectStoresVersionInfo(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	data := []byte("hello world")
	opts := object.PutOptions{
		ContentType:        "text/plain",
		ContentEncoding:    "gzip",
		ContentDisposition: "inline",
		ContentLanguage:    "en",
		CacheControl:       "no-cache",
		Expires:            "Wed, 21 Oct 2037 07:28:00 GMT",
		UserMetadata:       map[string]string{"color": "blue"},
		Tags:               "env=test",
	}
	got, err := l.PutObject(ctx, testBucket, "dir/hello.txt", newTestBody(data), opts)
	wantCode(t, err, "")

	want := object.Info{
		Bucket:             testBucket,
		Key:                "dir/hello.txt",
		IsLatest:           true,
		Size:               int64(len(data)),
		ETag:               md5Hex(data),
		ContentType:        "text/plain",
		ContentEncoding:    "gzip",
		ContentDisposition: "inline",
		ContentLanguage:    "en",
		CacheControl:       "no-cache",
		Expires:            "Wed, 21 Oct 2037 07:28:00 GMT",
		UserMetadata:       map[string]string{"color": "blue"},
		Tags:               "env=test",
		StorageClass:       "STANDARD",
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(object.Info{}, "ModTime")); diff != "" {
		t.Errorf("PutObject info mismatch (-want +got):\n%s", diff)
	}
	if got.ModTime.Nanosecond() != 0 || got.ModTime.Location() != time.UTC {
		t.Errorf("ModTime = %v, want UTC at second precision", got.ModTime)
	}

	head, err := l.HeadObject(ctx, testBucket, "dir/hello.txt", object.GetOptions{})
	wantCode(t, err, "")
	if diff := cmp.Diff(got, head); diff != "" {
		t.Errorf("HeadObject differs from PutObject info (-put +head):\n%s", diff)
	}

	r, err := l.GetObject(ctx, testBucket, "dir/hello.txt", object.GetOptions{})
	wantCode(t, err, "")
	defer func() { _ = r.Close() }()
	content, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading object: %v", err)
	}
	if !bytes.Equal(content, data) || r.Offset != 0 || r.Length != int64(len(data)) {
		t.Errorf("GetObject content=%q offset=%d length=%d, want %q 0 %d",
			content, r.Offset, r.Length, data, len(data))
	}
	if diff := cmp.Diff(got, r.Info); diff != "" {
		t.Errorf("GetObject Info differs from PutObject info (-put +get):\n%s", diff)
	}
}

func TestPutObjectDefaultsAndBodyChecksum(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	body := newTestBody([]byte("abc"))
	body.checksum = object.Checksum{Algorithm: object.ChecksumCRC32, Value: []byte{0x35, 0x24, 0x41, 0xc2}}

	info, err := l.PutObject(ctx, testBucket, "k", body, object.PutOptions{})
	wantCode(t, err, "")
	if info.StorageClass != "STANDARD" {
		t.Errorf("StorageClass = %q, want STANDARD", info.StorageClass)
	}
	if info.Checksum.Algorithm != object.ChecksumCRC32 || !bytes.Equal(info.Checksum.Value, body.checksum.Value) {
		t.Errorf("Checksum = %+v, want the body checksum", info.Checksum)
	}
	if info.ETag != "900150983cd24fb0d6963f7d28e17f72" {
		t.Errorf("ETag = %q, want MD5 of abc", info.ETag)
	}
}

func TestPutObjectInvalidMetadataAndTags(t *testing.T) {
	tests := []struct {
		name     string
		opts     object.PutOptions
		wantCode s3err.Code
	}{
		{
			name:     "user metadata too large",
			opts:     object.PutOptions{UserMetadata: map[string]string{"k": strings.Repeat("v", object.MaxUserMetadataSize)}},
			wantCode: s3err.MetadataTooLarge,
		},
		{name: "malformed tags", opts: object.PutOptions{Tags: "a=%zz"}, wantCode: s3err.InvalidTag},
		{
			name:     "too many tags",
			opts:     object.PutOptions{Tags: "a=1&b=2&c=3&d=4&e=5&f=6&g=7&h=8&i=9&j=10&k=11"},
			wantCode: s3err.InvalidTag,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLayer(t)
			_, err := l.PutObject(context.Background(), testBucket, "k", newTestBody([]byte("x")), tt.opts)
			wantCode(t, err, tt.wantCode)
		})
	}
}

func TestPutObjectConditions(t *testing.T) {
	oldETag := md5Hex([]byte("old"))
	tests := []struct {
		name     string
		exists   bool
		cond     object.Conditions
		wantCode s3err.Code
	}{
		{name: "none-match star on missing key", cond: object.Conditions{IfNoneMatch: "*"}},
		{name: "none-match star on existing key", exists: true, cond: object.Conditions{IfNoneMatch: "*"}, wantCode: s3err.PreconditionFailed},
		{name: "match on missing key", cond: object.Conditions{IfMatch: oldETag}, wantCode: s3err.NoSuchKey},
		{name: "match on existing key", exists: true, cond: object.Conditions{IfMatch: oldETag}},
		{name: "match star on existing key", exists: true, cond: object.Conditions{IfMatch: "*"}},
		{name: "match differs", exists: true, cond: object.Conditions{IfMatch: "abc"}, wantCode: s3err.PreconditionFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newTestLayer(t)
			if tt.exists {
				mustPut(t, l, "k", []byte("old"))
			}
			_, err := l.PutObject(ctx, testBucket, "k", newTestBody([]byte("new")), object.PutOptions{Conditions: tt.cond})
			wantCode(t, err, tt.wantCode)

			wantContent := []byte("old")
			if tt.wantCode == "" {
				wantContent = []byte("new")
			}
			if tt.exists || tt.wantCode == "" {
				if got := mustRead(t, l, "k"); !bytes.Equal(got, wantContent) {
					t.Errorf("content = %q, want %q", got, wantContent)
				}
			} else {
				_, err := l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
				wantCode(t, err, s3err.NoSuchKey)
			}
		})
	}
}

func TestPutObjectOverwriteKeepsOpenReaders(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	mustPut(t, l, "k", []byte("first version"))

	old, err := l.GetObject(ctx, testBucket, "k", object.GetOptions{})
	wantCode(t, err, "")
	defer func() { _ = old.Close() }()

	mustPut(t, l, "k", []byte("second"))

	content, err := io.ReadAll(old)
	if err != nil {
		t.Fatalf("reading old reader: %v", err)
	}
	if string(content) != "first version" {
		t.Errorf("open reader saw %q, want the content it was opened on", content)
	}
	if got := mustRead(t, l, "k"); string(got) != "second" {
		t.Errorf("GetObject after overwrite = %q, want second", got)
	}
}

func TestPutObjectReadFailureDoesNotCommit(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("content-md5 mismatch")
	tests := []struct {
		name   string
		exists bool
	}{
		{name: "new key"},
		{name: "existing key", exists: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLayer(t)
			if tt.exists {
				mustPut(t, l, "k", []byte("old"))
			}
			_, err := l.PutObject(ctx, testBucket, "k", newFailingBody([]byte("new content"), boom), object.PutOptions{})
			if !errors.Is(err, boom) {
				t.Fatalf("PutObject error = %v, want the body error unchanged", err)
			}
			if tt.exists {
				if got := mustRead(t, l, "k"); string(got) != "old" {
					t.Errorf("content = %q after failed put, want old", got)
				}
				return
			}
			_, err = l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
			wantCode(t, err, s3err.NoSuchKey)
		})
	}
}

func TestPutObjectTooLargeIsRejectedBeforeReading(t *testing.T) {
	l := newTestLayer(t)
	body := &testBody{reader: bytes.NewReader(nil), size: object.MaxPutSize + 1}
	_, err := l.PutObject(context.Background(), testBucket, "big", body, object.PutOptions{})
	wantCode(t, err, s3err.EntityTooLarge)
}

func TestObjectKeyValidation(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		wantCode s3err.Code
	}{
		{name: "empty", key: "", wantCode: s3err.InvalidArgument},
		{name: "too long", key: strings.Repeat("k", object.MaxKeyLength+1), wantCode: s3err.KeyTooLongError},
		{name: "NUL byte", key: "a\x00b", wantCode: s3err.InvalidArgument},
		{name: "invalid UTF-8", key: "\xff", wantCode: s3err.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newTestLayer(t)
			_, err := l.PutObject(ctx, testBucket, tt.key, newTestBody(nil), object.PutOptions{})
			wantCode(t, err, tt.wantCode)
			_, err = l.GetObject(ctx, testBucket, tt.key, object.GetOptions{})
			wantCode(t, err, tt.wantCode)
			_, err = l.HeadObject(ctx, testBucket, tt.key, object.GetOptions{})
			wantCode(t, err, tt.wantCode)
			_, err = l.DeleteObject(ctx, testBucket, tt.key, object.DeleteOptions{})
			wantCode(t, err, tt.wantCode)
		})
	}
}

func TestGetObjectRange(t *testing.T) {
	const content = "0123456789"
	tests := []struct {
		name       string
		rng        *object.HTTPRange
		want       string
		wantOffset int64
		wantCode   s3err.Code
	}{
		{name: "no range", want: content},
		{name: "closed range", rng: &object.HTTPRange{Start: 2, End: 5}, want: "2345", wantOffset: 2},
		{name: "open-ended range", rng: &object.HTTPRange{Start: 7, End: -1}, want: "789", wantOffset: 7},
		{name: "suffix range", rng: &object.HTTPRange{Suffix: 3}, want: "789", wantOffset: 7},
		{name: "suffix longer than object", rng: &object.HTTPRange{Suffix: 100}, want: content},
		{name: "start past end", rng: &object.HTTPRange{Start: 10, End: -1}, wantCode: s3err.InvalidRange},
		{name: "end before start", rng: &object.HTTPRange{Start: 5, End: 2}, wantCode: s3err.InvalidRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newTestLayer(t)
			mustPut(t, l, "k", []byte(content))

			r, err := l.GetObject(ctx, testBucket, "k", object.GetOptions{Range: tt.rng})
			wantCode(t, err, tt.wantCode)
			if tt.wantCode != "" {
				return
			}
			defer func() { _ = r.Close() }()
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("reading range: %v", err)
			}
			if string(got) != tt.want || r.Offset != tt.wantOffset || r.Length != int64(len(tt.want)) {
				t.Errorf("range read = %q offset %d length %d, want %q offset %d length %d",
					got, r.Offset, r.Length, tt.want, tt.wantOffset, len(tt.want))
			}
		})
	}
}

func TestEmptyObjectRead(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	mustPut(t, l, "empty", nil)

	r, err := l.GetObject(ctx, testBucket, "empty", object.GetOptions{})
	wantCode(t, err, "")
	defer func() { _ = r.Close() }()
	got, err := io.ReadAll(r)
	if err != nil || len(got) != 0 || r.Length != 0 {
		t.Errorf("empty read = %q length %d err %v, want no content", got, r.Length, err)
	}

	_, err = l.GetObject(ctx, testBucket, "empty", object.GetOptions{Range: &object.HTTPRange{Start: 0, End: -1}})
	wantCode(t, err, s3err.InvalidRange)
}

func TestGetObjectConditions(t *testing.T) {
	data := []byte("cond")
	tests := []struct {
		name     string
		cond     object.Conditions
		wantCode s3err.Code
	}{
		{name: "none-match equal", cond: object.Conditions{IfNoneMatch: md5Hex(data)}, wantCode: s3err.NotModified},
		{name: "none-match star", cond: object.Conditions{IfNoneMatch: "*"}, wantCode: s3err.NotModified},
		{name: "none-match differs", cond: object.Conditions{IfNoneMatch: "other"}},
		{name: "match equal", cond: object.Conditions{IfMatch: md5Hex(data)}},
		{name: "match differs", cond: object.Conditions{IfMatch: "other"}, wantCode: s3err.PreconditionFailed},
		{
			name:     "modified-since in the future",
			cond:     object.Conditions{IfModifiedSince: time.Now().Add(time.Hour)},
			wantCode: s3err.NotModified,
		},
		{
			name:     "unmodified-since in the past",
			cond:     object.Conditions{IfUnmodifiedSince: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
			wantCode: s3err.PreconditionFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newTestLayer(t)
			mustPut(t, l, "k", data)

			r, err := l.GetObject(ctx, testBucket, "k", object.GetOptions{Conditions: tt.cond})
			wantCode(t, err, tt.wantCode)
			if err == nil {
				_ = r.Close()
			}
			_, err = l.HeadObject(ctx, testBucket, "k", object.GetOptions{Conditions: tt.cond})
			wantCode(t, err, tt.wantCode)
		})
	}
}

func TestGetAndHeadVersionAndPartOptions(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	mustPut(t, l, "k", []byte("v"))

	_, err := l.GetObject(ctx, testBucket, "k", object.GetOptions{VersionID: "v1"})
	wantCode(t, err, s3err.NoSuchVersion)
	_, err = l.HeadObject(ctx, testBucket, "k", object.GetOptions{VersionID: "v1"})
	wantCode(t, err, s3err.NoSuchVersion)
	_, err = l.GetObject(ctx, testBucket, "k", object.GetOptions{PartNumber: 1})
	wantCode(t, err, s3err.NotImplemented)
}

func TestReturnedInfoDoesNotAliasStoredState(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	opts := object.PutOptions{UserMetadata: map[string]string{"color": "blue"}}
	info, err := l.PutObject(ctx, testBucket, "k", newTestBody([]byte("x")), opts)
	wantCode(t, err, "")

	info.UserMetadata["color"] = "red"
	opts.UserMetadata["color"] = "green"

	head, err := l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
	wantCode(t, err, "")
	if head.UserMetadata["color"] != "blue" {
		t.Errorf("stored metadata = %q, want blue", head.UserMetadata["color"])
	}
	head.UserMetadata["color"] = "yellow"
	again, err := l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
	wantCode(t, err, "")
	if again.UserMetadata["color"] != "blue" {
		t.Errorf("stored metadata changed through HeadObject result: %q", again.UserMetadata["color"])
	}
}

func TestDeleteObject(t *testing.T) {
	tests := []struct {
		name     string
		exists   bool
		opts     object.DeleteOptions
		wantCode s3err.Code
		survives bool
	}{
		{name: "missing key succeeds", opts: object.DeleteOptions{}},
		{name: "existing key removed", exists: true},
		{name: "if-match on missing key", opts: object.DeleteOptions{Conditions: object.Conditions{IfMatch: "*"}}, wantCode: s3err.NoSuchKey},
		{
			name:     "if-match differs keeps object",
			exists:   true,
			opts:     object.DeleteOptions{Conditions: object.Conditions{IfMatch: "other"}},
			wantCode: s3err.PreconditionFailed,
			survives: true,
		},
		{name: "version id not supported", exists: true, opts: object.DeleteOptions{VersionID: "v1"}, wantCode: s3err.NoSuchVersion, survives: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newTestLayer(t)
			if tt.exists {
				mustPut(t, l, "k", []byte("v"))
			}
			res, err := l.DeleteObject(ctx, testBucket, "k", tt.opts)
			wantCode(t, err, tt.wantCode)
			if tt.wantCode == "" && res.Key != "k" {
				t.Errorf("DeleteResult.Key = %q, want k", res.Key)
			}
			_, err = l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
			if tt.survives {
				wantCode(t, err, "")
			} else {
				wantCode(t, err, s3err.NoSuchKey)
			}
		})
	}
}

func TestDeleteObjectsReportsPerKeyResults(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	mustPut(t, l, "a", []byte("a"))
	mustPut(t, l, "c", []byte("c"))

	in := []object.ToDelete{
		{Key: "c"},
		{Key: "missing"},
		{Key: ""},
		{Key: "a", VersionID: "v1"},
		{Key: "a"},
	}
	results, err := l.DeleteObjects(ctx, testBucket, in)
	wantCode(t, err, "")
	if len(results) != len(in) {
		t.Fatalf("got %d results, want %d", len(results), len(in))
	}
	wantCodes := []s3err.Code{"", "", s3err.InvalidArgument, s3err.NoSuchVersion, ""}
	for i, res := range results {
		if res.Key != in[i].Key {
			t.Errorf("result %d key = %q, want %q (input order must be kept)", i, res.Key, in[i].Key)
		}
		wantCode(t, res.Err, wantCodes[i])
	}
	for _, key := range []string{"a", "c"} {
		_, err = l.HeadObject(ctx, testBucket, key, object.GetOptions{})
		wantCode(t, err, s3err.NoSuchKey)
	}
}

func TestDeleteObjectsLimit(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	mustPut(t, l, "a", []byte("a"))

	many := make([]object.ToDelete, object.MaxDeleteObjects+1)
	for i := range many {
		many[i] = object.ToDelete{Key: "a"}
	}
	results, err := l.DeleteObjects(ctx, testBucket, many)
	wantCode(t, err, s3err.InvalidArgument)
	if results != nil {
		t.Errorf("results = %d entries, want none on a request error", len(results))
	}
	if _, err := l.HeadObject(ctx, testBucket, "a", object.GetOptions{}); err != nil {
		t.Errorf("rejected request still deleted the object: %v", err)
	}

	at := many[:object.MaxDeleteObjects]
	results, err = l.DeleteObjects(ctx, testBucket, at)
	wantCode(t, err, "")
	if len(results) != object.MaxDeleteObjects {
		t.Errorf("got %d results at the limit, want %d", len(results), object.MaxDeleteObjects)
	}
}
