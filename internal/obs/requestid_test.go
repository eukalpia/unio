package obs

import (
	"context"
	"encoding/hex"
	"net/http"
	"testing"
)

func TestRequestIDHeaderIsCanonical(t *testing.T) {
	t.Parallel()
	if RequestIDHeader != "X-Amz-Request-Id" {
		t.Fatalf("RequestIDHeader = %q", RequestIDHeader)
	}
	if got := http.CanonicalHeaderKey(RequestIDHeader); got != RequestIDHeader {
		t.Fatalf("RequestIDHeader is not canonical: %q", got)
	}
}

func TestRequestIDContext(t *testing.T) {
	t.Parallel()
	var nilCtx context.Context
	tests := []struct {
		name string
		ctx  func() context.Context
		id   string
		want string
	}{
		{
			name: "absent",
			ctx:  context.Background,
			want: "",
		},
		{
			name: "set",
			ctx:  context.Background,
			id:   "req-1",
			want: "req-1",
		},
		{
			name: "overwritten by inner value",
			ctx: func() context.Context {
				return WithRequestID(context.Background(), "outer")
			},
			id:   "inner",
			want: "inner",
		},
		{
			name: "nil context reads as absent",
			ctx:  func() context.Context { return nilCtx },
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := tc.ctx()
			if tc.id != "" {
				ctx = WithRequestID(ctx, tc.id)
			}
			if got := RequestIDFrom(ctx); got != tc.want {
				t.Fatalf("RequestIDFrom = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWithRequestIDNilContext(t *testing.T) {
	t.Parallel()
	var nilCtx context.Context
	ctx := WithRequestID(nilCtx, "req-nil")
	if got := RequestIDFrom(ctx); got != "req-nil" {
		t.Fatalf("RequestIDFrom = %q, want req-nil", got)
	}
}

func TestNewRequestID(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{})
	for range 64 {
		id, err := NewRequestID()
		if err != nil {
			t.Fatalf("NewRequestID: %v", err)
		}
		if len(id) != 32 {
			t.Fatalf("NewRequestID length = %d, want 32", len(id))
		}
		raw, err := hex.DecodeString(id)
		if err != nil {
			t.Fatalf("NewRequestID %q is not hex: %v", id, err)
		}
		if len(raw) != 16 {
			t.Fatalf("decoded length = %d, want 16", len(raw))
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewRequestID returned duplicate %q", id)
		}
		seen[id] = struct{}{}
	}
}
