package objecttest

import (
	"bytes"
	"maps"
	"slices"
	"testing"

	"github.com/eukalpia/unio/internal/object"
)

// suiteAliasing checks that the values a layer returns share no memory with
// its stored state. A caller that changes what it received must not change
// what the layer returns later, and must not change the stored object either.
func suiteAliasing(t *testing.T, newLayer func(*testing.T) object.Layer) {
	for _, size := range []int{100, inlineSize + 1} {
		t.Run("object info "+sizeName(size), func(t *testing.T) {
			l := newLayer(t)
			ctx := t.Context()
			mustBucket(t, l)
			data := payload(size, seedOf(size)+11)
			put, err := l.PutObject(ctx, testBucket, "k",
				NewBody(data, WithChecksum(object.ChecksumCRC32C)),
				object.PutOptions{UserMetadata: map[string]string{"k": "v"}})
			if err != nil {
				t.Fatal(err)
			}
			want := copyInfo(put)
			scramble(&put)

			head, err := l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !sameInfo(head, want) {
				t.Fatalf("HEAD after the caller changed the PUT result: %+v, want %+v", head, want)
			}
			scramble(&head)

			r, err := l.GetObject(ctx, testBucket, "k", object.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !sameInfo(r.Info, want) {
				t.Fatalf("GET after the caller changed HEAD: %+v, want %+v", r.Info, want)
			}
			scramble(&r.Info)
			closeReader(t, r)

			listed, err := l.ListObjects(ctx, testBucket, object.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(listed.Objects) != 1 || !sameInfo(listed.Objects[0], want) {
				t.Fatalf("LIST after the caller changed GET: %+v, want %+v", listed.Objects, want)
			}
		})
	}

	t.Run("part and upload info", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		id, err := l.NewMultipartUpload(ctx, testBucket, "k", object.PutOptions{ChecksumAlgorithm: object.ChecksumCRC32C})
		if err != nil {
			t.Fatal(err)
		}
		part, err := l.PutObjectPart(ctx, testBucket, "k", id, 1,
			NewBody([]byte("tail"), WithChecksum(object.ChecksumCRC32C)))
		if err != nil {
			t.Fatal(err)
		}
		wantPart := copyPart(part)
		part.Checksum.Value[0] ^= 0xff

		listed, err := l.ListParts(ctx, testBucket, "k", id, object.ListPartsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed.Parts) != 1 || !samePart(listed.Parts[0], wantPart) {
			t.Fatalf("ListParts after the caller changed the part info: %+v, want %+v", listed.Parts, wantPart)
		}
		listed.Parts[0].Checksum.Value[0] ^= 0xff

		info, err := l.CompleteMultipartUpload(ctx, testBucket, "k", id,
			[]object.CompletePart{{PartNumber: 1, ETag: wantPart.ETag, Checksum: wantPart.Checksum}},
			object.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		want := copyInfo(info)
		info.Checksum.Value[0] ^= 0xff
		head, err := l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !sameInfo(head, want) {
			t.Fatalf("HEAD after the caller changed the completion result: %+v, want %+v", head, want)
		}
	})

	t.Run("listings", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		mustPut(t, l, "a/1", []byte("x"))
		mustPut(t, l, "b", []byte("y"))
		opts := object.ListOptions{Delimiter: "/"}
		res, err := l.ListObjects(ctx, testBucket, opts)
		if err != nil {
			t.Fatal(err)
		}
		if !equalStrings(res.CommonPrefixes, []string{"a/"}) {
			t.Fatalf("common prefixes = %v", res.CommonPrefixes)
		}
		wantPrefixes := slices.Clone(res.CommonPrefixes)
		res.CommonPrefixes[0] = "zzz"
		again, err := l.ListObjects(ctx, testBucket, opts)
		if err != nil {
			t.Fatal(err)
		}
		if !equalStrings(again.CommonPrefixes, wantPrefixes) {
			t.Fatalf("common prefixes after the caller changed them: %v, want %v", again.CommonPrefixes, wantPrefixes)
		}

		uploads, err := l.ListMultipartUploads(ctx, testBucket, object.ListUploadsOptions{Delimiter: "/"})
		if err != nil {
			t.Fatal(err)
		}
		if len(uploads.CommonPrefixes) != 0 {
			t.Fatalf("unexpected common prefixes before any upload: %v", uploads.CommonPrefixes)
		}
		if _, err := l.NewMultipartUpload(ctx, testBucket, "d/x", object.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		up, err := l.ListMultipartUploads(ctx, testBucket, object.ListUploadsOptions{Delimiter: "/"})
		if err != nil {
			t.Fatal(err)
		}
		wantUploadPrefixes := slices.Clone(up.CommonPrefixes)
		if len(up.CommonPrefixes) != 1 {
			t.Fatalf("upload common prefixes = %v, want one", up.CommonPrefixes)
		}
		up.CommonPrefixes[0] = "zzz"
		upAgain, err := l.ListMultipartUploads(ctx, testBucket, object.ListUploadsOptions{Delimiter: "/"})
		if err != nil {
			t.Fatal(err)
		}
		if !equalStrings(upAgain.CommonPrefixes, wantUploadPrefixes) {
			t.Fatalf("upload common prefixes after the caller changed them: %v, want %v",
				upAgain.CommonPrefixes, wantUploadPrefixes)
		}
	})
}

// scramble changes every byte-valued and map-valued field of info that a
// caller could reach, the way a careless caller might.
func scramble(info *object.Info) {
	for i := range info.Checksum.Value {
		info.Checksum.Value[i] ^= 0xff
	}
	if info.UserMetadata != nil {
		info.UserMetadata["k"] = "changed"
	}
}

// copyInfo returns an Info that shares no memory with info.
func copyInfo(info object.Info) object.Info {
	out := info
	out.Checksum.Value = bytes.Clone(info.Checksum.Value)
	out.UserMetadata = cloneMap(info.UserMetadata)
	return out
}

// sameInfo reports whether a and b describe the same version in every field
// the suite compares.
func sameInfo(a, b object.Info) bool {
	return a.ETag == b.ETag && a.Size == b.Size && a.Parts == b.Parts &&
		a.Checksum.Algorithm == b.Checksum.Algorithm && a.Checksum.Type == b.Checksum.Type &&
		a.Checksum.Parts == b.Checksum.Parts && bytes.Equal(a.Checksum.Value, b.Checksum.Value) &&
		maps.Equal(a.UserMetadata, b.UserMetadata)
}

// copyPart returns a PartInfo that shares no memory with p.
func copyPart(p object.PartInfo) object.PartInfo {
	out := p
	out.Checksum.Value = bytes.Clone(p.Checksum.Value)
	return out
}

// samePart reports whether a and b describe the same part.
func samePart(a, b object.PartInfo) bool {
	return a.PartNumber == b.PartNumber && a.Size == b.Size && a.ETag == b.ETag &&
		a.Checksum.Algorithm == b.Checksum.Algorithm && bytes.Equal(a.Checksum.Value, b.Checksum.Value)
}
