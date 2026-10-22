package objecttest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// suiteTags checks object tagging against any layer. The cases name no version
// that exists, so they hold for layers with and without versions.
func suiteTags(t *testing.T, newLayer func(*testing.T) object.Layer) {
	t.Run("set read and clear", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		body := []byte("tagged body")
		before := mustPut(t, l, "k", body)

		set := object.TagSet{{Key: "env", Value: "it"}, {Key: "team", Value: "storage & backup"}}
		info, err := l.PutObjectTags(ctx, bucket, "k", object.TagOptions{Tags: set})
		if err != nil {
			t.Fatalf("PutObjectTags: %v", err)
		}
		checkUnchangedData(t, info, before)

		_, got, err := l.GetObjectTags(ctx, bucket, "k", "")
		if err != nil {
			t.Fatalf("GetObjectTags: %v", err)
		}
		if object.EncodeTags(got) != object.EncodeTags(set) {
			t.Fatalf("tags after put = %v, want %v", got, set)
		}
		head, err := l.HeadObject(ctx, bucket, "k", object.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if head.Tags != object.EncodeTags(set) {
			t.Fatalf("head tags = %q, want %q", head.Tags, object.EncodeTags(set))
		}
		checkUnchangedData(t, head, before)
		if got := mustRead(t, l, "k", object.GetOptions{}); !bytes.Equal(got, body) {
			t.Fatal("tagging changed the data")
		}

		info, err = l.DeleteObjectTags(ctx, bucket, "k", "")
		if err != nil {
			t.Fatalf("DeleteObjectTags: %v", err)
		}
		checkUnchangedData(t, info, before)
		_, got, err = l.GetObjectTags(ctx, bucket, "k", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("tags after delete = %v, want none", got)
		}
	})

	t.Run("replace keeps only the new set", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		mustPut(t, l, "k", []byte("x"))
		if _, err := l.PutObjectTags(ctx, bucket, "k", object.TagOptions{Tags: object.TagSet{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := l.PutObjectTags(ctx, bucket, "k", object.TagOptions{Tags: object.TagSet{{Key: "c", Value: ""}}}); err != nil {
			t.Fatal(err)
		}
		_, got, err := l.GetObjectTags(ctx, bucket, "k", "")
		if err != nil {
			t.Fatal(err)
		}
		if object.EncodeTags(got) != "c=" {
			t.Fatalf("tags after replace = %v, want only c", got)
		}
	})

	t.Run("missing targets", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		set := object.TagOptions{Tags: object.TagSet{{Key: "a", Value: "b"}}}
		_, err := l.PutObjectTags(ctx, bucket, "absent", set)
		requireTagCode(t, err, s3err.NoSuchKey)
		_, _, err = l.GetObjectTags(ctx, bucket, "absent", "")
		requireTagCode(t, err, s3err.NoSuchKey)
		_, err = l.DeleteObjectTags(ctx, bucket, "absent", "")
		requireTagCode(t, err, s3err.NoSuchKey)
		_, err = l.PutObjectTags(ctx, "absent-bucket", "k", set)
		requireTagCode(t, err, s3err.NoSuchBucket)
		_, _, err = l.GetObjectTags(ctx, "absent-bucket", "k", "")
		requireTagCode(t, err, s3err.NoSuchBucket)
	})

	t.Run("unknown version", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		mustPut(t, l, "k", []byte("x"))
		_, err := l.PutObjectTags(ctx, bucket, "k", object.TagOptions{VersionID: "no-such-version", Tags: object.TagSet{{Key: "a", Value: "b"}}})
		requireTagCode(t, err, s3err.NoSuchVersion)
		_, _, err = l.GetObjectTags(ctx, bucket, "k", "no-such-version")
		requireTagCode(t, err, s3err.NoSuchVersion)
		_, err = l.DeleteObjectTags(ctx, bucket, "k", "no-such-version")
		requireTagCode(t, err, s3err.NoSuchVersion)
	})

	t.Run("invalid tag sets are refused and change nothing", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		bucket := mustBucket(t, l)
		mustPut(t, l, "k", []byte("x"))
		keep := object.TagOptions{Tags: object.TagSet{{Key: "keep", Value: "me"}}}
		if _, err := l.PutObjectTags(ctx, bucket, "k", keep); err != nil {
			t.Fatal(err)
		}
		bad := map[string]object.TagSet{
			"duplicate key":        {{Key: "a", Value: "1"}, {Key: "a", Value: "2"}},
			"reserved prefix":      {{Key: "aws:created-by", Value: "x"}},
			"too many tags":        tagSetOfSize(object.MaxTags + 1),
			"empty key":            {{Key: "", Value: "x"}},
			"key too long":         {{Key: strings.Repeat("k", object.MaxTagKeyLength+1), Value: "x"}},
			"value too long":       {{Key: "k", Value: strings.Repeat("v", object.MaxTagValueLength+1)}},
			"control in value":     {{Key: "k", Value: "a\nb"}},
			"invalid utf-8 in key": {{Key: "\xff", Value: "x"}},
		}
		for name, set := range bad {
			t.Run(name, func(t *testing.T) {
				_, err := l.PutObjectTags(ctx, bucket, "k", object.TagOptions{Tags: set})
				requireTagCode(t, err, s3err.InvalidTag)
			})
		}
		_, got, err := l.GetObjectTags(ctx, bucket, "k", "")
		if err != nil {
			t.Fatal(err)
		}
		if object.EncodeTags(got) != object.EncodeTags(keep.Tags) {
			t.Fatalf("refused sets changed the tags: %v", got)
		}
	})
}

// checkUnchangedData fails the test unless info keeps the data identity of
// before: size, ETag and modification time.
func checkUnchangedData(t *testing.T, info, before object.Info) {
	t.Helper()
	if info.Size != before.Size || info.ETag != before.ETag || !info.ModTime.Equal(before.ModTime) {
		t.Fatalf("tagging changed the object: before size=%d etag=%q mod=%v, after size=%d etag=%q mod=%v",
			before.Size, before.ETag, before.ModTime, info.Size, info.ETag, info.ModTime)
	}
}

// requireTagCode fails the test unless err carries the S3 code want.
func requireTagCode(t *testing.T, err error, want s3err.Code) {
	t.Helper()
	if got := s3err.CodeOf(err); got != want {
		t.Fatalf("error = %v (code %q), want code %q", err, got, want)
	}
}

// tagSetOfSize returns a set of n tags with distinct keys.
func tagSetOfSize(n int) object.TagSet {
	set := make(object.TagSet, n)
	for i := range set {
		set[i] = object.Tag{Key: "k" + string(rune('a'+i)), Value: "v"}
	}
	return set
}
