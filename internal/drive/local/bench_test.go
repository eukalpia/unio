package local_test

import (
	"bytes"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/eukalpia/unio/internal/drive/local"
	"github.com/eukalpia/unio/internal/ids"
)

// benchDrive formats and opens a drive with default options in a temporary
// directory.
func benchDrive(b *testing.B) *local.Drive {
	b.Helper()
	dir := b.TempDir()
	ctx := context.Background()
	if _, err := local.Format(ctx, dir, "bench-cluster"); err != nil {
		b.Fatalf("format: %v", err)
	}
	d, err := local.Open(ctx, dir, local.Options{ClusterID: "bench-cluster"})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = d.Close() })
	return d
}

// populate writes n small shards with the given number of concurrent writers.
func populate(b *testing.B, d *local.Drive, n int, writers int) {
	b.Helper()
	data := make([]byte, 64)
	var next atomic.Uint64
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := next.Add(1)
				if i > uint64(n) {
					return
				}
				if err := d.WriteShard(context.Background(), benchID(i), bytes.NewReader(data), int64(len(data))); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		b.Fatalf("populate: %v", err)
	}
}

// benchID returns a shard id unique for n.
func benchID(n uint64) ids.ShardID {
	var id ids.ShardID
	for i := 0; i < 8; i++ {
		id.Data[i] = byte(n >> (8 * i))
	}
	id.Data[15] = 0xb0
	return id
}

// BenchmarkPut4KiBConcurrent measures 4 KiB packed writes from 64 concurrent
// writers. With SyncAlways, the throughput depends on group commit sharing the
// segment fdatasync and the index commit.
func BenchmarkPut4KiBConcurrent(b *testing.B) {
	d := benchDrive(b)
	const writers = 64
	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i)
	}
	var next atomic.Uint64
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n := next.Add(1)
				if n > uint64(b.N) {
					return
				}
				err := d.WriteShard(context.Background(), benchID(n), bytes.NewReader(data), int64(len(data)))
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	b.StopTimer()
	close(errs)
	for err := range errs {
		b.Fatalf("write: %v", err)
	}
}

// blobBenchSize is the shard size for the blob benchmarks.
const blobBenchSize = 8 << 20

// BenchmarkPutBlob measures writes of 8 MiB blob shards.
func BenchmarkPutBlob(b *testing.B) {
	d := benchDrive(b)
	data := bytes.Repeat([]byte("unio-blob-"), blobBenchSize/10+1)[:blobBenchSize]
	b.SetBytes(blobBenchSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := benchID(uint64(i + 1))
		if err := d.WriteShard(context.Background(), id, bytes.NewReader(data), int64(len(data))); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
}

// BenchmarkGetBlob measures full reads of 8 MiB blob shards.
func BenchmarkGetBlob(b *testing.B) {
	d := benchDrive(b)
	data := bytes.Repeat([]byte("unio-blob-"), blobBenchSize/10+1)[:blobBenchSize]
	id := benchID(1)
	if err := d.WriteShard(context.Background(), id, bytes.NewReader(data), int64(len(data))); err != nil {
		b.Fatalf("write: %v", err)
	}
	b.SetBytes(blobBenchSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rc, err := d.ReadShard(context.Background(), id, 0, -1)
		if err != nil {
			b.Fatalf("read: %v", err)
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			b.Fatalf("copy: %v", err)
		}
		_ = rc.Close()
	}
}

// BenchmarkStatShard measures index lookups over a populated drive. The
// population is written by concurrent writers so that group commit shares the
// syncs; written one at a time, each write would wait for its own fsync.
func BenchmarkStatShard(b *testing.B) {
	d := benchDrive(b)
	const population = 10000
	populate(b, d, population, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.StatShard(context.Background(), benchID(uint64(i%population+1))); err != nil {
			b.Fatalf("stat: %v", err)
		}
	}
}
