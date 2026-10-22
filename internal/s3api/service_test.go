package s3api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3api"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// do sends a request with a plain client and returns the status and body.
func do(t *testing.T, hc *http.Client, method, url string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestHealthEndpoints(t *testing.T) {
	base := startServer(t, s3api.Config{}, nil)
	hc := newHTTPClient(t, nil)
	cases := []struct {
		name, method, path string
		want               int
	}{
		{"live", "GET", "/unio/health/live", http.StatusOK},
		{"live head", "HEAD", "/unio/health/live", http.StatusOK},
		{"ready", "GET", "/unio/health/ready", http.StatusOK},
		{"live wrong method", "POST", "/unio/health/live", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if status, _ := do(t, hc, tc.method, base+tc.path, nil); status != tc.want {
				t.Fatalf("status = %d, want %d", status, tc.want)
			}
		})
	}
}

func TestReadinessFailureHidesCause(t *testing.T) {
	base := startServer(t, s3api.Config{
		Readiness: func(context.Context) error { return errors.New("drive /dev/sda: input/output error") },
	}, nil)
	status, body := do(t, newHTTPClient(t, nil), "GET", base+"/unio/health/ready", nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if strings.Contains(body, "dev/sda") || strings.Contains(body, "input/output") {
		t.Fatalf("readiness body leaks the cause: %q", body)
	}
}

func TestMetricsUnsetIsNotImplemented(t *testing.T) {
	base := startServer(t, s3api.Config{}, nil)
	status, body := do(t, newHTTPClient(t, nil), "GET", base+"/unio/metrics", map[string]string{"Authorization": "x"})
	if status != http.StatusNotImplemented || !strings.Contains(body, "NotImplemented") {
		t.Fatalf("status %d body %q", status, body)
	}
}

func TestMetricsHookRequiresAdmin(t *testing.T) {
	hook := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("unio_up 1\n"))
	})
	deny := &fakes.Authorizer{Deny: func(_ auth.Identity, action auth.Action, _, _ string) bool {
		return action == auth.ActionAdmin
	}}
	base := startServer(t, s3api.Config{Metrics: hook}, deny)
	hc := newHTTPClient(t, nil)

	if status, body := do(t, hc, "GET", base+"/unio/metrics", map[string]string{"Authorization": "x"}); status != http.StatusForbidden || !strings.Contains(body, "AccessDenied") {
		t.Fatalf("admin-denied metrics: status %d body %q", status, body)
	}
	allowed := startServer(t, s3api.Config{Metrics: hook}, nil)
	status, body := do(t, newHTTPClient(t, nil), "GET", allowed+"/unio/metrics", map[string]string{"Authorization": "x"})
	if status != http.StatusOK || body != "unio_up 1\n" {
		t.Fatalf("metrics: status %d body %q", status, body)
	}
}

func TestReservedNamespaceIsNeverABucket(t *testing.T) {
	base := startServer(t, s3api.Config{}, nil)
	hc := newHTTPClient(t, nil)
	for _, p := range []string{"/unio/admin/v1/users", "/unio/anything", "/unio"} {
		status, body := do(t, hc, "GET", base+p, map[string]string{"Authorization": "x"})
		if status != http.StatusNotFound || !strings.Contains(body, "NoSuchKey") {
			t.Fatalf("%s: status %d body %q", p, status, body)
		}
	}
}

func TestBucketNamedUnioIsReserved(t *testing.T) {
	base := startServer(t, s3api.Config{}, nil)
	client := newS3(base, newHTTPClient(t, nil))
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String("unio")})
	if err == nil || !strings.Contains(err.Error(), "InvalidBucketName") {
		t.Fatalf("CreateBucket(unio) = %v, want InvalidBucketName", err)
	}
}
