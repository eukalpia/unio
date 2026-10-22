package fakes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestAuthenticatorIdentity(t *testing.T) {
	tests := []struct {
		name string
		hdr  http.Header
		want auth.Identity
	}{
		{name: "unsigned request is anonymous", hdr: http.Header{}, want: auth.Identity{Anonymous: true}},
		{
			name: "authorization header makes the fake user",
			hdr:  http.Header{"Authorization": {"AWS4-HMAC-SHA256 Credential=anything"}},
			want: auth.Identity{AccessKey: FakeAccessKey, User: FakeUser},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var a Authenticator
			res, err := a.Authenticate(context.Background(), &auth.Request{Header: tt.hdr, ContentLength: -1})
			wantCode(t, err, "")
			if res.Identity != tt.want {
				t.Errorf("Identity = %+v, want %+v", res.Identity, tt.want)
			}
			if res.SignedTime.Nanosecond() != 0 || res.Trailer != nil {
				t.Errorf("SignedTime = %v Trailer nil=%v, want second precision and no trailer",
					res.SignedTime, res.Trailer == nil)
			}
		})
	}
}

func TestAuthenticatorBody(t *testing.T) {
	tests := []struct {
		name     string
		body     bool
		length   int64
		wantLen  int64
		wantBody bool
	}{
		{name: "no body", body: false, length: 0, wantLen: 0, wantBody: false},
		{name: "known length", body: true, length: 5, wantLen: 5, wantBody: true},
		{name: "unknown length stays -1", body: true, length: -1, wantLen: -1, wantBody: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &auth.Request{Header: http.Header{}, ContentLength: tt.length}
			if tt.body {
				req.Body = strings.NewReader("hello")
			}
			var a Authenticator
			res, err := a.Authenticate(context.Background(), req)
			wantCode(t, err, "")
			if (res.Body != nil) != tt.wantBody {
				t.Errorf("Body set = %v, want %v", res.Body != nil, tt.wantBody)
			}
			if res.DecodedLength != tt.wantLen {
				t.Errorf("DecodedLength = %d, want %d", res.DecodedLength, tt.wantLen)
			}
		})
	}
}

func TestAuthenticatorPayloadSHA256(t *testing.T) {
	digest := sha256.Sum256([]byte("payload"))
	tests := []struct {
		name string
		val  string
		want []byte
	}{
		{name: "hex digest", val: hex.EncodeToString(digest[:]), want: digest[:]},
		{name: "unsigned payload", val: "UNSIGNED-PAYLOAD"},
		{name: "streaming marker", val: "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"},
		{name: "short hex", val: "abcd"},
		{name: "not hex", val: strings.Repeat("z", 64)},
		{name: "absent", val: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr := http.Header{}
			if tt.val != "" {
				hdr.Set("X-Amz-Content-Sha256", tt.val)
			}
			var a Authenticator
			res, err := a.Authenticate(context.Background(), &auth.Request{Header: hdr, ContentLength: -1})
			wantCode(t, err, "")
			if !bytes.Equal(res.PayloadSHA256, tt.want) {
				t.Errorf("PayloadSHA256 = %x, want %x", res.PayloadSHA256, tt.want)
			}
		})
	}
}

func TestAuthenticatorErrors(t *testing.T) {
	boom := s3err.Errorf(s3err.SignatureDoesNotMatch, "signature mismatch")
	var failing Authenticator = Authenticator{Err: boom}
	res, err := failing.Authenticate(context.Background(), &auth.Request{ContentLength: -1})
	if res != nil || !errors.Is(err, boom) {
		t.Errorf("Authenticate with Err = (%v, %v), want (nil, %v)", res, err, boom)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var a Authenticator
	res, err = a.Authenticate(ctx, &auth.Request{ContentLength: -1})
	if res != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("Authenticate with canceled context = (%v, %v), want context.Canceled", res, err)
	}

	res, err = a.Authenticate(context.Background(), nil)
	if res != nil {
		t.Errorf("Authenticate(nil) result = %+v, want nil", res)
	}
	wantCode(t, err, s3err.InvalidRequest)
}
