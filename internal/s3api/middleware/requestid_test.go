package middleware

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

// errEntropy is returned by a source that cannot supply random bytes.
var errEntropy = errors.New("entropy source exhausted")

// failingSource is an io.Reader that never produces bytes.
type failingSource struct{}

func (failingSource) Read([]byte) (int, error) { return 0, errEntropy }

func TestNewRequestIDFormatAndUniqueness(t *testing.T) {
	const n = 10000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id, err := newRequestID(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 16 || strings.Trim(id, "0123456789ABCDEF") != "" {
			t.Fatalf("id %q is not 16 upper-case hexadecimal characters", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q in %d draws from crypto/rand", id, n)
		}
		seen[id] = struct{}{}
	}
}

func TestNewRequestIDIsTheRandomSource(t *testing.T) {
	src := bytes.NewReader([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef})
	id, err := newRequestID(src)
	if err != nil {
		t.Fatal(err)
	}
	if id != "0123456789ABCDEF" {
		t.Fatalf("id = %q, want the source bytes rendered as hexadecimal", id)
	}
}

func TestNewRequestIDReportsSourceFailure(t *testing.T) {
	if _, err := newRequestID(failingSource{}); !errors.Is(err, errEntropy) {
		t.Fatalf("err = %v, want the source error", err)
	}
	if _, err := newRequestID(bytes.NewReader([]byte{1, 2, 3})); err == nil {
		t.Fatal("a short source must fail, not produce a shorter id")
	}
}

// TestRequestIDFailsClosed checks that a request cannot proceed without an id:
// when entropy is unavailable the client gets 500 InternalError and the handler
// never runs.
func TestRequestIDFailsClosed(t *testing.T) {
	ran := false
	h := server.New()
	h.Use(requestIDMiddleware("node-1", discard, failingSource{}))
	h.GET("/x", func(ctx context.Context, c *app.RequestContext) {
		ran = true
		c.String(http.StatusOK, "ok")
	})
	w := ut.PerformRequest(h.Engine, "GET", "/x", nil)
	if w.Result().StatusCode() != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "InternalError") {
		t.Fatalf("status %d body %s", w.Result().StatusCode(), w.Body.String())
	}
	if ran {
		t.Fatal("handler ran without a request id")
	}
}

// BenchmarkNewRequestID measures the cost of one request id drawn from
// crypto/rand. It is the only per-request cost the identifier adds.
func BenchmarkNewRequestID(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := newRequestID(rand.Reader); err != nil {
			b.Fatal(err)
		}
	}
}
