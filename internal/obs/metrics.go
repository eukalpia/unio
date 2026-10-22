package obs

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics owns one Prometheus registry holding the Unio metric families. No
// global default registry is used, so several instances can coexist, as they
// do in tests.
type Metrics struct {
	registry     *prometheus.Registry
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	httpBytesIn  *prometheus.CounterVec
	httpBytesOut *prometheus.CounterVec
	erasureWrite *prometheus.HistogramVec
	erasureRead  *prometheus.HistogramVec
	driveErrors  *prometheus.CounterVec
	raftApply    *prometheus.HistogramVec
	bufpoolInUse prometheus.Gauge
	gcQueueDepth prometheus.Gauge
}

// NewMetrics returns a Metrics with every family registered on its own
// registry, together with the Go and process collectors. It returns an error
// rather than panicking if a family cannot be registered.
func NewMetrics() (*Metrics, error) {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry: reg,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "unio_http_requests_total",
			Help: "S3 HTTP requests served, by operation and status code.",
		}, []string{"op", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "unio_http_request_duration_seconds",
			Help:    "S3 HTTP request latency in seconds, by operation.",
			Buckets: httpBuckets(),
		}, []string{"op"}),
		httpBytesIn: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "unio_http_request_bytes_in_total",
			Help: "Request body bytes received, by operation.",
		}, []string{"op"}),
		httpBytesOut: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "unio_http_response_bytes_out_total",
			Help: "Response body bytes sent, by operation.",
		}, []string{"op"}),
		erasureWrite: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "unio_erasure_write_duration_seconds",
			Help:    "Erasure-coded write latency in seconds, by result.",
			Buckets: latencyBuckets(),
		}, []string{"result"}),
		erasureRead: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "unio_erasure_read_duration_seconds",
			Help:    "Erasure-coded read latency in seconds, by result.",
			Buckets: latencyBuckets(),
		}, []string{"result"}),
		driveErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "unio_drive_errors_total",
			Help: "Drive errors observed, by drive and error kind.",
		}, []string{"drive", "kind"}),
		raftApply: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "unio_raft_apply_duration_seconds",
			Help:    "Raft log apply latency in seconds, by partition.",
			Buckets: latencyBuckets(),
		}, []string{"partition"}),
		bufpoolInUse: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "unio_bufpool_in_use_bytes",
			Help: "Bytes currently held by buffer pool buffers.",
		}),
		gcQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "unio_gc_queue_depth",
			Help: "Number of objects waiting in the garbage collection queue.",
		}),
	}
	if err := registerAll(reg,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.httpRequests,
		m.httpDuration,
		m.httpBytesIn,
		m.httpBytesOut,
		m.erasureWrite,
		m.erasureRead,
		m.driveErrors,
		m.raftApply,
		m.bufpoolInUse,
		m.gcQueueDepth,
	); err != nil {
		return nil, err
	}
	return m, nil
}

// registerAll registers every collector on reg and returns all failures joined,
// so that a bad family is reported rather than panicking the caller.
func registerAll(reg *prometheus.Registry, cs ...prometheus.Collector) error {
	var errs []error
	for _, c := range cs {
		if err := reg.Register(c); err != nil {
			errs = append(errs, fmt.Errorf("register metric family: %w", err))
		}
	}
	return errors.Join(errs...)
}

// Registry returns the registry that holds every family of m. Serve it with
// MetricsHandler.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// ObserveHTTPRequest records one completed S3 request. Negative byte counts
// are ignored. Operation and status labels are sanitised, so an unexpected
// value cannot create unbounded series.
func (m *Metrics) ObserveHTTPRequest(op string, status int, d time.Duration, bytesIn, bytesOut int64) {
	opLabel := sanitizeLabel(op)
	m.httpRequests.WithLabelValues(opLabel, statusLabel(status)).Inc()
	m.httpDuration.WithLabelValues(opLabel).Observe(d.Seconds())
	if bytesIn >= 0 {
		m.httpBytesIn.WithLabelValues(opLabel).Add(float64(bytesIn))
	}
	if bytesOut >= 0 {
		m.httpBytesOut.WithLabelValues(opLabel).Add(float64(bytesOut))
	}
}

// ObserveErasureWrite records the latency of one erasure-coded write. A
// non-nil err sets the result label to "error".
func (m *Metrics) ObserveErasureWrite(d time.Duration, err error) {
	m.erasureWrite.WithLabelValues(resultLabel(err)).Observe(d.Seconds())
}

// ObserveErasureRead records the latency of one erasure-coded read. A non-nil
// err sets the result label to "error".
func (m *Metrics) ObserveErasureRead(d time.Duration, err error) {
	m.erasureRead.WithLabelValues(resultLabel(err)).Observe(d.Seconds())
}

// IncDriveError counts one error on a drive, by error kind.
func (m *Metrics) IncDriveError(drive, kind string) {
	m.driveErrors.WithLabelValues(sanitizeLabel(drive), sanitizeLabel(kind)).Inc()
}

// ObserveRaftApply records the latency of one Raft apply on a partition.
func (m *Metrics) ObserveRaftApply(partition string, d time.Duration) {
	m.raftApply.WithLabelValues(sanitizeLabel(partition)).Observe(d.Seconds())
}

// SetBufpoolInUse sets the number of bytes held by buffer pool buffers.
func (m *Metrics) SetBufpoolInUse(bytes int64) {
	m.bufpoolInUse.Set(float64(bytes))
}

// SetGCQueueDepth sets the number of objects waiting for garbage collection.
func (m *Metrics) SetGCQueueDepth(depth int64) {
	m.gcQueueDepth.Set(float64(depth))
}

// statusLabel returns the status code as a label value. Codes outside the
// three-digit HTTP range are reported as "other".
func statusLabel(status int) string {
	if status < 100 || status > 999 {
		return otherLabel
	}
	return sanitizeLabel(strconv.Itoa(status))
}

// resultLabel returns "ok" for a nil error and "error" otherwise.
func resultLabel(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

// httpBuckets returns the request latency buckets, from 1 ms to 60 s.
func httpBuckets() []float64 {
	return []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
}

// latencyBuckets returns exponential buckets from 100 microseconds, doubling
// 18 times, so the last bound is about 13 seconds.
func latencyBuckets() []float64 {
	return prometheus.ExponentialBuckets(0.0001, 2, 18)
}
