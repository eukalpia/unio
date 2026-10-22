package server

import (
	"bytes"
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// BenchmarkPut1MiB writes 1 MiB objects to a four-drive node from 32
// concurrent clients, the shape of the put-1m comparison workload. Run it with
// -cpuprofile, -blockprofile or -trace to see where a write spends its time.
func BenchmarkPut1MiB(b *testing.B) {
	benchmarkPut(b, 1<<20, 32)
}

// benchmarkPut writes b.N objects of size bytes from clients goroutines.
func benchmarkPut(b *testing.B, size, clients int) {
	n := newTestNode(b, "127.0.0.1", 4)
	r := serve(b, loadConfig(b, n, []*testNode{n}, map[string]string{"UNIO_MEMORY_BUDGET": "1GiB"}))
	waitReady(b, r.node.S3Addr().String())
	putLoad(b, client(r.node.S3Addr().String()), size, clients)
}

// putLoad writes b.N objects of size bytes through c from clients goroutines.
func putLoad(b *testing.B, c *s3.Client, size, clients int) {
	ctx := context.Background()
	if _, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("bench")}); err != nil {
		b.Fatal(err)
	}
	data := bytes.Repeat([]byte{0x5a}, size)
	var next atomic.Int64
	b.SetBytes(int64(size))
	b.ResetTimer()
	var wg sync.WaitGroup
	for range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := next.Add(1)
				if i > int64(b.N) {
					return
				}
				if _, err := c.PutObject(ctx, &s3.PutObjectInput{
					Bucket: aws.String("bench"), Key: aws.String("o" + strconv.FormatInt(i, 10)),
					Body: bytes.NewReader(data),
				}); err != nil {
					b.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// BenchmarkPut1MiBCluster is BenchmarkPut1MiB on four nodes of four drives,
// so shards travel to remote drives over UniWire as in a real cluster.
func BenchmarkPut1MiBCluster(b *testing.B) {
	nodes := []*testNode{
		newTestNode(b, "127.0.0.1", 4),
		newTestNode(b, "127.0.0.2", 4),
		newTestNode(b, "127.0.0.3", 4),
		newTestNode(b, "127.0.0.4", 4),
	}
	var rs []*running
	for _, n := range nodes {
		rs = append(rs, serve(b, loadConfig(b, n, nodes, map[string]string{"UNIO_MEMORY_BUDGET": "1GiB"})))
	}
	for _, r := range rs {
		waitReady(b, r.node.S3Addr().String())
	}
	putLoad(b, client(rs[0].node.S3Addr().String()), 1<<20, 32)
}
