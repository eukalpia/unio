package local

import (
	"testing"

	"github.com/eukalpia/unio/internal/drive/local/blob"
	"github.com/eukalpia/unio/internal/ids"
)

// layoutOf returns the layout of the drive at dir, failing the test if the path
// is invalid.
func layoutOf(t testing.TB, dir string) layout {
	t.Helper()
	lay, err := newLayout(dir)
	if err != nil {
		t.Fatalf("layout of %s: %v", dir, err)
	}
	return lay
}

// segmentFile returns the file of segment id in lay.
func segmentFile(t testing.TB, lay layout, id uint64) string {
	t.Helper()
	p, err := lay.segmentPath(id)
	if err != nil {
		t.Fatalf("segment %d path: %v", id, err)
	}
	return p
}

// blobFile returns the final path of the blob for id in s.
func blobFile(t testing.TB, s *blob.Store, id ids.ShardID) string {
	t.Helper()
	p, err := s.Path(id)
	if err != nil {
		t.Fatalf("blob path of %s: %v", id, err)
	}
	return p
}
