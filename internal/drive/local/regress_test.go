package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/compact"
)

// TestBlobDeleteStaysDeletedWhenOlderPackedRecordSurvives covers a shard whose
// packed record is older than its tombstone. The tombstone is dropped by
// compaction while the shard is rewritten as a blob, and the blob is then
// deleted. The deletion must be durable on its own: a rebuild must not bring
// back the packed record that is still in the first segment.
func TestBlobDeleteStaysDeletedWhenOlderPackedRecordSurvives(t *testing.T) {
	dir := newTestDir(t)
	opts := testOptions()
	ctx := context.Background()
	d := openAt(t, dir, opts)

	victim := idOf(1)
	if err := writeShard(d, victim, payload(1000, 1)); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	// Keep the victim's segment mostly live so that it is never compacted.
	for i := 2; i < 70; i++ {
		if err := writeShard(d, idOf(i), payload(1000, i)); err != nil {
			t.Fatalf("filler: %v", err)
		}
	}
	if err := d.DeleteShard(ctx, victim); err != nil {
		t.Fatalf("delete packed victim: %v", err)
	}
	// Fill and empty later segments so that the tombstone's segment is compacted.
	for i := 100; i < 200; i++ {
		if err := writeShard(d, idOf(i), payload(1000, i)); err != nil {
			t.Fatalf("churn: %v", err)
		}
	}
	for i := 100; i < 200; i++ {
		if err := d.DeleteShard(ctx, idOf(i)); err != nil {
			t.Fatalf("churn delete: %v", err)
		}
	}
	if err := writeShard(d, victim, payload(2*testPacked, 9)); err != nil {
		t.Fatalf("rewrite as blob: %v", err)
	}
	if _, err := compact.Pass(ctx, compactTarget{d: d}, compact.Config{}); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if err := d.DeleteShard(ctx, victim); err != nil {
		t.Fatalf("delete blob victim: %v", err)
	}
	closeDrive(t, d)

	if err := removeIndex(dir); err != nil {
		t.Fatal(err)
	}
	d2 := openAt(t, dir, opts)
	if _, err := d2.StatShard(ctx, victim); !errors.Is(err, drive.ErrShardNotFound) {
		t.Fatalf("deleted shard came back after rebuild: %v", err)
	}
}

// TestSameGroupDeleteAndRecreateKeepsNewBlob puts a delete and a re-create of
// one blob id into the same commit group. The re-created blob reuses the path
// of the deleted one, so the deferred unlink of the delete must not remove it.
// Requests are driven through the committer's own staging functions, from the
// test goroutine, while the committer is idle.
func TestSameGroupDeleteAndRecreateKeepsNewBlob(t *testing.T) {
	dir := newTestDir(t)
	d := openAt(t, dir, testOptions())
	ctx := context.Background()
	id := idOf(1)
	if err := writeShard(d, id, payload(2*testPacked, 1)); err != nil {
		t.Fatalf("first write: %v", err)
	}
	newer := payload(2*testPacked, 2)
	tmp, err := d.blobs.NewTemp()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.blobs.Write(ctx, tmp, bytes.NewReader(newer), int64(len(newer))); err != nil {
		t.Fatalf("stage new blob: %v", err)
	}

	del := newRequest(reqDelete, id)
	put := newRequest(reqBlob, id)
	put.tmp = tmp
	put.final = d.blobs.Path(id)
	put.size = int64(len(newer))
	put.created = time.Now().UnixNano()

	g := d.newGroup()
	d.handle(g, del)
	d.handle(g, put)
	if err := g.flush(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := <-del.reply; err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := <-put.reply; err != nil {
		t.Fatalf("re-create: %v", err)
	}
	got, err := readShard(d, id)
	if err != nil || !bytes.Equal(got, newer) {
		t.Fatalf("acknowledged re-created blob is unreadable: %v", err)
	}
}

// TestCloseDoesNotWaitForStalledWriter checks that a client which has not
// finished sending its payload cannot hold up Close, and that the stalled
// write fails with ErrClosed once it resumes.
func TestCloseDoesNotWaitForStalledWriter(t *testing.T) {
	dir := newTestDir(t)
	d := openAt(t, dir, testOptions())
	pr, pw := io.Pipe()
	reader := &signalReader{Reader: pr, started: make(chan struct{})}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- d.WriteShard(context.Background(), idOf(1), reader, 100)
	}()
	// Close only once the writer is reading its payload, so that the writer is
	// the one that is stalled.
	<-reader.started
	closed := make(chan error, 1)
	go func() { closed <- d.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(10 * time.Second):
		_ = pw.CloseWithError(errors.New("test abandoned"))
		t.Fatal("Close blocked by a writer that has not finished reading its payload")
	}
	if _, err := pw.Write(payload(100, 1)); err != nil {
		t.Fatalf("feed payload: %v", err)
	}
	_ = pw.Close()
	if err := <-writeDone; !errors.Is(err, drive.ErrClosed) {
		t.Fatalf("stalled write: err = %v, want ErrClosed", err)
	}
}

// signalReader closes started on its first read, so that a test can wait until
// a writer is blocked on its payload.
type signalReader struct {
	io.Reader
	once    sync.Once
	started chan struct{}
}

func (s *signalReader) Read(p []byte) (int, error) {
	s.once.Do(func() { close(s.started) })
	return s.Reader.Read(p)
}

// removeIndex deletes the index of the drive rooted at dir, forcing a rebuild.
func removeIndex(dir string) error {
	return os.RemoveAll(filepath.Join(dir, ".unio", "index"))
}
