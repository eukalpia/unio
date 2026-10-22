package chaos

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/smithy-go"
)

const testBucket = "chaos-test"

// newTestS3Client starts an httptest server running handler and returns a
// client pointed at it. Both are closed when the test ends.
func newTestS3Client(t *testing.T, timeout time.Duration, handler http.HandlerFunc) *S3Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := NewS3Client(srv.URL, testBucket, "ak", "sk", timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

const s3ErrorBody = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>test</Message></Error>`

func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, strings.Replace(s3ErrorBody, "%s", code, 1))
}

func TestS3ClientPutClassification(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    Outcome
	}{
		{
			name: "200 is acknowledged",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != "/"+testBucket+"/chaos/run/7" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusOK)
			},
			want: OutcomeAcked,
		},
		{
			name: "500 is rejected",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				s3Error(w, http.StatusInternalServerError, "InternalError")
			},
			want: OutcomeRejected,
		},
		{
			name: "503 SlowDown is rejected",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				s3Error(w, http.StatusServiceUnavailable, "SlowDown")
			},
			want: OutcomeRejected,
		},
		{
			name: "connection dropped mid-request is unknown",
			handler: func(w http.ResponseWriter, r *http.Request) {
				conn, _, err := http.NewResponseController(w).Hijack()
				if err != nil {
					t.Errorf("hijack: %v", err)
					return
				}
				_ = conn.Close()
			},
			want: OutcomeUnknown,
		},
		{
			name: "no response in time is unknown",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// The server notices that the client gave up only after the
				// request body has been read, so drain it before blocking.
				_, _ = io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
			},
			want: OutcomeUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestS3Client(t, 200*time.Millisecond, tt.handler)
			err := c.Put(context.Background(), "chaos/run/7", []byte("hello"))
			if got := Classify(err); got != tt.want {
				t.Fatalf("Classify(Put) = %v (err %v), want %v", got, err, tt.want)
			}
		})
	}
}

func TestS3ClientGet(t *testing.T) {
	tests := []struct {
		name         string
		handler      http.HandlerFunc
		wantBody     string
		wantNotFound bool
		wantErr      bool
	}{
		{
			name: "object returned",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "hello")
			},
			wantBody: "hello",
		},
		{
			name: "missing key",
			handler: func(w http.ResponseWriter, r *http.Request) {
				s3Error(w, http.StatusNotFound, "NoSuchKey")
			},
			wantNotFound: true,
			wantErr:      true,
		},
		{
			name: "missing bucket is not a missing key",
			handler: func(w http.ResponseWriter, r *http.Request) {
				s3Error(w, http.StatusNotFound, "NoSuchBucket")
			},
			wantErr: true,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				s3Error(w, http.StatusInternalServerError, "InternalError")
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestS3Client(t, time.Second, tt.handler)
			body, err := c.Get(context.Background(), "chaos/run/7")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Get error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && string(body) != tt.wantBody {
				t.Fatalf("body = %q, want %q", body, tt.wantBody)
			}
			if got := isNoSuchKey(err); got != tt.wantNotFound {
				t.Fatalf("isNoSuchKey = %v, want %v", got, tt.wantNotFound)
			}
		})
	}
}

func TestS3ClientEnsureBucket(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		code    string
		wantErr bool
	}{
		{name: "created", status: http.StatusOK},
		{name: "already owned", status: http.StatusConflict, code: "BucketAlreadyOwnedByYou"},
		{name: "already exists", status: http.StatusConflict, code: "BucketAlreadyExists"},
		{name: "access denied", status: http.StatusForbidden, code: "AccessDenied", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestS3Client(t, time.Second, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != "/"+testBucket {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if tt.code == "" {
					w.WriteHeader(tt.status)
					return
				}
				s3Error(w, tt.status, tt.code)
			})
			err := c.EnsureBucket(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("EnsureBucket error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsNoSuchKey(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"NoSuchKey code", &smithy.GenericAPIError{Code: "NoSuchKey"}, true},
		{"wrapped NoSuchKey", errors.Join(errors.New("get"), &smithy.GenericAPIError{Code: "NoSuchKey"}), true},
		{"NoSuchBucket code", &smithy.GenericAPIError{Code: "NoSuchBucket"}, false},
		{"transport error", errors.New("connection refused"), false},
		{"NoSuchKey with status 500", statusErr{500}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNoSuchKey(tt.err); got != tt.want {
				t.Fatalf("isNoSuchKey(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestNewS3ClientValidation(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		bucket   string
		timeout  time.Duration
	}{
		{"empty endpoint", "", testBucket, time.Second},
		{"empty bucket", "http://localhost:9001", "", time.Second},
		{"zero timeout", "http://localhost:9001", testBucket, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewS3Client(tt.endpoint, tt.bucket, "ak", "sk", tt.timeout); err == nil {
				t.Fatal("invalid client accepted")
			}
		})
	}
}

// stubClient is an ObjectClient that tags its replies so tests can see which
// backend answered.
type stubClient struct{ name string }

func (s stubClient) Get(context.Context, string) ([]byte, error) {
	return []byte(s.name), nil
}

func TestRoundRobinRotatesBackends(t *testing.T) {
	rr, err := RoundRobin(stubClient{"a"}, stubClient{"b"}, stubClient{"c"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for range 6 {
		body, err := rr.Get(context.Background(), "k")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(body))
	}
	want := strings.Split("a,b,c,a,b,c", ",")
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestRoundRobinRequiresBackends(t *testing.T) {
	if _, err := RoundRobin(); err == nil {
		t.Fatal("empty backend list accepted")
	}
}
