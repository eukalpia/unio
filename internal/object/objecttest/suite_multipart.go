package objecttest

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine/digest"
	"github.com/eukalpia/unio/internal/s3err"
)

// uploadWith starts an upload on key and stores the given parts, numbered
// from 1. It returns the upload id and the part list for completion.
func uploadWith(t *testing.T, l object.Layer, key string, parts ...[]byte) (string, []object.CompletePart) {
	t.Helper()
	ctx := t.Context()
	id, err := l.NewMultipartUpload(ctx, testBucket, key, object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var list []object.CompletePart
	for i, data := range parts {
		info, err := l.PutObjectPart(ctx, testBucket, key, id, i+1, bodyOf(data))
		if err != nil {
			t.Fatalf("part %d: %v", i+1, err)
		}
		list = append(list, object.CompletePart{PartNumber: i + 1, ETag: info.ETag})
	}
	return id, list
}

// multipartETag computes the expected ETag of a multipart object.
func multipartETag(parts ...[]byte) string {
	var raw []byte
	for _, p := range parts {
		sum := digest.SumMD5(p)
		raw = append(raw, sum[:]...)
	}
	sum := digest.SumMD5(raw)
	return fmt.Sprintf("%s-%d", hex.EncodeToString(sum[:]), len(parts))
}

func suiteMultipart(t *testing.T, newLayer func(*testing.T) object.Layer) {
	t.Run("happy path", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		p1 := payload(5*mib, 11)
		p2 := payload(3, 12)
		id, err := l.NewMultipartUpload(ctx, testBucket, "big", object.PutOptions{
			ContentType:  "application/x-part",
			UserMetadata: map[string]string{"k": "v"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 32 {
			t.Fatalf("upload id %q is not 32 characters", id)
		}
		i1, err := l.PutObjectPart(ctx, testBucket, "big", id, 1, bodyOf(p1))
		if err != nil {
			t.Fatal(err)
		}
		i2, err := l.PutObjectPart(ctx, testBucket, "big", id, 2, bodyOf(p2))
		if err != nil {
			t.Fatal(err)
		}
		if i1.ETag != md5Hex(p1) || i1.Size != int64(len(p1)) || i2.ETag != md5Hex(p2) || i2.PartNumber != 2 {
			t.Fatalf("part infos = %+v %+v", i1, i2)
		}
		info, err := l.CompleteMultipartUpload(ctx, testBucket, "big", id, []object.CompletePart{
			{PartNumber: 1, ETag: i1.ETag}, {PartNumber: 2, ETag: i2.ETag},
		}, object.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if info.ETag != multipartETag(p1, p2) || info.Parts != 2 || info.Size != int64(len(p1)+len(p2)) {
			t.Fatalf("complete info = %+v, want etag %s", info, multipartETag(p1, p2))
		}
		if info.ContentType != "application/x-part" || info.UserMetadata["k"] != "v" {
			t.Fatalf("metadata of the upload was not applied: %+v", info)
		}
		want := append(bytes.Clone(p1), p2...)
		if got := mustRead(t, l, "big", object.GetOptions{}); !bytes.Equal(got, want) {
			t.Fatal("completed object differs from the parts")
		}
		head, err := l.HeadObject(ctx, testBucket, "big", object.GetOptions{})
		if err != nil || head.Parts != 2 || head.ETag != info.ETag {
			t.Fatalf("head = %+v, %v", head, err)
		}
		r, err := l.GetObject(ctx, testBucket, "big", object.GetOptions{PartNumber: 2})
		if err != nil {
			t.Fatal(err)
		}
		if got := readAllBytes(t, r); !bytes.Equal(got, p2) || r.Info.Parts != 2 {
			t.Fatalf("part 2 read: %d bytes, parts=%d", len(got), r.Info.Parts)
		}
	})

	t.Run("part number bounds", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		id, err := l.NewMultipartUpload(t.Context(), testBucket, "k", object.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []int{0, object.MaxPartNumber + 1} {
			_, err := l.PutObjectPart(t.Context(), testBucket, "k", id, n, bodyOf([]byte("x")))
			expectCode(t, err, s3err.InvalidArgument)
		}
		_, err = l.PutObjectPart(t.Context(), testBucket, "k", id, object.MaxPartNumber, bodyOf([]byte("x")))
		expectCode(t, err, "")
	})

	t.Run("order errors", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		id, parts := uploadWith(t, l, "k", []byte("aaaaa"), []byte("bbbbb"))
		ctx := t.Context()
		reversed := []object.CompletePart{parts[1], parts[0]}
		_, err := l.CompleteMultipartUpload(ctx, testBucket, "k", id, reversed, object.PutOptions{})
		expectCode(t, err, s3err.InvalidPartOrder)
		dup := []object.CompletePart{parts[0], parts[0]}
		_, err = l.CompleteMultipartUpload(ctx, testBucket, "k", id, dup, object.PutOptions{})
		expectCode(t, err, s3err.InvalidPartOrder)
		_, err = l.CompleteMultipartUpload(ctx, testBucket, "k", id, nil, object.PutOptions{})
		if err == nil {
			t.Fatal("completing with no parts must fail")
		}
		_, err = l.CompleteMultipartUpload(ctx, testBucket, "k", id, []object.CompletePart{{PartNumber: 0, ETag: parts[0].ETag}}, object.PutOptions{})
		expectCode(t, err, s3err.InvalidArgument)
	})

	t.Run("parts smaller than the minimum", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		id, parts := uploadWith(t, l, "k", payload(1024, 1), payload(3, 2))
		_, err := l.CompleteMultipartUpload(t.Context(), testBucket, "k", id, parts, object.PutOptions{})
		expectCode(t, err, s3err.EntityTooSmall)
	})

	t.Run("wrong etag and missing part", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		id, parts := uploadWith(t, l, "k", payload(5*mib, 3), []byte("tail"))
		ctx := t.Context()
		bad := []object.CompletePart{{PartNumber: 1, ETag: "0123"}, parts[1]}
		_, err := l.CompleteMultipartUpload(ctx, testBucket, "k", id, bad, object.PutOptions{})
		expectCode(t, err, s3err.InvalidPart)
		missing := []object.CompletePart{parts[0], {PartNumber: 3, ETag: "0123"}}
		_, err = l.CompleteMultipartUpload(ctx, testBucket, "k", id, missing, object.PutOptions{})
		expectCode(t, err, s3err.InvalidPart)
		quoted := []object.CompletePart{{PartNumber: 1, ETag: `"` + parts[0].ETag + `"`}, parts[1]}
		_, err = l.CompleteMultipartUpload(ctx, testBucket, "k", id, quoted, object.PutOptions{})
		expectCode(t, err, "")
	})

	t.Run("complete conditions", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		mustPut(t, l, "k", []byte("existing"))
		id, parts := uploadWith(t, l, "k", []byte("new"))
		_, err := l.CompleteMultipartUpload(t.Context(), testBucket, "k", id, parts,
			object.PutOptions{Conditions: object.Conditions{IfNoneMatch: "*"}})
		expectCode(t, err, s3err.PreconditionFailed)
		_, err = l.CompleteMultipartUpload(t.Context(), testBucket, "k", id, parts, object.PutOptions{})
		expectCode(t, err, "")
	})

	t.Run("abort", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		base := dataCount(t, l)
		id, parts := uploadWith(t, l, "k", payload(5*mib, 4))
		if base >= 0 && dataCount(t, l) <= base {
			t.Fatalf("uploaded part is not counted as stored data")
		}
		expectCode(t, l.AbortMultipartUpload(ctx, testBucket, "k", id), "")
		expectCode(t, l.AbortMultipartUpload(ctx, testBucket, "k", id), s3err.NoSuchUpload)
		_, err := l.CompleteMultipartUpload(ctx, testBucket, "k", id, parts, object.PutOptions{})
		expectCode(t, err, s3err.NoSuchUpload)
		_, err = l.PutObjectPart(ctx, testBucket, "k", id, 2, bodyOf([]byte("x")))
		expectCode(t, err, s3err.NoSuchUpload)
		_, err = l.ListParts(ctx, testBucket, "k", id, object.ListPartsOptions{})
		expectCode(t, err, s3err.NoSuchUpload)
		_, err = l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
		expectCode(t, err, s3err.NoSuchKey)
		flush(t, l)
		if base >= 0 {
			if got := dataCount(t, l); got != base {
				t.Fatalf("stored data after abort = %d, want %d", got, base)
			}
		}
	})

	t.Run("list parts", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		data := [][]byte{payload(5*mib, 5), []byte("bb"), []byte("cccc")}
		id, _ := uploadWith(t, l, "k", data...)
		res, err := l.ListParts(ctx, testBucket, "k", id, object.ListPartsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Parts) != 3 || res.IsTruncated {
			t.Fatalf("list parts = %+v", res)
		}
		for i, p := range res.Parts {
			if p.PartNumber != i+1 || p.Size != int64(len(data[i])) || p.ETag != md5Hex(data[i]) || p.ModTime == 0 {
				t.Fatalf("part %d = %+v", i+1, p)
			}
		}
		page, err := l.ListParts(ctx, testBucket, "k", id, object.ListPartsOptions{MaxParts: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Parts) != 2 || !page.IsTruncated || page.NextPartNumberMarker != 2 {
			t.Fatalf("first page = %+v", page)
		}
		rest, err := l.ListParts(ctx, testBucket, "k", id, object.ListPartsOptions{PartNumberMarker: page.NextPartNumberMarker, MaxParts: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(rest.Parts) != 1 || rest.Parts[0].PartNumber != 3 || rest.IsTruncated {
			t.Fatalf("second page = %+v", rest)
		}
		// The upload belongs to testBucket; looking it up under another
		// bucket finds nothing.
		_, err = l.ListParts(ctx, "other-bucket", "k", id, object.ListPartsOptions{})
		expectCode(t, err, s3err.NoSuchUpload)
	})

	t.Run("list uploads", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		ids := map[string]bool{}
		for _, key := range []string{"k1", "k2", "dir/k3"} {
			id, err := l.NewMultipartUpload(ctx, testBucket, key, object.PutOptions{})
			if err != nil {
				t.Fatal(err)
			}
			ids[id] = true
		}
		res, err := l.ListMultipartUploads(ctx, testBucket, object.ListUploadsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Uploads) != 3 {
			t.Fatalf("uploads = %+v", res.Uploads)
		}
		for _, u := range res.Uploads {
			if !ids[u.UploadID] || u.Initiated == 0 {
				t.Fatalf("unexpected upload %+v", u)
			}
		}
		delim, err := l.ListMultipartUploads(ctx, testBucket, object.ListUploadsOptions{Delimiter: "/"})
		if err != nil {
			t.Fatal(err)
		}
		if len(delim.Uploads) != 2 || !equalStrings(delim.CommonPrefixes, []string{"dir/"}) {
			t.Fatalf("delimited uploads = %+v", delim)
		}
		seen := map[string]bool{}
		marker, idMarker := "", ""
		for pages := 0; ; pages++ {
			if pages > 10 {
				t.Fatal("paging does not terminate")
			}
			page, err := l.ListMultipartUploads(ctx, testBucket, object.ListUploadsOptions{
				MaxUploads: 1, KeyMarker: marker, UploadIDMarker: idMarker,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, u := range page.Uploads {
				seen[u.UploadID] = true
			}
			if !page.IsTruncated {
				break
			}
			marker, idMarker = page.NextKeyMarker, page.NextUploadIDMarker
		}
		if len(seen) != 3 {
			t.Fatalf("paged uploads = %d, want 3", len(seen))
		}
		_, err = l.ListMultipartUploads(ctx, "no-bucket", object.ListUploadsOptions{})
		expectCode(t, err, s3err.NoSuchBucket)
	})

	t.Run("ranges across part boundaries", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		p1, p2, p3 := payload(5*mib+1, 6), payload(5*mib+2, 7), payload(7, 8)
		id, parts := uploadWith(t, l, "k", p1, p2, p3)
		if _, err := l.CompleteMultipartUpload(t.Context(), testBucket, "k", id, parts, object.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		all := append(append(bytes.Clone(p1), p2...), p3...)
		n := int64(len(all))
		b1 := int64(len(p1))
		b2 := b1 + int64(len(p2))
		tests := []struct {
			name string
			rng  object.HTTPRange
			off  int64
			len  int64
		}{
			{"part one into two", object.HTTPRange{Start: b1 - 3, End: b1 + 4}, b1 - 3, 8},
			{"part two into three", object.HTTPRange{Start: b2 - 2, End: n - 1}, b2 - 2, n - b2 + 2},
			{"suffix across two boundaries", object.HTTPRange{Suffix: 10}, n - 10, 10},
			{"whole object", object.HTTPRange{Start: 0, End: -1}, 0, n},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rng := tt.rng
				r, err := l.GetObject(t.Context(), testBucket, "k", object.GetOptions{Range: &rng})
				if err != nil {
					t.Fatal(err)
				}
				if r.Offset != tt.off || r.Length != tt.len {
					t.Fatalf("window = [%d,+%d), want [%d,+%d)", r.Offset, r.Length, tt.off, tt.len)
				}
				if got := readAllBytes(t, r); !bytes.Equal(got, all[tt.off:tt.off+tt.len]) {
					t.Fatal("range bytes differ")
				}
			})
		}
	})

	t.Run("part number reads", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		p1, p2, p3 := payload(5*mib, 13), payload(5*mib, 14), payload(4, 15)
		id, parts := uploadWith(t, l, "k", p1, p2, p3)
		if _, err := l.CompleteMultipartUpload(t.Context(), testBucket, "k", id, parts, object.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		for n, want := range map[int][]byte{1: p1, 2: p2, 3: p3} {
			r, err := l.GetObject(t.Context(), testBucket, "k", object.GetOptions{PartNumber: n})
			if err != nil {
				t.Fatal(err)
			}
			if got := readAllBytes(t, r); !bytes.Equal(got, want) {
				t.Fatalf("part %d differs", n)
			}
		}
		rng := object.HTTPRange{Start: 0, End: 9}
		r, err := l.GetObject(t.Context(), testBucket, "k", object.GetOptions{PartNumber: 1, Range: &rng})
		if err != nil {
			t.Fatal(err)
		}
		if got := readAllBytes(t, r); !bytes.Equal(got, p1[:10]) {
			t.Fatal("range within part 1 differs")
		}
		head, err := l.HeadObject(t.Context(), testBucket, "k", object.GetOptions{PartNumber: 2})
		if err != nil || head.Size != int64(len(p2)) {
			t.Fatalf("head of part 2 = %+v, %v", head, err)
		}
		_, err = l.GetObject(t.Context(), testBucket, "k", object.GetOptions{PartNumber: 4})
		expectCode(t, err, s3err.InvalidArgument)
	})

	t.Run("part number on a single-part object", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		data := payload(inlineSize+1, 16)
		mustPut(t, l, "k", data)
		if got := mustRead(t, l, "k", object.GetOptions{PartNumber: 1}); !bytes.Equal(got, data) {
			t.Fatal("part 1 of a single-part object is the whole object")
		}
		_, err := l.GetObject(t.Context(), testBucket, "k", object.GetOptions{PartNumber: 2})
		expectCode(t, err, s3err.InvalidArgument)
	})

	t.Run("copy part", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		src := payload(10*mib, 17)
		mustPut(t, l, "src", src)
		id, err := l.NewMultipartUpload(ctx, testBucket, "dst", object.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ref := object.Ref{Bucket: testBucket, Key: "src"}
		p1, err := l.CopyObjectPart(ctx, ref, testBucket, "dst", id, 1, object.CopyOptions{
			SourceRange: &object.HTTPRange{Start: 0, End: 5*mib - 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		if p1.Size != 5*mib || p1.ETag != md5Hex(src[:5*mib]) {
			t.Fatalf("copied part = %+v", p1)
		}
		p2, err := l.PutObjectPart(ctx, testBucket, "dst", id, 2, bodyOf([]byte("tail")))
		if err != nil {
			t.Fatal(err)
		}
		_, err = l.CopyObjectPart(ctx, ref, testBucket, "dst", id, 3, object.CopyOptions{
			SourceConditions: object.Conditions{IfMatch: "0123"},
		})
		expectCode(t, err, s3err.PreconditionFailed)
		_, err = l.CopyObjectPart(ctx, ref, testBucket, "dst", "0123456789abcdef0123456789abcdef", 1, object.CopyOptions{})
		expectCode(t, err, s3err.NoSuchUpload)
		info, err := l.CompleteMultipartUpload(ctx, testBucket, "dst", id, []object.CompletePart{
			{PartNumber: 1, ETag: p1.ETag}, {PartNumber: 2, ETag: p2.ETag},
		}, object.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if info.Size != 5*mib+4 {
			t.Fatalf("size = %d", info.Size)
		}
		want := append(bytes.Clone(src[:5*mib]), []byte("tail")...)
		if got := mustRead(t, l, "dst", object.GetOptions{}); !bytes.Equal(got, want) {
			t.Fatal("completed copy differs")
		}
	})

	t.Run("composite checksum", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		p1, p2 := payload(5*mib, 18), []byte("last")
		id, err := l.NewMultipartUpload(ctx, testBucket, "k", object.PutOptions{ChecksumAlgorithm: object.ChecksumCRC32C})
		if err != nil {
			t.Fatal(err)
		}
		i1, err := l.PutObjectPart(ctx, testBucket, "k", id, 1, NewBody(p1, WithChecksum(object.ChecksumCRC32C)))
		if err != nil {
			t.Fatal(err)
		}
		i2, err := l.PutObjectPart(ctx, testBucket, "k", id, 2, NewBody(p2, WithChecksum(object.ChecksumCRC32C)))
		if err != nil {
			t.Fatal(err)
		}
		info, err := l.CompleteMultipartUpload(ctx, testBucket, "k", id, []object.CompletePart{
			{PartNumber: 1, ETag: i1.ETag}, {PartNumber: 2, ETag: i2.ETag},
		}, object.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		h, err := digest.New(object.ChecksumCRC32C)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(i1.Checksum.Value)
		h.Write(i2.Checksum.Value)
		want := object.Checksum{Algorithm: object.ChecksumCRC32C, Type: object.ChecksumComposite, Value: h.Sum(nil), Parts: 2}
		if info.Checksum.Algorithm != want.Algorithm || info.Checksum.Type != want.Type ||
			info.Checksum.Parts != 2 || !bytes.Equal(info.Checksum.Value, want.Value) {
			t.Fatalf("checksum = %+v, want %+v", info.Checksum, want)
		}
	})

	t.Run("full object checksum for CRC64NVME", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		p1, p2 := payload(5*mib, 19), []byte("tail bytes")
		id, err := l.NewMultipartUpload(ctx, testBucket, "k", object.PutOptions{ChecksumAlgorithm: object.ChecksumCRC64NVME})
		if err != nil {
			t.Fatal(err)
		}
		i1, err := l.PutObjectPart(ctx, testBucket, "k", id, 1, NewBody(p1, WithChecksum(object.ChecksumCRC64NVME)))
		if err != nil {
			t.Fatal(err)
		}
		i2, err := l.PutObjectPart(ctx, testBucket, "k", id, 2, NewBody(p2, WithChecksum(object.ChecksumCRC64NVME)))
		if err != nil {
			t.Fatal(err)
		}
		info, err := l.CompleteMultipartUpload(ctx, testBucket, "k", id, []object.CompletePart{
			{PartNumber: 1, ETag: i1.ETag}, {PartNumber: 2, ETag: i2.ETag},
		}, object.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		// S3 supports only the full-object type for CRC-64/NVME in multipart
		// uploads, and defines it over the whole object.
		h, err := digest.New(object.ChecksumCRC64NVME)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(p1)
		h.Write(p2)
		want := h.Sum(nil)
		if info.Checksum.Type != object.ChecksumFullObject || info.Checksum.Algorithm != object.ChecksumCRC64NVME ||
			info.Checksum.Parts != 0 || !bytes.Equal(info.Checksum.Value, want) {
			t.Fatalf("checksum = %+v, want full-object CRC64NVME %x", info.Checksum, want)
		}
	})

	t.Run("part without the upload checksum", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		id, err := l.NewMultipartUpload(ctx, testBucket, "k", object.PutOptions{ChecksumAlgorithm: object.ChecksumCRC32C})
		if err != nil {
			t.Fatal(err)
		}
		i1, err := l.PutObjectPart(ctx, testBucket, "k", id, 1, bodyOf([]byte("no checksum")))
		if err != nil {
			t.Fatal(err)
		}
		_, err = l.CompleteMultipartUpload(ctx, testBucket, "k", id, []object.CompletePart{{PartNumber: 1, ETag: i1.ETag}}, object.PutOptions{})
		expectCode(t, err, s3err.InvalidPart)
	})

	t.Run("upload is bound to its key", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		id, parts := uploadWith(t, l, "k", []byte("x"))
		_, err := l.CompleteMultipartUpload(ctx, testBucket, "other", id, parts, object.PutOptions{})
		expectCode(t, err, s3err.NoSuchUpload)
		_, err = l.CompleteMultipartUpload(ctx, testBucket, "k", "0123456789abcdef0123456789abcdef", parts, object.PutOptions{})
		expectCode(t, err, s3err.NoSuchUpload)
	})
}
