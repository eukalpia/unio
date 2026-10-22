package fsm

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// updateTagsCmd builds an UpdateTags command for testBucket.
func updateTagsCmd(key, versionID, tags string) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_UpdateTags{UpdateTags: &pb.UpdateTags{
		BucketId:  uint64(testBucket),
		Key:       key,
		VersionId: versionID,
		Tags:      tags,
	}}}
}

// tagsOf returns the stored tag string of a record.
func tagsOf(rec *pb.ObjectRecord) string {
	return rec.GetAttributes().GetTags()
}

func TestUpdateTags(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, enabled))
	h.mustApply(putCmd(testBucket, objRec("k", "v1", etagA), nil))
	h.mustApply(putCmd(testBucket, objRec("k", "v2", etagB), nil))
	h.mustApply(deleteCmd(testBucket, "gone", "", "m1", nil)) // latest is a marker

	t.Run("latest by empty version id", func(t *testing.T) {
		before, err := h.getObject("k", "v2")
		if err != nil {
			t.Fatal(err)
		}
		res := h.mustApply(updateTagsCmd("k", "", "env=it"))
		if res.VersionId != "v2" {
			t.Fatalf("VersionId = %q, want the resolved latest v2", res.VersionId)
		}
		if len(res.Garbage) != 0 || len(res.UnusedParts) != 0 {
			t.Fatalf("UpdateTags returned garbage: %v", res.Garbage)
		}
		if tagsOf(res.GetObject()) != "env=it" {
			t.Fatalf("reply tags = %q, want env=it", tagsOf(res.GetObject()))
		}
		got, err := h.getObject("k", "v2")
		if err != nil {
			t.Fatal(err)
		}
		if tagsOf(got) != "env=it" {
			t.Fatalf("stored tags = %q, want env=it", tagsOf(got))
		}
		// Nothing but the tags may change: data, ETag, size and time stay.
		if got.Etag != before.Etag || got.Size != before.Size || got.ModTimeUnixNano != before.ModTimeUnixNano {
			t.Fatalf("UpdateTags changed the data or metadata: before %v, after %v", before, got)
		}
		if n := len(h.versionsOf("k")); n != 2 {
			t.Fatalf("stored versions = %d, want 2 (no new version)", n)
		}
		v1, err := h.getObject("k", "v1")
		if err != nil || tagsOf(v1) != "" {
			t.Fatalf("v1 changed by an update to v2: %v, %v", v1, err)
		}
	})

	t.Run("explicit version id", func(t *testing.T) {
		res := h.mustApply(updateTagsCmd("k", "v1", "team=storage"))
		if res.VersionId != "v1" {
			t.Fatalf("VersionId = %q, want v1", res.VersionId)
		}
		v1, err := h.getObject("k", "v1")
		if err != nil || tagsOf(v1) != "team=storage" {
			t.Fatalf("v1 tags = %v, %v; want team=storage", v1, err)
		}
		v2, err := h.getObject("k", "v2")
		if err != nil || tagsOf(v2) != "env=it" {
			t.Fatalf("v2 tags changed by an update to v1: %v, %v", v2, err)
		}
	})

	t.Run("empty tag set clears the tags", func(t *testing.T) {
		h.mustApply(updateTagsCmd("k", "v2", ""))
		got, err := h.getObject("k", "v2")
		if err != nil || tagsOf(got) != "" {
			t.Fatalf("tags after clear = %q, %v; want none", tagsOf(got), err)
		}
	})

	t.Run("reply omits inline content, store keeps it", func(t *testing.T) {
		rec := objRec("inline", "v1", etagA)
		rec.Inline = []byte("payload")
		h.mustApply(putCmd(testBucket, rec, nil))
		res := h.mustApply(updateTagsCmd("inline", "v1", "a=b"))
		if res.GetObject().Inline != nil {
			t.Fatalf("reply carries inline content %q", res.GetObject().Inline)
		}
		got, err := h.getObject("inline", "v1")
		if err != nil || string(got.Inline) != "payload" || tagsOf(got) != "a=b" {
			t.Fatalf("stored record = %v, %v; want inline payload and tags a=b", got, err)
		}
	})

	t.Run("tags survive a reopen", func(t *testing.T) {
		h.mustApply(updateTagsCmd("k", "v2", "durable=yes"))
		h.reopen()
		got, err := h.getObject("k", "v2")
		if err != nil || tagsOf(got) != "durable=yes" {
			t.Fatalf("tags after reopen = %q, %v", tagsOf(got), err)
		}
	})

	errorCases := []struct {
		name string
		cmd  *pb.Command
		code s3err.Code
	}{
		{"missing key", updateTagsCmd("absent", "", "a=b"), s3err.NoSuchKey},
		{"missing key with version", updateTagsCmd("absent", "v1", "a=b"), s3err.NoSuchVersion},
		{"unknown version", updateTagsCmd("k", "v9", "a=b"), s3err.NoSuchVersion},
		{"latest is a delete marker", updateTagsCmd("gone", "", "a=b"), s3err.NoSuchKey},
		{"delete marker named by version", updateTagsCmd("gone", "m1", "a=b"), s3err.MethodNotAllowed},
	}
	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			before := h.dumpState()
			wantCode(t, h.mustReject(tc.cmd), tc.code)
			h.assertUnchanged(before)
		})
	}

	rejectCases := []struct {
		name string
		cmd  *pb.Command
	}{
		{"zero bucket id", &pb.Command{Cmd: &pb.Command_UpdateTags{UpdateTags: &pb.UpdateTags{Key: "k", Tags: "a=b"}}}},
		{"empty key", updateTagsCmd("", "", "a=b")},
		{"version id too long", updateTagsCmd("k", strings.Repeat("v", maxIDLength+1), "a=b")},
		{"duplicate tag key", updateTagsCmd("k", "", "a=1&a=2")},
		{"reserved prefix", updateTagsCmd("k", "", "aws:x=1")},
		{"eleven tags", updateTagsCmd("k", "", tagsString(object.MaxTags+1))},
		{"tag set too long", updateTagsCmd("k", "", "k="+strings.Repeat("%FF", object.MaxEncodedTagsLength))},
		{"malformed escape", updateTagsCmd("k", "", "a=%ZZ")},
	}
	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			before := h.dumpState()
			if err := h.mustReject(tc.cmd); err == nil {
				t.Fatal("invalid command was accepted")
			}
			h.assertUnchanged(before)
		})
	}
}

// tagsString returns n valid tags k00=v, k01=v, ... joined with '&'.
func tagsString(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "k" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + "=v"
	}
	return strings.Join(parts, "&")
}
