package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/eukalpia/unio/internal/config"
)

// Test credentials. They satisfy the root-credential rules and exist only in
// tests.
const (
	testSecret       = "cluster-secret-for-server-tests-0123456789abcdef"
	testRootUser     = "UNIOSERVERTEST01"
	testRootPassword = "Srv-tests-root-key-0123456789-abcdefghXYZ"
)

// testNode describes one node of a test cluster.
type testNode struct {
	host     string // endpoint host, also the node id
	s3Port   int
	wirePort int
	dirs     []string // drive directories
}

// newTestNode picks free ports on host and creates drives drive directories.
func newTestNode(t testing.TB, host string, drives int) *testNode {
	t.Helper()
	n := &testNode{host: host, s3Port: freePort(t, host), wirePort: freePort(t, host)}
	base := t.TempDir()
	for i := 1; i <= drives; i++ {
		n.dirs = append(n.dirs, filepath.Join(base, "d"+strconv.Itoa(i)))
	}
	return n
}

// endpoints returns the drive endpoints of every node, in node order.
func endpoints(nodes []*testNode) string {
	var eps []string
	for _, n := range nodes {
		for _, d := range n.dirs {
			eps = append(eps, "https://"+net.JoinHostPort(n.host, strconv.Itoa(n.wirePort))+d)
		}
	}
	return strings.Join(eps, ",")
}

// loadConfig returns the configuration of self in the cluster of nodes,
// through config.Load as the CLI would build it. extra overrides variables.
func loadConfig(t testing.TB, self *testNode, nodes []*testNode, extra map[string]string) *config.Config {
	t.Helper()
	env := map[string]string{
		"UNIO_NODE_ID":             self.host,
		"UNIO_S3_ADDRESS":          net.JoinHostPort(self.host, strconv.Itoa(self.s3Port)),
		"UNIO_INTERNODE_ADDRESS":   net.JoinHostPort(self.host, strconv.Itoa(self.wirePort)),
		"UNIO_ENDPOINTS":           endpoints(nodes),
		"UNIO_METADATA_PARTITIONS": "4",
		"UNIO_MEMORY_BUDGET":       "128MiB",
		"UNIO_SHUTDOWN_TIMEOUT":    "10s",
		"UNIO_CLUSTER_SECRET":      testSecret,
		"UNIO_ROOT_USER":           testRootUser,
		"UNIO_ROOT_PASSWORD":       testRootPassword,
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, _, err := config.Load(context.Background(), config.Options{Env: env})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

// running is a node serving in the background.
type running struct {
	node   *Node
	cancel context.CancelFunc
	done   chan struct{} // closed when Serve has returned
	err    error         // Serve's result, valid once done is closed
}

// stop stops the node and waits for Serve to return.
func (r *running) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	select {
	case <-r.done:
		if r.err != nil {
			t.Errorf("serve: %v", r.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("node did not stop within 30s")
	}
}

// serve starts a node with cfg and serves it until the test ends or stop is
// called.
func serve(t testing.TB, cfg *config.Config) *running {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	n, err := Start(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("start %s: %v", cfg.Node.ID, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{node: n, cancel: cancel, done: make(chan struct{})}
	go func() {
		r.err = n.Serve(ctx)
		close(r.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(30 * time.Second):
		}
	})
	return r
}

// waitReady polls the readiness endpoint of the node at addr until it
// reports ready.
func waitReady(t testing.TB, addr string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/unio/health/ready")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("node at %s not ready after 60s (last error %v)", addr, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// client returns an S3 client for the node at addr.
func client(addr string) *s3.Client {
	return s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(testRootUser, testRootPassword, ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://" + addr)
		o.UsePathStyle = true
	})
}

// freePort returns a port on host that was free a moment ago.
func freePort(t testing.TB, host string) int {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", host, err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// listenOn occupies addr.
func listenOn(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// discardLogger returns a logger that drops everything.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testWriter sends log output to the test log.
type testWriter struct{ t testing.TB }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
