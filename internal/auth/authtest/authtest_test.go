package authtest

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestMemKVRevisionRules(t *testing.T) {
	ctx := context.Background()
	kv := NewMemKV()

	rev, err := kv.Put(ctx, "k", []byte("v1"), 0)
	if err != nil || rev == 0 {
		t.Fatalf("create with expect 0: rev %d, err %v", rev, err)
	}
	if _, err := kv.Put(ctx, "k", []byte("again"), 0); !errors.Is(err, meta.ErrRevisionMismatch) {
		t.Fatalf("create over existing key: err = %v, want ErrRevisionMismatch", err)
	}
	if _, err := kv.Put(ctx, "k", []byte("stale"), rev+1); !errors.Is(err, meta.ErrRevisionMismatch) {
		t.Fatalf("stale revision: err = %v", err)
	}
	rev2, err := kv.Put(ctx, "k", []byte("v2"), rev)
	if err != nil || rev2 <= rev {
		t.Fatalf("update at current revision: rev %d, err %v", rev2, err)
	}
	if _, err := kv.Put(ctx, "k", []byte("any"), meta.AnyRevision); err != nil {
		t.Fatalf("AnyRevision write: %v", err)
	}
	if _, err := kv.Put(ctx, "missing", []byte("x"), 7); !errors.Is(err, meta.ErrRevisionMismatch) {
		t.Fatalf("specific revision on missing key: err = %v", err)
	}
}

func TestMemKVGetReturnsCopies(t *testing.T) {
	ctx := context.Background()
	kv := NewMemKV()
	src := []byte("original")
	if _, err := kv.Put(ctx, "k", src, 0); err != nil {
		t.Fatal(err)
	}
	src[0] = 'X'
	got, _, err := kv.Get(ctx, "k")
	if err != nil || string(got) != "original" {
		t.Fatalf("stored value follows the caller's buffer: %q, %v", got, err)
	}
	got[0] = 'Y'
	again, _, _ := kv.Get(ctx, "k")
	if string(again) != "original" {
		t.Fatal("Get exposed the stored buffer")
	}
}

func TestMemKVNotFoundAndDelete(t *testing.T) {
	ctx := context.Background()
	kv := NewMemKV()
	if _, _, err := kv.Get(ctx, "nope"); !errors.Is(err, meta.ErrNotFound) {
		t.Fatalf("get missing: err = %v", err)
	}
	if err := kv.Delete(ctx, "nope", meta.AnyRevision); err != nil {
		t.Fatalf("delete missing with AnyRevision: %v", err)
	}
	rev, _ := kv.Put(ctx, "k", []byte("v"), 0)
	if err := kv.Delete(ctx, "k", rev+1); !errors.Is(err, meta.ErrRevisionMismatch) {
		t.Fatalf("delete with stale revision: err = %v", err)
	}
	if err := kv.Delete(ctx, "k", rev); err != nil {
		t.Fatalf("delete at revision: %v", err)
	}
	if _, _, err := kv.Get(ctx, "k"); !errors.Is(err, meta.ErrNotFound) {
		t.Fatalf("key still present after delete: %v", err)
	}
}

func TestMemKVListSortedByPrefix(t *testing.T) {
	ctx := context.Background()
	kv := NewMemKV()
	for _, k := range []string{"iam/users/b", "iam/keys/x", "iam/users/a", "other"} {
		if _, err := kv.Put(ctx, k, []byte(k), meta.AnyRevision); err != nil {
			t.Fatal(err)
		}
	}
	kvs, err := kv.List(ctx, "iam/users/")
	if err != nil {
		t.Fatal(err)
	}
	if len(kvs) != 2 || kvs[0].Key != "iam/users/a" || kvs[1].Key != "iam/users/b" {
		t.Fatalf("list = %+v", kvs)
	}
	if !bytes.Equal(kvs[0].Value, []byte("iam/users/a")) || kvs[0].Revision == 0 {
		t.Fatalf("entry = %+v", kvs[0])
	}
}

func TestMemKVRejectsOversizedValues(t *testing.T) {
	kv := NewMemKV()
	big := make([]byte, meta.MaxSystemValueSize+1)
	if _, err := kv.Put(context.Background(), "k", big, 0); !errors.Is(err, meta.ErrValueTooLarge) {
		t.Fatalf("err = %v, want ErrValueTooLarge", err)
	}
}

func TestMemKVHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	kv := NewMemKV()
	if _, err := kv.Put(ctx, "k", []byte("v"), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestAllowAllAndDenyAll(t *testing.T) {
	ctx := context.Background()
	if err := (AllowAll{}).Authorize(ctx, auth.Identity{}, auth.ActionGetObject, "b", "k"); err != nil {
		t.Fatalf("AllowAll denied: %v", err)
	}
	err := (DenyAll{}).Authorize(ctx, auth.Identity{}, auth.ActionGetObject, "b", "k")
	if !s3err.Is(err, s3err.AccessDenied) {
		t.Fatalf("DenyAll: err = %v, want AccessDenied", err)
	}
}

func TestNewAuthenticatorAcceptsOnlyItsKeyPair(t *testing.T) {
	const ak, sk = "AKIDTESTKEYPAIR0001", "testsecretthatislongenoughforsigning0123456789"
	when := time.Now().UTC()
	a := NewAuthenticator(ak, sk, nil)

	signed := func(key, secret string) *auth.Request {
		r, err := http.NewRequest(http.MethodGet, "https://bucket.s3.example.com/key", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
		creds := aws.Credentials{AccessKeyID: key, SecretAccessKey: secret}
		signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
		if err := signer.SignHTTP(context.Background(), creds, r, "UNSIGNED-PAYLOAD", "s3", TestRegion, when); err != nil {
			t.Fatal(err)
		}
		return &auth.Request{
			Method: r.Method, Host: r.Host, RawPath: r.URL.EscapedPath(),
			Header: r.Header.Clone(), ContentLength: 0,
		}
	}

	res, err := a.Authenticate(context.Background(), signed(ak, sk))
	if err != nil {
		t.Fatalf("fixed key pair rejected: %v", err)
	}
	if !res.Identity.Root || res.Identity.AccessKey != ak {
		t.Fatalf("identity = %+v", res.Identity)
	}
	if _, err := a.Authenticate(context.Background(), signed("AKIDOTHERKEYPAIR0002", sk)); !s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatalf("unknown key: err = %v", err)
	}
}
