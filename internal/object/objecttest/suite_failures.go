package objecttest

import (
	"bytes"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func suiteFailures(t *testing.T, newLayer func(*testing.T) object.Layer) {
	sizes := []int{100, inlineSize + 1, 5*mib + 3}
	for _, size := range sizes {
		t.Run("body error "+sizeName(size), func(t *testing.T) {
			tests := []struct {
				name string
				body func(size int) *Body
				code s3err.Code
			}{
				{"error before end", func(n int) *Body {
					return NewBody(payload(n, 21), FailAfter(n/2, s3err.Errorf(s3err.BadDigest, "bad digest")))
				}, s3err.BadDigest},
				{"error with the last bytes", func(n int) *Body {
					return NewBody(payload(n, 22), FinalError(s3err.Errorf(s3err.XAmzContentSHA256Mismatch, "sha256")))
				}, s3err.XAmzContentSHA256Mismatch},
				{"payload shorter than declared", func(n int) *Body {
					return NewBody(payload(n-1, 23), Declared(int64(n)))
				}, ""},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					l := newLayer(t)
					ctx := t.Context()
					mustBucket(t, l)
					base := dataCount(t, l)
					_, err := l.PutObject(ctx, testBucket, "k", tt.body(size), object.PutOptions{})
					if err == nil {
						t.Fatal("put with a failing body succeeded")
					}
					if tt.code != "" && !s3err.Is(err, tt.code) {
						t.Fatalf("error = %v, want code %s", err, tt.code)
					}
					_, err = l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
					expectCode(t, err, s3err.NoSuchKey)
					flush(t, l)
					if base >= 0 {
						if got := dataCount(t, l); got != base {
							t.Fatalf("stored data after failed put = %d, want %d (no leaked data)", got, base)
						}
					}
				})
			}
		})
	}

	t.Run("failed overwrite keeps the previous version", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		old := payload(5*mib, 24)
		info := mustPut(t, l, "k", old)
		_, err := l.PutObject(t.Context(), testBucket, "k",
			NewBody(payload(5*mib, 25), FailAfter(mib, s3err.Errorf(s3err.IncompleteBody, "cut"))), object.PutOptions{})
		expectCode(t, err, s3err.IncompleteBody)
		head, err := l.HeadObject(t.Context(), testBucket, "k", object.GetOptions{})
		if err != nil || head.ETag != info.ETag {
			t.Fatalf("previous version changed: %+v, %v", head, err)
		}
		if got := mustRead(t, l, "k", object.GetOptions{}); !bytes.Equal(got, old) {
			t.Fatal("previous version bytes changed")
		}
	})

	t.Run("failing body does not reach the layer's commit", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		_, err := l.PutObject(t.Context(), testBucket, "k",
			NewBody(payload(10, 26), FailAfter(0, errors.New("network reset"))), object.PutOptions{})
		if err == nil {
			t.Fatal("put with a body that fails immediately succeeded")
		}
		_, err = l.HeadObject(t.Context(), testBucket, "k", object.GetOptions{})
		expectCode(t, err, s3err.NoSuchKey)
	})

	t.Run("write quorum lost", func(t *testing.T) {
		l := newLayer(t)
		ctx := t.Context()
		mustBucket(t, l)
		faults, ok := l.(QuorumFaults)
		if !ok {
			t.Skip("layer has no write quorum fault injection")
		}
		old := payload(inlineSize+1, 27)
		mustPut(t, l, "k", old)
		id, err := l.NewMultipartUpload(ctx, testBucket, "multi", object.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		base := dataCount(t, l)
		faults.SetWriteQuorumFailure(true)
		for _, size := range []int{10, 5 * mib} {
			_, err := l.PutObject(ctx, testBucket, "fresh", bodyOf(payload(size, 28)), object.PutOptions{})
			expectCode(t, err, s3err.ServiceUnavailable)
		}
		_, err = l.PutObject(ctx, testBucket, "k", bodyOf(payload(5*mib, 29)), object.PutOptions{})
		expectCode(t, err, s3err.ServiceUnavailable)
		_, err = l.PutObjectPart(ctx, testBucket, "multi", id, 1, bodyOf(payload(5*mib, 30)))
		expectCode(t, err, s3err.ServiceUnavailable)
		faults.SetWriteQuorumFailure(false)

		_, err = l.HeadObject(ctx, testBucket, "fresh", object.GetOptions{})
		expectCode(t, err, s3err.NoSuchKey)
		if got := mustRead(t, l, "k", object.GetOptions{}); !bytes.Equal(got, old) {
			t.Fatal("object changed while the write quorum was lost")
		}
		flush(t, l)
		if base >= 0 {
			if got := dataCount(t, l); got != base {
				t.Fatalf("stored data after failed writes = %d, want %d", got, base)
			}
		}
		mustPut(t, l, "fresh", []byte("recovered"))
		if got := mustRead(t, l, "fresh", object.GetOptions{}); string(got) != "recovered" {
			t.Fatal("writes do not recover after the quorum returns")
		}
	})
}
