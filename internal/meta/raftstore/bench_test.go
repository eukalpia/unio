package raftstore_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
)

// benchBucket is the bucket used by the benchmarks.
const benchBucket = "bench-bucket"

// benchListKeys is the number of objects in the listing benchmark.
const benchListKeys = 100_000

// benchPopulateWorkers bounds the concurrent writes used to populate the
// listing benchmark. Concurrent proposals let raft batch log entries.
const benchPopulateWorkers = 64

// benchObject returns a small inline object for key.
func benchObject(key string) meta.ObjectRecord {
	value := "benchmark payload " + key
	return meta.ObjectRecord{
		Bucket:  benchBucket,
		Key:     key,
		Size:    int64(len(value)),
		ETag:    value,
		ModTime: time.Now(),
		Inline:  []byte(value),
	}
}

// BenchmarkPutObject measures the latency of one linearizable PutObject on a
// single-node store. Each iteration writes a new key, so the cost includes the
// log append, its fsync, the apply and the response.
func BenchmarkPutObject(b *testing.B) {
	c := openTestCluster(b, 1)
	s := c.store()
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, benchBucket, object.MakeBucketOptions{Owner: "bench"}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.PutObject(ctx, benchObject(fmt.Sprintf("put-%d", i)), object.Conditions{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetObject measures the latency of one linearizable GetObject on a
// single-node store. The read is served by the leader after its leadership
// check and a barrier when needed.
func BenchmarkGetObject(b *testing.B) {
	c := openTestCluster(b, 1)
	s := c.store()
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, benchBucket, object.MakeBucketOptions{Owner: "bench"}); err != nil {
		b.Fatal(err)
	}
	if _, err := s.PutObject(ctx, benchObject("get-me"), object.Conditions{}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.GetObject(ctx, benchBucket, "get-me", ""); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListObjectsPage measures the latency of one page of 1000 keys with
// a delimiter over a bucket of 100k objects spread over 100 directories. The
// bucket is populated once per benchmark invocation, outside the timed loop.
func BenchmarkListObjectsPage(b *testing.B) {
	c := openTestCluster(b, 1)
	s := c.store()
	ctx := context.Background()
	if _, err := s.CreateBucket(ctx, benchBucket, object.MakeBucketOptions{Owner: "bench"}); err != nil {
		b.Fatal(err)
	}
	populate(b, s, benchListKeys)
	opts := object.ListOptions{Prefix: "dir042/", Delimiter: "/", MaxKeys: 1000}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := s.ListObjects(ctx, benchBucket, opts)
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Objects) == 0 {
			b.Fatal("empty page")
		}
	}
}

// populate writes n objects named dir%03d/file%06d with bounded concurrency.
func populate(b *testing.B, s meta.Store, n int) {
	b.Helper()
	ctx := context.Background()
	jobs := make(chan int)
	errs := make(chan error, benchPopulateWorkers)
	var wg sync.WaitGroup
	for w := 0; w < benchPopulateWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				key := fmt.Sprintf("dir%03d/file%06d", i%100, i)
				if _, err := s.PutObject(ctx, benchObject(key), object.Conditions{}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		select {
		case jobs <- i:
		case err := <-errs:
			close(jobs)
			wg.Wait()
			b.Fatal(err)
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errs:
		b.Fatal(err)
	default:
	}
}
