package security

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// leakPatterns match fragments that reveal implementation details.
var leakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`goroutine \d+`),
	regexp.MustCompile(`\.go:\d+`),
	regexp.MustCompile(`(?i)panic:`),
	regexp.MustCompile(`runtime\.`),
	regexp.MustCompile(`/(home|root|var|tmp|usr)/`),
	regexp.MustCompile(`[A-Z]:\\`),
	regexp.MustCompile(`(?i)(pebble|raft|uniwire|xxh3|reedsolomon)`),
}

func assertNoLeak(t *testing.T, tg target, what string, r response) {
	t.Helper()
	for _, secret := range []string{tg.secretKey, tg.secretKey[:len(tg.secretKey)/2]} {
		if strings.Contains(string(r.body), secret) {
			t.Fatalf("%s: response contains the secret key", what)
		}
	}
	for _, p := range leakPatterns {
		if loc := p.FindIndex(r.body); loc != nil {
			t.Fatalf("%s: response leaks internal detail %q: %s", what, r.body[loc[0]:loc[1]], truncate(r.body))
		}
	}
	for k, vs := range r.header {
		for _, v := range vs {
			if strings.Contains(v, tg.secretKey) || strings.Contains(strings.ToLower(k), "debug") {
				t.Fatalf("%s: header %s leaks: %q", what, k, v)
			}
		}
	}
}

func TestErrorResponsesDoNotLeakInternals(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	signed := func(method, url string, body []byte, c creds) response {
		req := newRequest(t, method, url, body)
		tg.sign(t, req, body, c, time.Now(), "")
		return send(t, req)
	}
	cases := map[string]response{
		"missing key":      signed(http.MethodGet, tg.objectURL(bucket, "missing", ""), nil, tg.goodCreds()),
		"missing bucket":   signed(http.MethodGet, tg.objectURL("no-such-bucket-"+randomHex(t, 4), "k", ""), nil, tg.goodCreds()),
		"invalid bucket":   signed(http.MethodPut, tg.objectURL("Invalid_Bucket", "", ""), nil, tg.goodCreds()),
		"bad signature":    signed(http.MethodGet, tg.objectURL(bucket, "", ""), nil, creds{tg.accessKey, "wrong-" + tg.secretKey}),
		"malformed xml":    send(t, deleteObjectsRequest(t, tg, bucket, []byte("<Delete><Object>"))),
		"invalid range":    rangeRequest(t, tg, bucket),
		"unsupported verb": signed("PATCH", tg.objectURL(bucket, "k", ""), []byte("x"), tg.goodCreds()),
	}
	for name, r := range cases {
		if r.status < 400 {
			t.Fatalf("%s: expected an error, got %d", name, r.status)
		}
		assertNoLeak(t, tg, name, r)
	}
}

func rangeRequest(t *testing.T, tg target, bucket string) response {
	t.Helper()
	tg.putObject(t, bucket, "small", []byte("0123456789"))
	req := newRequest(t, http.MethodGet, tg.objectURL(bucket, "small", ""), nil)
	req.Header.Set("Range", "bytes=100-200")
	tg.sign(t, req, nil, tg.goodCreds(), time.Now(), "")
	return send(t, req)
}
