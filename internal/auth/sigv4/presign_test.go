package sigv4

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/s3err"
)

// presignedDocURL returns the documented presigned example with its
// parameters rewritten by edit.
func presignedDocURL(edit func(q url.Values)) string {
	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", docAccessKey+"/20130524/us-east-1/s3/aws4_request")
	q.Set("X-Amz-Date", "20130524T000000Z")
	q.Set("X-Amz-Expires", "86400")
	q.Set("X-Amz-SignedHeaders", "host")
	q.Set("X-Amz-Signature", "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404")
	if edit != nil {
		edit(q)
	}
	return "https://examplebucket.s3.amazonaws.com/test.txt?" + q.Encode()
}

func TestPresignedLifetime(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want s3err.Code
	}{
		{"just issued", docNow, ""},
		{"one second before expiry", docNow.Add(86399 * time.Second), ""},
		{"at expiry", docNow.Add(86400 * time.Second), s3err.AccessDenied},
		{"long after expiry", docNow.Add(30 * 24 * time.Hour), s3err.AccessDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newHTTPRequest(t, http.MethodGet, presignedDocURL(nil), nil)
			a := newTestAuthenticator(c.now)
			_, err := a.Authenticate(context.Background(), authRequest(t, r))
			if c.want == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if !s3err.Is(err, c.want) {
				t.Fatalf("err = %v, want %s", err, c.want)
			}
			if !strings.Contains(err.Error(), "Request has expired") {
				t.Fatalf("message = %q, want the expiry message", err.Error())
			}
		})
	}
}

func TestPresignedRejectsBadParameters(t *testing.T) {
	cases := []struct {
		name string
		edit func(q url.Values)
	}{
		{"zero expiry", func(q url.Values) { q.Set("X-Amz-Expires", "0") }},
		{"expiry above seven days", func(q url.Values) { q.Set("X-Amz-Expires", "604801") }},
		{"negative expiry", func(q url.Values) { q.Set("X-Amz-Expires", "-1") }},
		{"non-numeric expiry", func(q url.Values) { q.Set("X-Amz-Expires", "1h") }},
		{"empty expiry", func(q url.Values) { q.Set("X-Amz-Expires", "") }},
		{"unknown algorithm", func(q url.Values) { q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA512") }},
		{"missing signature", func(q url.Values) { q.Del("X-Amz-Signature") }},
		{"duplicate date", func(q url.Values) { q.Add("X-Amz-Date", "20130524T000000Z") }},
		{"signed headers without host", func(q url.Values) { q.Set("X-Amz-SignedHeaders", "range") }},
		{"malformed credential", func(q url.Values) { q.Set("X-Amz-Credential", "AKIA/20130524") }},
		{"bad date", func(q url.Values) { q.Set("X-Amz-Date", "2013-05-24") }},
		{"date does not match scope", func(q url.Values) { q.Set("X-Amz-Date", "20130525T000000Z") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newHTTPRequest(t, http.MethodGet, presignedDocURL(c.edit), nil)
			a := newTestAuthenticator(docNow.Add(time.Minute))
			_, err := a.Authenticate(context.Background(), authRequest(t, r))
			if !s3err.Is(err, s3err.AuthorizationHeaderMalformed) {
				t.Fatalf("err = %v, want AuthorizationHeaderMalformed", err)
			}
		})
	}
}

func TestPresignedSignatureMismatch(t *testing.T) {
	r := newHTTPRequest(t, http.MethodGet, presignedDocURL(func(q url.Values) {
		q.Set("X-Amz-Expires", "86401")
	}), nil)
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); !s3err.Is(err, s3err.SignatureDoesNotMatch) {
		t.Fatalf("err = %v, want SignatureDoesNotMatch", err)
	}
}

func TestPresignedRejectsFutureRequestTime(t *testing.T) {
	r := newHTTPRequest(t, http.MethodGet, presignedDocURL(nil), nil)
	a := newTestAuthenticator(docNow.Add(-20 * time.Minute))
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); !s3err.Is(err, s3err.RequestTimeTooSkewed) {
		t.Fatalf("err = %v, want RequestTimeTooSkewed", err)
	}
}

func TestPresignedRejectsStreamingMarker(t *testing.T) {
	r := newHTTPRequest(t, http.MethodPut, presignedDocURL(nil), nil)
	r.Header.Set("X-Amz-Content-Sha256", streamingPayload)
	a := newTestAuthenticator(docNow)
	if _, err := a.Authenticate(context.Background(), authRequest(t, r)); !s3err.Is(err, s3err.InvalidRequest) {
		t.Fatalf("err = %v, want InvalidRequest", err)
	}
}

func TestParseExpires(t *testing.T) {
	cases := map[string]bool{
		"1": true, "86400": true, "604800": true,
		"0": false, "604801": false, "": false, "1234567": false, " 5": false, "+5": false,
	}
	for in, ok := range cases {
		_, err := parseExpires(in)
		if (err == nil) != ok {
			t.Fatalf("parseExpires(%q) err = %v, want ok=%v", in, err, ok)
		}
	}
}
