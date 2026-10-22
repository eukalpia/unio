package objecttest

import (
	"bytes"
	"slices"
	"sort"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func suiteCopy(t *testing.T, newLayer func(*testing.T) object.Layer) {
	t.Run("copy keeps metadata", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		src := payload(inlineSize+1, 9)
		orig, err := l.PutObject(ctx, testBucket, "src", bodyOf(src), object.PutOptions{
			ContentType:  "application/x-test",
			UserMetadata: map[string]string{"a": "1"},
			Tags:         "t=1",
		})
		if err != nil {
			t.Fatal(err)
		}
		info, err := l.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "src"}, testBucket, "dst", object.CopyOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if info.Key != "dst" || info.ETag != orig.ETag || info.Size != int64(len(src)) {
			t.Fatalf("copy info = %+v", info)
		}
		if info.ContentType != "application/x-test" || info.UserMetadata["a"] != "1" || info.Tags != "t=1" {
			t.Fatalf("copy did not keep metadata: %+v", info)
		}
		if got := mustRead(t, l, "dst", object.GetOptions{}); !bytes.Equal(got, src) {
			t.Fatal("copied bytes differ")
		}
	})

	t.Run("copy replaces metadata", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		mustPut(t, l, "src", []byte("content"))
		info, err := l.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "src"}, testBucket, "dst", object.CopyOptions{
			Directive: object.MetadataReplace,
			Put:       object.PutOptions{ContentType: "text/plain", UserMetadata: map[string]string{"b": "2"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if info.ContentType != "text/plain" || info.UserMetadata["b"] != "2" || info.UserMetadata["a"] != "" {
			t.Fatalf("replaced metadata = %+v", info)
		}
	})

	t.Run("self copy", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		mustPut(t, l, "k", []byte("same"))
		_, err := l.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "k"}, testBucket, "k", object.CopyOptions{})
		expectCode(t, err, s3err.InvalidRequest)
		_, err = l.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "k"}, testBucket, "k", object.CopyOptions{
			Directive: object.MetadataReplace,
			Put:       object.PutOptions{ContentType: "text/plain"},
		})
		expectCode(t, err, "")
		head, err := l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
		if err != nil || head.ContentType != "text/plain" {
			t.Fatalf("self copy with new metadata: %+v, %v", head, err)
		}
	})

	for _, size := range []int{0, inlineSize + 1, 5*mib + 3} {
		t.Run("copy "+sizeName(size), func(t *testing.T) {
			l := newLayer(t)
			mustBucket(t, l)
			data := payload(size, seedOf(size)+7)
			mustPut(t, l, "src", data)
			if _, err := l.CopyObject(t.Context(), object.Ref{Bucket: testBucket, Key: "src"}, testBucket, "dst", object.CopyOptions{}); err != nil {
				t.Fatal(err)
			}
			if got := mustRead(t, l, "dst", object.GetOptions{}); !bytes.Equal(got, data) {
				t.Fatal("copy differs from source")
			}
		})
	}

	t.Run("copy checksum algorithm", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		mustPut(t, l, "src", []byte("checksum me"))
		info, err := l.CopyObject(t.Context(), object.Ref{Bucket: testBucket, Key: "src"}, testBucket, "dst", object.CopyOptions{
			Directive: object.MetadataReplace,
			Put:       object.PutOptions{ChecksumAlgorithm: object.ChecksumSHA256},
		})
		if err != nil {
			t.Fatal(err)
		}
		if info.Checksum.Algorithm != object.ChecksumSHA256 || info.Checksum.Type != object.ChecksumFullObject || len(info.Checksum.Value) != 32 {
			t.Fatalf("checksum = %+v", info.Checksum)
		}
	})

	t.Run("source errors", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		_, err := l.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "absent"}, testBucket, "dst", object.CopyOptions{})
		expectCode(t, err, s3err.NoSuchKey)
		_, err = l.CopyObject(ctx, object.Ref{Bucket: "no-bucket", Key: "k"}, testBucket, "dst", object.CopyOptions{})
		expectCode(t, err, s3err.NoSuchBucket)
		mustPut(t, l, "src", []byte("x"))
		_, err = l.CopyObject(ctx, object.Ref{Bucket: testBucket, Key: "src"}, "no-bucket", "dst", object.CopyOptions{})
		expectCode(t, err, s3err.NoSuchBucket)
	})

	t.Run("source conditions", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		info := mustPut(t, l, "src", []byte("x"))
		src := object.Ref{Bucket: testBucket, Key: "src"}
		tests := []struct {
			name string
			cond object.Conditions
			want s3err.Code
		}{
			{"if-match mismatch", object.Conditions{IfMatch: "0123"}, s3err.PreconditionFailed},
			{"if-match match", object.Conditions{IfMatch: info.ETag}, ""},
			{"if-none-match match reports 412", object.Conditions{IfNoneMatch: info.ETag}, s3err.PreconditionFailed},
			{"if-none-match mismatch", object.Conditions{IfNoneMatch: "0123"}, ""},
		}
		for _, tt := range tests {
			_, err := l.CopyObject(ctx, src, testBucket, "dst", object.CopyOptions{SourceConditions: tt.cond})
			expectCode(t, err, tt.want)
		}
	})

	t.Run("range is only valid for part copies", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		mustPut(t, l, "src", []byte("x"))
		_, err := l.CopyObject(t.Context(), object.Ref{Bucket: testBucket, Key: "src"}, testBucket, "dst",
			object.CopyOptions{SourceRange: &object.HTTPRange{Start: 0, End: 0}})
		expectCode(t, err, s3err.InvalidArgument)
	})
}

func suiteList(t *testing.T, newLayer func(*testing.T) object.Layer) {
	keys := []string{"a/1", "a/2", "b", "c/d/e", "c/f"}
	setup := func(t *testing.T) object.Layer {
		l := newLayer(t)
		mustBucket(t, l)
		for _, k := range keys {
			mustPut(t, l, k, []byte(k))
		}
		return l
	}
	list := func(t *testing.T, l object.Layer, opts object.ListOptions) object.ListResult {
		t.Helper()
		res, err := l.ListObjects(t.Context(), testBucket, opts)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	objectKeys := func(res object.ListResult) []string {
		out := make([]string, len(res.Objects))
		for i, o := range res.Objects {
			out[i] = o.Key
		}
		return out
	}

	t.Run("all keys in order", func(t *testing.T) {
		l := setup(t)
		res := list(t, l, object.ListOptions{})
		if got := objectKeys(res); !equalStrings(got, keys) {
			t.Fatalf("keys = %v", got)
		}
		for _, o := range res.Objects {
			if !o.IsLatest || o.ETag == "" || o.Size == 0 {
				t.Fatalf("listed info = %+v", o)
			}
		}
		if res.IsTruncated || len(res.CommonPrefixes) != 0 {
			t.Fatalf("unexpected paging state: %+v", res)
		}
	})

	t.Run("prefix", func(t *testing.T) {
		l := setup(t)
		res := list(t, l, object.ListOptions{Prefix: "a/"})
		if got := objectKeys(res); !equalStrings(got, []string{"a/1", "a/2"}) {
			t.Fatalf("keys = %v", got)
		}
	})

	t.Run("delimiter", func(t *testing.T) {
		l := setup(t)
		res := list(t, l, object.ListOptions{Delimiter: "/"})
		if got := objectKeys(res); !equalStrings(got, []string{"b"}) {
			t.Fatalf("keys = %v", got)
		}
		if !equalStrings(res.CommonPrefixes, []string{"a/", "c/"}) {
			t.Fatalf("common prefixes = %v", res.CommonPrefixes)
		}
		res = list(t, l, object.ListOptions{Prefix: "c/", Delimiter: "/"})
		if got := objectKeys(res); !equalStrings(got, []string{"c/f"}) || !equalStrings(res.CommonPrefixes, []string{"c/d/"}) {
			t.Fatalf("under c/: keys=%v prefixes=%v", got, res.CommonPrefixes)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		for _, delim := range []string{"", "/"} {
			l := setup(t)
			var seen []string
			after := ""
			pages := 0
			for {
				res := list(t, l, object.ListOptions{Delimiter: delim, After: after, MaxKeys: 2})
				pages++
				if len(res.Objects)+len(res.CommonPrefixes) > 2 {
					t.Fatalf("page holds %d entries, MaxKeys is 2", len(res.Objects)+len(res.CommonPrefixes))
				}
				for _, o := range res.Objects {
					seen = append(seen, o.Key)
				}
				seen = append(seen, res.CommonPrefixes...)
				if !res.IsTruncated {
					break
				}
				if res.NextAfter == "" || pages > 10 {
					t.Fatalf("delimiter %q: bad continuation %+v", delim, res)
				}
				after = res.NextAfter
			}
			want := keys
			if delim == "/" {
				want = []string{"a/", "b", "c/"}
			}
			sort.Strings(seen)
			if !equalStrings(seen, want) {
				t.Fatalf("delimiter %q: paged listing = %v, want %v", delim, seen, want)
			}
		}
	})

	t.Run("after", func(t *testing.T) {
		l := setup(t)
		res := list(t, l, object.ListOptions{After: "b"})
		if got := objectKeys(res); !equalStrings(got, []string{"c/d/e", "c/f"}) {
			t.Fatalf("keys after b = %v", got)
		}
	})

	t.Run("bounds and errors", func(t *testing.T) {
		l := setup(t)
		for _, n := range []int{-1, object.MaxListKeys + 1} {
			_, err := l.ListObjects(t.Context(), testBucket, object.ListOptions{MaxKeys: n})
			expectCode(t, err, s3err.InvalidArgument)
		}
		_, err := l.ListObjects(t.Context(), "no-bucket", object.ListOptions{})
		expectCode(t, err, s3err.NoSuchBucket)
	})

	t.Run("empty bucket", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		res := list(t, l, object.ListOptions{})
		if len(res.Objects) != 0 || res.IsTruncated {
			t.Fatalf("empty listing = %+v", res)
		}
	})
}

// equalStrings reports whether a and b hold the same strings in the same order.
func equalStrings(a, b []string) bool {
	return slices.Equal(a, b)
}
