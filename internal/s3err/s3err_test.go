package s3err

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

var errQuorum = NewSentinel("test: quorum lost", ServiceUnavailable)

func TestCodeOf(t *testing.T) {
	cases := []struct {
		err  error
		want Code
	}{
		{nil, ""},
		{Errorf(NoSuchKey, "x"), NoSuchKey},
		{fmt.Errorf("wrapped: %w", Errorf(AccessDenied, "no")), AccessDenied},
		{fmt.Errorf("w: %w", errQuorum), ServiceUnavailable},
		{context.DeadlineExceeded, RequestTimeout},
		{errors.New("boom"), InternalError},
		// The outermost code wins.
		{Wrap(SlowDown, errQuorum), SlowDown},
	}
	for i, c := range cases {
		if got := CodeOf(c.err); got != c.want {
			t.Fatalf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

func TestErrorsIs(t *testing.T) {
	if !errors.Is(fmt.Errorf("w: %w", Errorf(NoSuchKey, "a")), Errorf(NoSuchKey, "b")) {
		t.Fatal("errors.Is should match *Error by code")
	}
	if !errors.Is(fmt.Errorf("w: %w", errQuorum), errQuorum) {
		t.Fatal("errors.Is should match sentinels by identity")
	}
	if errors.Is(NewSentinel("test: quorum lost", ServiceUnavailable), errQuorum) {
		t.Fatal("distinct sentinels must not match")
	}
}

func TestHTTPStatus(t *testing.T) {
	if NoSuchKey.HTTPStatus() != http.StatusNotFound || Code("Unknown").HTTPStatus() != 500 {
		t.Fatal("bad status mapping")
	}
}

func TestWrapHidesCause(t *testing.T) {
	if e := Wrap(InternalError, errors.New("disk /dev/sda exploded")); e.Message != "" {
		t.Fatal("Wrap must not set a client message")
	}
}
