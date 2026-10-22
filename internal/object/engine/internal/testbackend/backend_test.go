package testbackend

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestSetWriteReadRoundTrip(t *testing.T) {
	s := NewSet(1, 2, 1)
	id := ids.NewDataID()
	payload := []byte("hello, erasure set")
	layout, err := s.Write(context.Background(), id, bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if layout.Written() != 3 {
		t.Fatalf("written shards = %d, want 3", layout.Written())
	}
	rc, err := s.Read(context.Background(), id, layout, int64(len(payload)), 7, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "erasur" {
		t.Fatalf("window = %q", got)
	}
}

func TestSetWriteQuorum(t *testing.T) {
	tests := []struct {
		name     string
		failing  []int
		wantErr  bool
		wantCode s3err.Code
	}{
		{"all drives healthy", nil, false, ""},
		{"one drive down keeps quorum", []int{0}, false, ""},
		{"two drives down loses quorum", []int{0, 1}, true, s3err.ServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSet(1, 2, 1)
			for _, i := range tt.failing {
				s.FailDrive(i, true)
			}
			id := ids.NewDataID()
			_, err := s.Write(context.Background(), id, strings.NewReader("abc"), 3)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if !errors.Is(err, erasure.ErrWriteQuorum) || !s3err.Is(err, tt.wantCode) {
					t.Fatalf("err = %v, want write quorum", err)
				}
				if n := len(keysOf(s, id)); n != 0 {
					t.Fatalf("%d shards left behind after failed write", n)
				}
			}
		})
	}
}

func TestSetWriteFailingReaderLeavesNothing(t *testing.T) {
	s := NewSet(1, 2, 1)
	id := ids.NewDataID()
	_, err := s.Write(context.Background(), id, io.LimitReader(strings.NewReader("ab"), 2), 3)
	if err == nil {
		t.Fatal("want error for short payload")
	}
	if n := len(keysOf(s, id)); n != 0 {
		t.Fatalf("%d shards written for failed payload", n)
	}
}

func TestSetReadSkipsCorruptCopy(t *testing.T) {
	s := NewSet(1, 2, 1)
	id := ids.NewDataID()
	layout, err := s.Write(context.Background(), id, strings.NewReader("payload"), 7)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Corrupt(id, 0) {
		t.Fatal("no copy to corrupt")
	}
	rc, err := s.Read(context.Background(), id, layout, 7, 0, 7)
	if err != nil {
		t.Fatalf("read after one corrupt copy: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("got %q", got)
	}
	s.Corrupt(id, 1)
	s.Corrupt(id, 2)
	if _, err := s.Read(context.Background(), id, layout, 7, 0, 7); !errors.Is(err, erasure.ErrCorrupt) {
		t.Fatalf("all copies corrupt: err = %v, want ErrCorrupt", err)
	}
}

func TestSetDeleteRemovesShards(t *testing.T) {
	s := NewSet(1, 2, 1)
	id := ids.NewDataID()
	layout, err := s.Write(context.Background(), id, strings.NewReader("x"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), id, layout); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), id, layout); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := s.Read(context.Background(), id, layout, 1, 0, 1); !errors.Is(err, erasure.ErrReadQuorum) {
		t.Fatalf("read after delete: err = %v", err)
	}
}

func TestRegistryPlacesDeterministically(t *testing.T) {
	r := NewRegistry(3, 2, 1)
	a, err := r.Place(context.Background(), "bkt", "key", 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Place(context.Background(), "bkt", "key", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() != b.ID() {
		t.Fatal("same key placed on different sets")
	}
	if _, err := r.Get(a.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ids.SetID(99)); !errors.Is(err, erasure.ErrUnknownSet) {
		t.Fatalf("unknown set: err = %v", err)
	}
}

func TestStoreVersionedDeleteMarker(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	if _, err := s.CreateBucket(ctx, "bkt", object.MakeBucketOptions{Owner: "u", Versioning: object.VersioningEnabled}); err != nil {
		t.Fatal(err)
	}
	rec := meta.ObjectRecord{Bucket: "bkt", Key: "k", VersionID: "v1", ETag: "e1", Inline: []byte("a")}
	if _, err := s.PutObject(ctx, rec, object.Conditions{}); err != nil {
		t.Fatal(err)
	}
	res, garbage, err := s.DeleteObject(ctx, "bkt", "k", "", object.Conditions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DeleteMarker || res.DeleteMarkerVersionID == "" || len(garbage) != 0 {
		t.Fatalf("delete result = %+v garbage=%d", res, len(garbage))
	}
	latest, err := s.GetObject(ctx, "bkt", "k", "")
	if err != nil || !latest.DeleteMarker {
		t.Fatalf("latest after delete: %+v, %v; want the delete marker", latest, err)
	}
	if _, err := s.GetObject(ctx, "bkt", "k", "nope"); !s3err.Is(err, s3err.NoSuchVersion) {
		t.Fatalf("unknown version: err = %v", err)
	}
	marker, err := s.GetObject(ctx, "bkt", "k", res.DeleteMarkerVersionID)
	if err != nil || !marker.DeleteMarker || marker.Bucket != "bkt" {
		t.Fatalf("get marker: %+v, %v", marker, err)
	}
	if _, err := s.GetObject(ctx, "bkt", "k", "v1"); err != nil {
		t.Fatalf("old version still readable: %v", err)
	}
}

func TestStoreListDelimiterAndPaging(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	if _, err := s.CreateBucket(ctx, "bkt", object.MakeBucketOptions{Owner: "u"}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a/1", "a/2", "b", "c"} {
		rec := meta.ObjectRecord{Bucket: "bkt", Key: k, ETag: k, Inline: []byte{}}
		if _, err := s.PutObject(ctx, rec, object.Conditions{}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.ListObjects(ctx, "bkt", object.ListOptions{Delimiter: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.CommonPrefixes, ","); got != "a/" || len(res.Objects) != 2 {
		t.Fatalf("prefixes=%q objects=%d", got, len(res.Objects))
	}
	page, err := s.ListObjects(ctx, "bkt", object.ListOptions{MaxKeys: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !page.IsTruncated || page.NextAfter != "a/2" || len(page.Objects) != 2 {
		t.Fatalf("page = %+v", page)
	}
}

func keysOf(s *Set, id ids.DataID) []ids.ShardID {
	var out []ids.ShardID
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.drives {
		for k := range d.shards {
			if k.Data == id {
				out = append(out, k)
			}
		}
	}
	return out
}
