package fakestore

import (
	"context"
	"errors"
	"testing"

	"github.com/eukalpia/unio/bench/internal/objstore"
)

type pageShape struct {
	keys, commons int
	truncated     bool
}

func seed(t *testing.T, s *Store, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if err := s.PutObject(context.Background(), "b", k, []byte("x")); err != nil {
			t.Fatalf("PutObject(%q): %v", k, err)
		}
	}
}

func TestListObjectsV2Pagination(t *testing.T) {
	keys := []string{"a/1", "a/2", "a/3", "a/4", "a/5", "a/sub/x", "a/sub/y", "b/z"}
	tests := []struct {
		name      string
		delimiter string
		maxKeys   int32
		want      []pageShape
	}{
		{
			name:      "delimiter groups the sub-prefix",
			delimiter: "/",
			maxKeys:   2,
			want: []pageShape{
				{keys: 2, truncated: true},
				{keys: 2, truncated: true},
				{keys: 1, commons: 1},
			},
		},
		{
			name:      "no delimiter lists every key",
			delimiter: "",
			maxKeys:   3,
			want: []pageShape{
				{keys: 3, truncated: true},
				{keys: 3, truncated: true},
				{keys: 1},
			},
		},
		{
			name:      "page covers everything in one request",
			delimiter: "/",
			maxKeys:   10,
			want:      []pageShape{{keys: 5, commons: 1}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			seed(t, s, keys...)
			var token string
			var got []pageShape
			for i := 0; i < 10; i++ {
				page, err := s.ListObjectsV2(context.Background(), "b", objstore.ListRequest{
					Prefix:            "a/",
					Delimiter:         tc.delimiter,
					MaxKeys:           tc.maxKeys,
					ContinuationToken: token,
				})
				if err != nil {
					t.Fatalf("ListObjectsV2: %v", err)
				}
				got = append(got, pageShape{keys: page.Keys, commons: page.CommonPrefixes, truncated: page.Truncated})
				if !page.Truncated {
					break
				}
				if page.NextToken == "" {
					t.Fatal("truncated page without a continuation token")
				}
				token = page.NextToken
			}
			if len(got) != len(tc.want) {
				t.Fatalf("pages = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("page %d = %+v, want %+v (all: %+v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestListRecordsCalls(t *testing.T) {
	s := New()
	seed(t, s, "p/1")
	if _, err := s.ListObjectsV2(context.Background(), "b", objstore.ListRequest{Prefix: "p/"}); err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	lists := s.Lists()
	if len(lists) != 1 || lists[0].Request.Prefix != "p/" || lists[0].Page.Keys != 1 {
		t.Fatalf("recorded lists = %+v", lists)
	}
	if got := s.Count(CallList); got != 1 {
		t.Fatalf("Count(LIST) = %d, want 1", got)
	}
}

func TestFailHookFailsCallAndIsCounted(t *testing.T) {
	boom := errors.New("boom")
	s := New()
	s.Fail = func(call, _ string) error {
		if call == CallPut {
			return boom
		}
		return nil
	}
	err := s.PutObject(context.Background(), "b", "k", []byte("x"))
	if !errors.Is(err, boom) {
		t.Fatalf("PutObject error = %v, want %v", err, boom)
	}
	if _, ok := s.Size("k"); ok {
		t.Fatal("failed PUT stored an object")
	}
	if s.Count(CallPut) != 1 {
		t.Fatalf("Count(PUT) = %d, want 1", s.Count(CallPut))
	}
}

func TestCancelledContextFailsBeforeEffect(t *testing.T) {
	s := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.PutObject(ctx, "b", "k", []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("PutObject on cancelled context = %v, want context.Canceled", err)
	}
	if s.Objects() != 0 {
		t.Fatal("cancelled PUT stored an object")
	}
}

func TestGetMissingKey(t *testing.T) {
	s := New()
	if _, err := s.GetObject(context.Background(), "b", "nope"); err == nil {
		t.Fatal("GetObject of a missing key returned no error")
	}
}
