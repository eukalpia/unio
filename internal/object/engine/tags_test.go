package engine_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3err"
)

// TestTagsLeaveDataAndVersionAlone checks that tagging a version changes its
// tags and nothing else: the data, the ETag, the modification time and the
// version stay as they were, and no version is created.
func TestTagsLeaveDataAndVersionAlone(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t, engine.Options{})
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o", Versioning: object.VersioningEnabled}); err != nil {
		t.Fatal(err)
	}
	body := []byte("versioned payload")
	v1, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody(body), object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("newer")), object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}

	tagged, err := h.PutObjectTags(ctx, testBucket, "k", object.TagOptions{VersionID: v1.VersionID, Tags: object.TagSet{{Key: "env", Value: "it"}}})
	if err != nil {
		t.Fatalf("PutObjectTags on an older version: %v", err)
	}
	if tagged.VersionID != v1.VersionID || tagged.ETag != v1.ETag || tagged.Size != v1.Size || !tagged.ModTime.Equal(v1.ModTime) {
		t.Fatalf("tagging changed version %q: before %+v, after %+v", v1.VersionID, v1, tagged)
	}
	if tagged.Tags != "env=it" {
		t.Fatalf("returned tags = %q", tagged.Tags)
	}

	old, err := h.GetObject(ctx, testBucket, "k", object.GetOptions{VersionID: v1.VersionID})
	if err != nil {
		t.Fatal(err)
	}
	got, err := readAllBody(old)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("data of the tagged version = %q, %v", got, err)
	}
	if old.Info.Tags != "env=it" || old.Info.ETag != v1.ETag {
		t.Fatalf("stored version = %+v", old.Info)
	}

	latest, err := h.HeadObject(ctx, testBucket, "k", object.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if latest.VersionID != v2.VersionID || latest.Tags != "" {
		t.Fatalf("latest after tagging an older version = %+v, want version %q untagged", latest, v2.VersionID)
	}

	// Tagging the latest version by its id, and by an empty id, is the same write.
	if _, err := h.PutObjectTags(ctx, testBucket, "k", object.TagOptions{Tags: object.TagSet{{Key: "a", Value: "b"}}}); err != nil {
		t.Fatal(err)
	}
	cur, _, err := h.GetObjectTags(ctx, testBucket, "k", v2.VersionID)
	if err != nil || cur.VersionID != v2.VersionID {
		t.Fatalf("GetObjectTags latest by id = %+v, %v", cur, err)
	}
	if _, tags, err := h.GetObjectTags(ctx, testBucket, "k", ""); err != nil || len(tags) != 1 || tags[0] != (object.Tag{Key: "a", Value: "b"}) {
		t.Fatalf("GetObjectTags latest = %v, %v", tags, err)
	}
}

// TestTagsOnDeleteMarker checks that a delete marker is never tagged, and that
// the other versions of the key still are.
func TestTagsOnDeleteMarker(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t, engine.Options{})
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o", Versioning: object.VersioningEnabled}); err != nil {
		t.Fatal(err)
	}
	data, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("x")), object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	del, err := h.DeleteObject(ctx, testBucket, "k", object.DeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	marker := del.DeleteMarkerVersionID
	if !del.DeleteMarker || marker == "" {
		t.Fatalf("DeleteObject = %+v, want a delete marker", del)
	}

	set := object.TagOptions{Tags: object.TagSet{{Key: "a", Value: "b"}}}
	if _, err := h.PutObjectTags(ctx, testBucket, "k", set); s3err.CodeOf(err) != s3err.NoSuchKey {
		t.Fatalf("tagging a key whose latest is a marker: %v, want NoSuchKey", err)
	}
	if _, _, err := h.GetObjectTags(ctx, testBucket, "k", ""); s3err.CodeOf(err) != s3err.NoSuchKey {
		t.Fatalf("reading the tags of a key whose latest is a marker: %v, want NoSuchKey", err)
	}
	if _, err := h.DeleteObjectTags(ctx, testBucket, "k", ""); s3err.CodeOf(err) != s3err.NoSuchKey {
		t.Fatalf("deleting the tags of a key whose latest is a marker: %v, want NoSuchKey", err)
	}
	set.VersionID = marker
	if _, err := h.PutObjectTags(ctx, testBucket, "k", set); s3err.CodeOf(err) != s3err.MethodNotAllowed {
		t.Fatalf("tagging a delete marker by id: %v, want MethodNotAllowed", err)
	}
	if _, _, err := h.GetObjectTags(ctx, testBucket, "k", marker); s3err.CodeOf(err) != s3err.MethodNotAllowed {
		t.Fatalf("reading the tags of a delete marker by id: %v, want MethodNotAllowed", err)
	}

	if _, err := h.PutObjectTags(ctx, testBucket, "k", object.TagOptions{VersionID: data.VersionID, Tags: object.TagSet{{Key: "a", Value: "b"}}}); err != nil {
		t.Fatalf("tagging the data version behind a marker: %v", err)
	}
}

// TestTagRulesAgreeOnBothPaths checks that the x-amz-tagging path of PutObject
// and the tagging operation accept the same tag sets and refuse the same ones.
// Both run the rules of object.ValidateTags, through DecodeTags on the stored
// form and directly on a TagSet.
func TestTagRulesAgreeOnBothPaths(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t, engine.Options{})
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("x")), object.PutOptions{}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		encoded string // the x-amz-tagging form
		valid   bool
	}{
		{"plain", "env=it&team=storage", true},
		{"empty", "", true},
		{"ten tags", tagsEncoded(object.MaxTags), true},
		{"eleven tags", tagsEncoded(object.MaxTags + 1), false},
		{"duplicate key", "a=1&a=2", false},
		{"duplicate key after decoding", "a=1&%61=2", false},
		{"reserved prefix", "aws:created-by=me", false},
		{"reserved prefix upper case", "AWS:x=1", false},
		{"key of 128 multi-byte characters", strings.Repeat("%C3%A9", object.MaxTagKeyLength) + "=v", true},
		{"key of 129 characters", strings.Repeat("k", object.MaxTagKeyLength+1) + "=v", false},
		{"value of 257 characters", "k=" + strings.Repeat("v", object.MaxTagValueLength+1), false},
		{"control character", "k=%0A", false},
		{"invalid utf-8", "k=%C3", false},
		{"malformed escape", "k=%ZZ", false},
		{"empty pair", "a=1&&b=2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, putErr := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("x")), object.PutOptions{Tags: tc.encoded})
			if tc.valid {
				if putErr != nil {
					t.Fatalf("PutObject refused a valid tag set: %v", putErr)
				}
				return
			}
			if s3err.CodeOf(putErr) != s3err.InvalidTag {
				t.Fatalf("PutObject accepted or misreported an invalid tag set: %v", putErr)
			}

			set, decodeErr := object.DecodeTags(tc.encoded)
			if decodeErr == nil {
				t.Fatal("DecodeTags accepted a set that PutObject refused")
			}
			if set != nil {
				t.Fatal("DecodeTags returned a set with its error")
			}
		})
	}
}

// readAllBody reads and closes the body of a reader.
func readAllBody(r *object.Reader) ([]byte, error) {
	defer r.Close()
	return io.ReadAll(r)
}

// tagsEncoded returns n valid encoded tags with distinct keys.
func tagsEncoded(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "k" + string(rune('a'+i)) + "=v"
	}
	return strings.Join(parts, "&")
}
