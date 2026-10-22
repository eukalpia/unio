package objstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const testSecret = "s3cret-value-that-must-not-leak"

func validOptions(endpoint string) Options {
	return Options{
		Endpoint:    endpoint,
		Region:      "us-east-1",
		PathStyle:   true,
		Credentials: Credentials{AccessKey: "bench-access", SecretKey: testSecret},
		Concurrency: 4,
	}
}

func TestNewAWSValidation(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Options)
	}{
		{name: "empty endpoint", mut: func(o *Options) { o.Endpoint = "" }},
		{name: "unsupported scheme", mut: func(o *Options) { o.Endpoint = "ftp://localhost:21" }},
		{name: "missing host", mut: func(o *Options) { o.Endpoint = "http://" }},
		{name: "embedded userinfo", mut: func(o *Options) { o.Endpoint = "http://user:pw@localhost:9000" }},
		{name: "empty region", mut: func(o *Options) { o.Region = "" }},
		{name: "empty access key", mut: func(o *Options) { o.Credentials.AccessKey = "" }},
		{name: "empty secret key", mut: func(o *Options) { o.Credentials.SecretKey = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := validOptions("http://localhost:9000")
			tc.mut(&opts)
			_, err := NewAWS(opts)
			if err == nil {
				t.Fatal("NewAWS returned no error")
			}
			if strings.Contains(err.Error(), testSecret) {
				t.Fatalf("error leaks the secret key: %v", err)
			}
		})
	}
}

func TestNewAWSAcceptsValidOptions(t *testing.T) {
	if _, err := NewAWS(validOptions("https://storage.example.test:9000")); err != nil {
		t.Fatalf("NewAWS: %v", err)
	}
}

func TestCredentialsNeverFormatted(t *testing.T) {
	c := Credentials{AccessKey: "bench-access", SecretKey: testSecret}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if got := fmt.Sprintf(format, c); strings.Contains(got, testSecret) || strings.Contains(got, "bench-access") {
			t.Errorf("format %s exposes credentials: %q", format, got)
		}
	}
}

// fakeS3 is a minimal path-style S3 endpoint covering the calls the adapter
// makes. It is safe for concurrent requests.
type fakeS3 struct {
	mu        sync.Mutex
	objects   map[string][]byte
	lastQuery url.Values
	listXML   string
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodHead:
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		f.objects[r.URL.Path] = body
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		f.lastQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, f.listXML)
	case r.Method == http.MethodGet:
		body, ok := f.objects[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<?xml version="1.0"?><Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		_, _ = w.Write(body)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func newFakeStore(t *testing.T, f *fakeS3) Store {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s, err := NewAWS(validOptions(srv.URL))
	if err != nil {
		t.Fatalf("NewAWS: %v", err)
	}
	return s
}

func TestPutThenGetRoundTrip(t *testing.T) {
	f := &fakeS3{objects: map[string][]byte{}}
	s := newFakeStore(t, f)
	ctx := context.Background()
	body := make([]byte, 4096)

	if err := s.PutObject(ctx, "bench", "bench/4k/00000001", body); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	n, err := s.GetObject(ctx, "bench", "bench/4k/00000001")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("GetObject returned %d bytes, want %d", n, len(body))
	}
}

func TestGetObjectMissingIsError(t *testing.T) {
	s := newFakeStore(t, &fakeS3{objects: map[string][]byte{}})
	if _, err := s.GetObject(context.Background(), "bench", "missing"); err == nil {
		t.Fatal("GetObject of a missing key returned no error")
	}
}

func TestEnsureBucketExisting(t *testing.T) {
	s := newFakeStore(t, &fakeS3{objects: map[string][]byte{}})
	if err := s.EnsureBucket(context.Background(), "bench"); err != nil {
		t.Fatalf("EnsureBucket on an existing bucket: %v", err)
	}
}

func TestListObjectsV2MapsRequestAndPage(t *testing.T) {
	const page = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>bench</Name>
  <Prefix>list/p00/</Prefix>
  <KeyCount>3</KeyCount>
  <MaxKeys>1000</MaxKeys>
  <IsTruncated>true</IsTruncated>
  <Contents><Key>list/p00/a</Key><Size>1</Size></Contents>
  <Contents><Key>list/p00/b</Key><Size>1</Size></Contents>
  <CommonPrefixes><Prefix>list/p00/archive/</Prefix></CommonPrefixes>
  <NextContinuationToken>tok-2</NextContinuationToken>
</ListBucketResult>`
	f := &fakeS3{objects: map[string][]byte{}, listXML: page}
	s := newFakeStore(t, f)

	got, err := s.ListObjectsV2(context.Background(), "bench", ListRequest{
		Prefix:            "list/p00/",
		Delimiter:         "/",
		MaxKeys:           1000,
		ContinuationToken: "tok-1",
	})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	want := ListPage{Keys: 2, CommonPrefixes: 1, Truncated: true, NextToken: "tok-2"}
	if got != want {
		t.Fatalf("page = %+v, want %+v", got, want)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	checks := map[string]string{
		"list-type":          "2",
		"prefix":             "list/p00/",
		"delimiter":          "/",
		"max-keys":           "1000",
		"continuation-token": "tok-1",
	}
	for k, v := range checks {
		if g := f.lastQuery.Get(k); g != v {
			t.Errorf("query %s = %q, want %q", k, g, v)
		}
	}
}

func TestListObjectsV2OmitsEmptyParameters(t *testing.T) {
	const page = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>bench</Name><IsTruncated>false</IsTruncated></ListBucketResult>`
	f := &fakeS3{objects: map[string][]byte{}, listXML: page}
	s := newFakeStore(t, f)

	got, err := s.ListObjectsV2(context.Background(), "bench", ListRequest{Prefix: "x/"})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	if got.Truncated || got.NextToken != "" || got.Keys != 0 {
		t.Fatalf("empty page = %+v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range []string{"delimiter", "continuation-token", "max-keys"} {
		if _, ok := f.lastQuery[k]; ok {
			t.Errorf("query parameter %s was sent but was empty", k)
		}
	}
}
