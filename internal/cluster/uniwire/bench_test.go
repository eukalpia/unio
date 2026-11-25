package uniwire

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// BenchmarkRPCRoundTrip measures one small RPC over loopback mTLS.
func BenchmarkRPCRoundTrip(b *testing.B) {
	n := newTestNet(b)
	a := n.node("a", Limits{})
	c := n.dialOnly("b", Limits{})
	if err := a.Register("bench.Echo", echoHandler); err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	req := &uniov1.DriveRequest{Drive: make([]byte, 64)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var resp uniov1.DriveRequest
		if err := c.Call(ctx, "a", "bench.Echo", req, &resp); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRPCRoundTripParallel measures RPC throughput with concurrent
// callers multiplexed over the pooled connections.
func BenchmarkRPCRoundTripParallel(b *testing.B) {
	n := newTestNet(b)
	a := n.node("a", Limits{})
	c := n.dialOnly("b", Limits{})
	if err := a.Register("bench.Echo", echoHandler); err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	req := &uniov1.DriveRequest{Drive: make([]byte, 64)}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var resp uniov1.DriveRequest
			if err := c.Call(ctx, "a", "bench.Echo", req, &resp); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

// BenchmarkStreamUpload measures upload throughput of a shard-sized body over
// loopback mTLS. Results are reported in bytes per second.
func BenchmarkStreamUpload(b *testing.B) {
	for _, size := range []int{1 << 20, 16 << 20} {
		b.Run(byteLabel(size), func(b *testing.B) {
			n := newTestNet(b)
			a := n.node("a", Limits{})
			c := n.dialOnly("b", Limits{})
			if err := a.RegisterStream("bench.Upload", func(_ context.Context, req *StreamRequest) (*StreamResponse, error) {
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					return nil, err
				}
				return &StreamResponse{}, nil
			}); err != nil {
				b.Fatal(err)
			}
			data := make([]byte, size)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := c.Stream(ctx, "a", "bench.Upload", nil, bytes.NewReader(data), int64(size)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkStreamDownload measures download throughput of a shard-sized body.
func BenchmarkStreamDownload(b *testing.B) {
	for _, size := range []int{1 << 20, 16 << 20} {
		b.Run(byteLabel(size), func(b *testing.B) {
			n := newTestNet(b)
			a := n.node("a", Limits{})
			c := n.dialOnly("b", Limits{})
			data := make([]byte, size)
			if err := a.RegisterStream("bench.Download", func(context.Context, *StreamRequest) (*StreamResponse, error) {
				return &StreamResponse{Body: io.NopCloser(bytes.NewReader(data)), BodyLen: int64(size)}, nil
			}); err != nil {
				b.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			buf := make([]byte, 256<<10)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				rep, err := c.Stream(ctx, "a", "bench.Download", nil, nil, 0)
				if err != nil {
					b.Fatal(err)
				}
				for {
					_, err := rep.Body.Read(buf)
					if err == io.EOF {
						break
					}
					if err != nil {
						b.Fatal(err)
					}
				}
				_ = rep.Body.Close()
			}
		})
	}
}

// byteLabel names a benchmark sub-case by its size.
func byteLabel(n int) string {
	return fmt.Sprintf("%dMiB", n>>20)
}
