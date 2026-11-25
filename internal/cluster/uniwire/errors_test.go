package uniwire

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/meta"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// roundTrip sends err through the wire representation and back.
func roundTrip(err error) error {
	return decodeError(encodeError(err))
}

func TestSentinelsSurviveTheWire(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		sentinel error
		code     s3err.Code
	}{
		{name: "drive not found", err: drive.ErrShardNotFound, sentinel: drive.ErrShardNotFound, code: s3err.InternalError},
		{name: "wrapped sentinel", err: fmt.Errorf("read: %w", meta.ErrNotLeader), sentinel: meta.ErrNotLeader, code: s3err.ServiceUnavailable},
		{name: "revision mismatch", err: meta.ErrRevisionMismatch, sentinel: meta.ErrRevisionMismatch, code: s3err.ConditionalRequestConflict},
		{name: "offline", err: drive.ErrOffline, sentinel: drive.ErrOffline, code: s3err.ServiceUnavailable},
		{name: "deadline", err: context.DeadlineExceeded, sentinel: context.DeadlineExceeded, code: s3err.RequestTimeout},
		{name: "canceled", err: context.Canceled, sentinel: context.Canceled, code: s3err.RequestTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(tc.err)
			if !errors.Is(got, tc.sentinel) {
				t.Fatalf("errors.Is(%v, sentinel) = false", got)
			}
			if c := s3err.CodeOf(got); c != tc.code {
				t.Fatalf("code = %s, want %s", c, tc.code)
			}
		})
	}
}

func TestOutermostSentinelWins(t *testing.T) {
	inner := fmt.Errorf("shard 3: %w", drive.ErrOffline)
	outer := fmt.Errorf("write failed: %w: %w", erasure.ErrWriteQuorum, inner)
	got := roundTrip(outer)
	if !errors.Is(got, erasure.ErrWriteQuorum) {
		t.Fatalf("outer sentinel lost: %v", got)
	}
	if s3err.CodeOf(got) != s3err.ServiceUnavailable {
		t.Fatalf("code = %s", s3err.CodeOf(got))
	}
}

func TestS3ErrorKeepsCodeAndMessageOnly(t *testing.T) {
	secret := errors.New("secret internal detail")
	cases := []struct {
		name    string
		err     error
		code    s3err.Code
		message string
	}{
		{name: "client message", err: s3err.Errorf(s3err.NoSuchKey, "key %q missing", "a"), code: s3err.NoSuchKey, message: `key "a" missing`},
		{name: "wrapped cause hidden", err: s3err.Wrap(s3err.InvalidArgument, secret), code: s3err.InvalidArgument, message: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(tc.err)
			if s3err.CodeOf(got) != tc.code {
				t.Fatalf("code = %s, want %s", s3err.CodeOf(got), tc.code)
			}
			var re *remoteError
			if !errors.As(got, &re) || re.message != tc.message {
				t.Fatalf("message = %q, want %q", re.message, tc.message)
			}
			if strings.Contains(got.Error(), "secret") {
				t.Fatalf("internal cause leaked: %v", got)
			}
		})
	}
}

func TestGenericErrorsBecomeInternal(t *testing.T) {
	got := roundTrip(errors.New("disk on fire at /var/secret"))
	if s3err.CodeOf(got) != s3err.InternalError {
		t.Fatalf("code = %s, want InternalError", s3err.CodeOf(got))
	}
	if strings.Contains(got.Error(), "secret") || strings.Contains(got.Error(), "fire") {
		t.Fatalf("details leaked: %v", got)
	}
}

func TestDecodeUnknownSentinelKeepsCode(t *testing.T) {
	re := decodeError(&uniov1.RPCError{Code: string(s3err.SlowDown), Sentinel: "from.a.newer.build"})
	if s3err.CodeOf(re) != s3err.SlowDown {
		t.Fatalf("code = %s, want SlowDown", s3err.CodeOf(re))
	}
	if errors.Is(re, drive.ErrOffline) || re.(*remoteError).Unwrap() != nil {
		t.Fatal("unknown sentinel name matched a local sentinel")
	}
}

func TestSentinelNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, ns := range sentinels {
		if seen[ns.name] {
			t.Fatalf("duplicate sentinel name %q", ns.name)
		}
		seen[ns.name] = true
		if ns.err == nil {
			t.Fatalf("sentinel %q is nil", ns.name)
		}
	}
}
