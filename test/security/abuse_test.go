package security

import (
	"crypto/md5"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func md5Base64(b []byte) string {
	s := md5.Sum(b)
	return base64.StdEncoding.EncodeToString(s[:])
}

// expectClosedWithin reads from conn until the server closes it and fails
// if that takes longer than limit.
func expectClosedWithin(t *testing.T, conn net.Conn, limit time.Duration) {
	t.Helper()
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(limit))
	_, err := io.Copy(io.Discard, conn)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("server kept the connection open for more than %v", limit)
	}
	t.Logf("server closed the connection after %v", time.Since(start).Round(time.Millisecond))
}

func skipShort(t *testing.T) {
	t.Helper()
	if testing.Short() || os.Getenv("UNIO_TEST_SLOW") == "" {
		t.Skip("slow abuse test; set UNIO_TEST_SLOW=1 and run without -short")
	}
}

// TestSlowHeadersAreCutOff sends request headers one byte at a time,
// slower than any legitimate client.
func TestSlowHeadersAreCutOff(t *testing.T) {
	tg := loadTarget(t)
	skipShort(t)
	conn := tg.dial(t)
	defer conn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		line := "GET / HTTP/1.1\r\nHost: " + tg.endpoint.Host + "\r\nX-Slow: "
		for i := 0; i < len(line); i++ {
			if _, err := conn.Write([]byte{line[i]}); err != nil {
				return
			}
		}
		for {
			if _, err := conn.Write([]byte("a")); err != nil {
				return
			}
			time.Sleep(time.Second)
		}
	}()
	expectClosedWithin(t, conn, 2*tg.headerTimeout)
	conn.Close()
	<-done
	tg.mustAlive(t)
}

// TestStalledBodyIsCutOff sends headers announcing a body and then stalls.
func TestStalledBodyIsCutOff(t *testing.T) {
	tg := loadTarget(t)
	skipShort(t)
	bucket := tg.newBucket(t)
	req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "stalled", ""), make([]byte, 1<<20))
	tg.sign(t, req, nil, tg.goodCreds(), time.Now(), "UNSIGNED-PAYLOAD")
	conn := tg.dial(t)
	defer conn.Close()
	var head strings.Builder
	head.WriteString("PUT " + req.URL.RequestURI() + " HTTP/1.1\r\nHost: " + req.Host + "\r\n")
	for k, vs := range req.Header {
		for _, v := range vs {
			head.WriteString(k + ": " + v + "\r\n")
		}
	}
	head.WriteString("Content-Length: 1048576\r\n\r\n")
	if _, err := conn.Write([]byte(head.String() + "partial")); err != nil {
		t.Fatal(err)
	}
	expectClosedWithin(t, conn, tg.bodyIdleTimeout+30*time.Second)
	if tg.objectExists(t, bucket, "stalled") {
		t.Fatal("a stalled upload became visible")
	}
}

// TestConnectionFloodDegradesGracefully opens many idle connections and
// checks that the server keeps serving real requests.
func TestConnectionFloodDegradesGracefully(t *testing.T) {
	tg := loadTarget(t)
	skipShort(t)
	const n = 2000
	var mu sync.Mutex
	var conns []net.Conn
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", tg.endpoint.Host, 5*time.Second)
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}()
	}
	wg.Wait()
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	t.Logf("opened %d idle connections", len(conns))
	tg.mustAlive(t)
}
