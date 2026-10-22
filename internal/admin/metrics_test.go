package admin

import (
	"net/http"
	"testing"
	"time"
)

// summaryBody is the decoded body of GET metrics/summary.
type summaryBody struct {
	RequestsPerSecond float64 `json:"requestsPerSecond"`
	ErrorsPerSecond   float64 `json:"errorsPerSecond"`
	BytesInPerSecond  float64 `json:"bytesInPerSecond"`
	BytesOutPerSecond float64 `json:"bytesOutPerSecond"`
	P99Millis         float64 `json:"p99Millis"`
	BufferBytesInUse  int64   `json:"bufferBytesInUse"`
	DeletionsPending  int     `json:"deletionsPending"`
}

func TestMetricsSummary(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)

	// An idle node reports zero rates, its buffer use and its backlog.
	resp := f.serve(f.sigv4(creds, http.MethodGet, BasePath+"/metrics/summary", "", nil))
	expectStatus(t, resp, http.StatusOK)
	expectNoStore(t, resp)
	var idle summaryBody
	mustUnmarshal(t, resp.Body, &idle)
	if idle.RequestsPerSecond != 0 || idle.P99Millis != 0 {
		t.Errorf("idle summary = %+v", idle)
	}
	if idle.BufferBytesInUse != 4096 || idle.DeletionsPending != 3 {
		t.Errorf("buffer=%d deletions=%d, want 4096 and 3", idle.BufferBytesInUse, idle.DeletionsPending)
	}

	// Sixty requests in the window: one per second is one request a second.
	for i := 0; i < 60; i++ {
		status := 200
		if i%10 == 0 {
			status = 500
		}
		f.rec.ObserveHTTPRequest("GetObject", status, 2*time.Millisecond, 100, 300)
	}
	resp = f.serve(f.sigv4(creds, http.MethodGet, BasePath+"/metrics/summary", "", nil))
	var busy summaryBody
	mustUnmarshal(t, resp.Body, &busy)
	if busy.RequestsPerSecond != 1 {
		t.Errorf("requestsPerSecond = %v, want 1 (60 requests over 60 s)", busy.RequestsPerSecond)
	}
	if busy.ErrorsPerSecond != 6.0/60 {
		t.Errorf("errorsPerSecond = %v, want 0.1", busy.ErrorsPerSecond)
	}
	if busy.BytesInPerSecond != 100 || busy.BytesOutPerSecond != 300 {
		t.Errorf("bytes per second = %v in, %v out, want 100 and 300", busy.BytesInPerSecond, busy.BytesOutPerSecond)
	}
	if busy.P99Millis < 2 || busy.P99Millis > 2.6 {
		t.Errorf("p99Millis = %v, want about 2", busy.P99Millis)
	}

	// After a minute the traffic has left the window.
	f.clock.Advance(WindowSeconds * time.Second)
	resp = f.serve(f.sigv4(creds, http.MethodGet, BasePath+"/metrics/summary", "", nil))
	var later summaryBody
	mustUnmarshal(t, resp.Body, &later)
	if later.RequestsPerSecond != 0 {
		t.Errorf("requests after the window = %v, want 0", later.RequestsPerSecond)
	}
}

func TestMetricsSummaryErrors(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)
	expectError(t, f.serve(f.sigv4(creds, http.MethodPost, BasePath+"/metrics/summary", "", nil)), http.StatusMethodNotAllowed, "MethodNotAllowed")
	expectError(t, f.serve(f.request(http.MethodGet, BasePath+"/metrics/summary", nil)), http.StatusUnauthorized, "AccessDenied")
}
