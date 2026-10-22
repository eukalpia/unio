package security

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

func TestUnsignedRequestIsDenied(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	for _, tc := range []struct{ method, key string }{
		{http.MethodGet, ""},
		{http.MethodPut, "unsigned.txt"},
		{http.MethodGet, "unsigned.txt"},
		{http.MethodDelete, ""},
	} {
		r := send(t, newRequest(t, tc.method, tg.objectURL(bucket, tc.key, ""), []byte("x")))
		expectRejected(t, r, "AccessDenied")
	}
	if tg.objectExists(t, bucket, "unsigned.txt") {
		t.Fatal("unsigned PUT created an object")
	}
}

func TestWrongSecretIsRejected(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	body := []byte("payload")
	req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "k", ""), body)
	tg.sign(t, req, body, creds{tg.accessKey, tg.secretKey + "x"}, time.Now(), "")
	expectRejected(t, send(t, req), "SignatureDoesNotMatch")
	if tg.objectExists(t, bucket, "k") {
		t.Fatal("request with a wrong secret created an object")
	}
}

func TestUnknownAccessKeyIsRejected(t *testing.T) {
	tg := loadTarget(t)
	req := newRequest(t, http.MethodGet, tg.objectURL("", "", ""), nil)
	tg.sign(t, req, nil, creds{"AKUNKNOWN" + strings.ToUpper(randomHex(t, 6)), tg.secretKey}, time.Now(), "")
	expectRejected(t, send(t, req), "InvalidAccessKeyId")
}

func TestTamperingAfterSigningIsRejected(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	body := []byte("original")

	t.Run("signed header changed", func(t *testing.T) {
		req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "hdr", ""), body)
		req.Header.Set("X-Amz-Meta-Owner", "alice")
		tg.sign(t, req, body, tg.goodCreds(), time.Now(), "")
		req.Header.Set("X-Amz-Meta-Owner", "mallory")
		expectRejected(t, send(t, req), "SignatureDoesNotMatch")
		if tg.objectExists(t, bucket, "hdr") {
			t.Fatal("tampered request created an object")
		}
	})

	t.Run("path changed", func(t *testing.T) {
		req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "intended", ""), body)
		tg.sign(t, req, body, tg.goodCreds(), time.Now(), "")
		other, _ := url.Parse(tg.objectURL(bucket, "redirected", ""))
		req.URL = other
		expectRejected(t, send(t, req), "SignatureDoesNotMatch")
		if tg.objectExists(t, bucket, "redirected") || tg.objectExists(t, bucket, "intended") {
			t.Fatal("request with a changed path created an object")
		}
	})

	t.Run("method changed", func(t *testing.T) {
		tg.putObject(t, bucket, "keep", body)
		req := newRequest(t, http.MethodGet, tg.objectURL(bucket, "keep", ""), nil)
		tg.sign(t, req, nil, tg.goodCreds(), time.Now(), "")
		req.Method = http.MethodDelete
		expectRejected(t, send(t, req), "SignatureDoesNotMatch")
		if !tg.objectExists(t, bucket, "keep") {
			t.Fatal("a GET signature was replayed as DELETE")
		}
	})

	t.Run("signed payload swapped", func(t *testing.T) {
		req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "payload", ""), []byte("forged!!"))
		tg.sign(t, req, body, tg.goodCreds(), time.Now(), "")
		r := send(t, req)
		expectRejected(t, r, "XAmzContentSHA256Mismatch", "SignatureDoesNotMatch", "BadDigest")
		if tg.objectExists(t, bucket, "payload") {
			t.Fatal("a body that does not match the signed hash was stored")
		}
	})

	t.Run("query parameter added", func(t *testing.T) {
		req := newRequest(t, http.MethodGet, tg.objectURL(bucket, "", "list-type=2"), nil)
		tg.sign(t, req, nil, tg.goodCreds(), time.Now(), "")
		req.URL.RawQuery = "list-type=2&prefix=secret"
		expectRejected(t, send(t, req), "SignatureDoesNotMatch")
	})
}

func TestClockSkewIsBounded(t *testing.T) {
	tg := loadTarget(t)
	for _, skew := range []time.Duration{-20 * time.Minute, 20 * time.Minute} {
		req := newRequest(t, http.MethodGet, tg.objectURL("", "", ""), nil)
		tg.sign(t, req, nil, tg.goodCreds(), time.Now().Add(skew), "")
		expectRejected(t, send(t, req), "RequestTimeTooSkewed", "AccessDenied")
	}
}

func presign(t *testing.T, tg target, method, rawURL string, when time.Time, expires time.Duration) string {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	q.Set("X-Amz-Expires", formatSeconds(expires))
	req.URL.RawQuery = q.Encode()
	signed, _, err := v4.NewSigner().PresignHTTP(context.Background(),
		aws.Credentials{AccessKeyID: tg.accessKey, SecretAccessKey: tg.secretKey},
		req, "UNSIGNED-PAYLOAD", "s3", tg.region, when)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	return signed
}

func formatSeconds(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10)
}

func TestPresignedURLs(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	tg.putObject(t, bucket, "shared.txt", []byte("shared"))
	target := tg.objectURL(bucket, "shared.txt", "")

	t.Run("valid", func(t *testing.T) {
		r := send(t, newRequest(t, http.MethodGet, presign(t, tg, http.MethodGet, target, time.Now(), time.Hour), nil))
		if r.status != http.StatusOK || string(r.body) != "shared" {
			t.Fatalf("valid presigned GET failed: %d %s", r.status, truncate(r.body))
		}
	})

	t.Run("expired", func(t *testing.T) {
		u := presign(t, tg, http.MethodGet, target, time.Now().Add(-2*time.Hour), time.Hour)
		expectRejected(t, send(t, newRequest(t, http.MethodGet, u, nil)), "AccessDenied")
	})

	t.Run("expiry longer than seven days", func(t *testing.T) {
		u := presign(t, tg, http.MethodGet, target, time.Now(), 7*24*time.Hour+time.Second)
		expectRejected(t, send(t, newRequest(t, http.MethodGet, u, nil)), "AuthorizationQueryParametersError", "AccessDenied", "InvalidArgument", "AuthorizationHeaderMalformed")
	})

	t.Run("object swapped", func(t *testing.T) {
		tg.putObject(t, bucket, "private.txt", []byte("private"))
		u := presign(t, tg, http.MethodGet, target, time.Now(), time.Hour)
		u = strings.Replace(u, "/shared.txt", "/private.txt", 1)
		expectRejected(t, send(t, newRequest(t, http.MethodGet, u, nil)), "SignatureDoesNotMatch")
	})

	t.Run("method swapped", func(t *testing.T) {
		u := presign(t, tg, http.MethodGet, target, time.Now(), time.Hour)
		expectRejected(t, send(t, newRequest(t, http.MethodDelete, u, nil)), "SignatureDoesNotMatch")
		if !tg.objectExists(t, bucket, "shared.txt") {
			t.Fatal("a presigned GET was usable as DELETE")
		}
	})

	t.Run("expiry extended", func(t *testing.T) {
		u := presign(t, tg, http.MethodGet, target, time.Now().Add(-2*time.Hour), time.Hour)
		u = strings.Replace(u, "X-Amz-Expires=3600", "X-Amz-Expires=604800", 1)
		expectRejected(t, send(t, newRequest(t, http.MethodGet, u, nil)), "SignatureDoesNotMatch", "AccessDenied")
	})
}
