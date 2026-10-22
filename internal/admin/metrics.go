package admin

import (
	"net/http"
	"time"
)

// summaryView is the body of GET metrics/summary. The rates are averages over
// the last WindowSeconds. The buffer and backlog figures are the values now.
type summaryView struct {
	RequestsPerSecond float64 `json:"requestsPerSecond"`
	ErrorsPerSecond   float64 `json:"errorsPerSecond"`
	BytesInPerSecond  float64 `json:"bytesInPerSecond"`
	BytesOutPerSecond float64 `json:"bytesOutPerSecond"`
	P99Millis         float64 `json:"p99Millis"`
	BufferBytesInUse  int64   `json:"bufferBytesInUse"`
	DeletionsPending  int     `json:"deletionsPending"`
}

// metricsSummary answers GET metrics/summary.
func (s *Service) metricsSummary(c *call) (Response, error) {
	w := s.cfg.Recorder.Summary(s.cfg.Clock())
	secs := float64(WindowSeconds)
	return jsonResponse(http.StatusOK, summaryView{
		RequestsPerSecond: float64(w.Requests) / secs,
		ErrorsPerSecond:   float64(w.Errors) / secs,
		BytesInPerSecond:  float64(w.BytesIn) / secs,
		BytesOutPerSecond: float64(w.BytesOut) / secs,
		P99Millis:         float64(w.P99) / float64(time.Millisecond),
		BufferBytesInUse:  s.cfg.BufferInUse(),
		DeletionsPending:  s.cfg.Deletions.Stats().GCPending,
	})
}
