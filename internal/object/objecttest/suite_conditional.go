package objecttest

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func suiteConditional(t *testing.T, newLayer func(*testing.T) object.Layer) {
	t.Run("reads", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		info := mustPut(t, l, "k", []byte("conditional"))
		mod := info.ModTime
		tests := []struct {
			name string
			cond object.Conditions
			want s3err.Code
		}{
			{"if-match matching", object.Conditions{IfMatch: info.ETag}, ""},
			{"if-match wildcard", object.Conditions{IfMatch: "*"}, ""},
			{"if-match other", object.Conditions{IfMatch: "0123"}, s3err.PreconditionFailed},
			{"if-none-match other", object.Conditions{IfNoneMatch: "0123"}, ""},
			{"if-none-match matching", object.Conditions{IfNoneMatch: info.ETag}, s3err.NotModified},
			{"if-none-match wildcard", object.Conditions{IfNoneMatch: "*"}, s3err.NotModified},
			{"if-unmodified-since earlier", object.Conditions{IfUnmodifiedSince: mod.Add(-time.Hour)}, s3err.PreconditionFailed},
			{"if-unmodified-since later", object.Conditions{IfUnmodifiedSince: mod.Add(time.Hour)}, ""},
			{"if-modified-since later", object.Conditions{IfModifiedSince: mod.Add(time.Hour)}, s3err.NotModified},
			{"if-modified-since earlier", object.Conditions{IfModifiedSince: mod.Add(-time.Hour)}, ""},
			{"if-match wins over if-unmodified-since", object.Conditions{IfMatch: info.ETag, IfUnmodifiedSince: mod.Add(-time.Hour)}, ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				opts := object.GetOptions{Conditions: tt.cond}
				r, err := l.GetObject(t.Context(), testBucket, "k", opts)
				if err == nil {
					r.Close()
				}
				expectCode(t, err, tt.want)
				_, err = l.HeadObject(t.Context(), testBucket, "k", opts)
				expectCode(t, err, tt.want)
			})
		}
	})

	t.Run("writes", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		ctx := t.Context()
		put := func(data string, cond object.Conditions) error {
			_, err := l.PutObject(ctx, testBucket, "k", bodyOf([]byte(data)), object.PutOptions{Conditions: cond})
			return err
		}
		expectCode(t, put("first", object.Conditions{IfNoneMatch: "*"}), "")
		expectCode(t, put("second", object.Conditions{IfNoneMatch: "*"}), s3err.PreconditionFailed)
		if got := mustRead(t, l, "k", object.GetOptions{}); string(got) != "first" {
			t.Fatalf("if-none-match write replaced the object: %q", got)
		}
		info, err := l.HeadObject(ctx, testBucket, "k", object.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		expectCode(t, put("third", object.Conditions{IfMatch: "0123"}), s3err.PreconditionFailed)
		expectCode(t, put("third", object.Conditions{IfMatch: info.ETag}), "")
		expectCode(t, put("fourth", object.Conditions{IfMatch: "*"}), "")
		expectCode(t, put("x", object.Conditions{IfMatch: "*"}), "")
		if got := mustRead(t, l, "k", object.GetOptions{}); string(got) != "x" {
			t.Fatalf("object = %q", got)
		}
		expectCode(t, l.DeleteBucket(ctx, testBucket, object.DeleteBucketOptions{Force: true}), "")
		mustBucket(t, l)
		expectCode(t, put("y", object.Conditions{IfMatch: "*"}), s3err.NoSuchKey)
	})

	for _, size := range []int{64, inlineSize + 1} {
		t.Run("concurrent if-none-match "+sizeName(size), func(t *testing.T) {
			l := newLayer(t)
			mustBucket(t, l)
			const writers = 20
			var wg sync.WaitGroup
			errs := make(chan error, writers)
			data := make([][]byte, writers)
			for i := range data {
				data[i] = payload(size, uint64(100+i))
			}
			for i := 0; i < writers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, err := l.PutObject(t.Context(), testBucket, "race", bodyOf(data[i]),
						object.PutOptions{Conditions: object.Conditions{IfNoneMatch: "*"}})
					errs <- err
				}(i)
			}
			wg.Wait()
			close(errs)
			wins, losses := 0, 0
			for err := range errs {
				switch {
				case err == nil:
					wins++
				case s3err.Is(err, s3err.PreconditionFailed):
					losses++
				default:
					t.Fatalf("unexpected error: %v", err)
				}
			}
			if wins != 1 || losses != writers-1 {
				t.Fatalf("winners = %d, losers = %d; want exactly one winner", wins, losses)
			}
			got := mustRead(t, l, "race", object.GetOptions{})
			matched := false
			for _, d := range data {
				if string(d) == string(got) {
					matched = true
				}
			}
			if !matched {
				t.Fatal("stored object is not one of the written payloads")
			}
		})
	}
}

func suiteRanges(t *testing.T, newLayer func(*testing.T) object.Layer) {
	sizes := []struct {
		name string
		size int
	}{
		{"inline", 100},
		{"erasure", inlineSize + 1},
	}
	for _, sz := range sizes {
		t.Run(sz.name, func(t *testing.T) {
			l := newLayer(t)
			mustBucket(t, l)
			data := payload(sz.size, 42)
			mustPut(t, l, "k", data)
			n := int64(sz.size)
			tests := []struct {
				name       string
				rng        object.HTTPRange
				wantOffset int64
				wantLength int64
				wantCode   s3err.Code
			}{
				{"first bytes", object.HTTPRange{Start: 0, End: 9}, 0, 10, ""},
				{"open ended", object.HTTPRange{Start: 5, End: -1}, 5, n - 5, ""},
				{"suffix", object.HTTPRange{Suffix: 5}, n - 5, 5, ""},
				{"suffix larger than object", object.HTTPRange{Suffix: n + 10}, 0, n, ""},
				{"end past object", object.HTTPRange{Start: n - 3, End: n + 100}, n - 3, 3, ""},
				{"single last byte", object.HTTPRange{Start: n - 1, End: n - 1}, n - 1, 1, ""},
				{"start at size", object.HTTPRange{Start: n, End: -1}, 0, 0, s3err.InvalidRange},
				{"start beyond size", object.HTTPRange{Start: n + 5, End: n + 9}, 0, 0, s3err.InvalidRange},
				{"end before start", object.HTTPRange{Start: 9, End: 4}, 0, 0, s3err.InvalidRange},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					rng := tt.rng
					r, err := l.GetObject(t.Context(), testBucket, "k", object.GetOptions{Range: &rng})
					if tt.wantCode != "" {
						expectCode(t, err, tt.wantCode)
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					defer r.Close()
					if r.Offset != tt.wantOffset || r.Length != tt.wantLength {
						t.Fatalf("window = [%d,+%d), want [%d,+%d)", r.Offset, r.Length, tt.wantOffset, tt.wantLength)
					}
					got := readAllBytes(t, r)
					want := data[tt.wantOffset : tt.wantOffset+tt.wantLength]
					if string(got) != string(want) {
						t.Fatalf("range bytes differ")
					}
				})
			}
		})
	}

	t.Run("empty object", func(t *testing.T) {
		l := newLayer(t)
		mustBucket(t, l)
		mustPut(t, l, "empty", nil)
		_, err := l.GetObject(t.Context(), testBucket, "empty", object.GetOptions{Range: &object.HTTPRange{Start: 0, End: -1}})
		expectCode(t, err, s3err.InvalidRange)
		if got := mustRead(t, l, "empty", object.GetOptions{}); len(got) != 0 {
			t.Fatalf("empty object returned %d bytes", len(got))
		}
	})
}

// readAllBytes reads a reader to the end and closes it.
func readAllBytes(t *testing.T, r *object.Reader) []byte {
	t.Helper()
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return got
}
