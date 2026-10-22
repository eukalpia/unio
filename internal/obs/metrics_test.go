package obs

import (
	"errors"
	"maps"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// familyTypes gathers g and maps each family name to its metric type.
func familyTypes(t *testing.T, g prometheus.Gatherer) map[string]string {
	t.Helper()
	mfs, err := g.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	out := make(map[string]string, len(mfs))
	for _, mf := range mfs {
		out[mf.GetName()] = mf.GetType().String()
	}
	return out
}

// sampleValue returns the counter or gauge value of the series in family whose
// label set equals want. The test fails if no such series exists.
func sampleValue(t *testing.T, reg *prometheus.Registry, family string, want map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != family {
			continue
		}
		for _, m := range mf.GetMetric() {
			got := make(map[string]string, len(m.GetLabel()))
			for _, lp := range m.GetLabel() {
				got[lp.GetName()] = lp.GetValue()
			}
			if !maps.Equal(got, want) {
				continue
			}
			if c := m.GetCounter(); c != nil {
				return c.GetValue()
			}
			if g := m.GetGauge(); g != nil {
				return g.GetValue()
			}
		}
	}
	t.Fatalf("no counter or gauge series %s%v", family, want)
	return 0
}

// histogramCount returns the sample count of the histogram series in family
// whose label set equals want. The test fails if no such series exists.
func histogramCount(t *testing.T, reg *prometheus.Registry, family string, want map[string]string) uint64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != family {
			continue
		}
		for _, m := range mf.GetMetric() {
			got := make(map[string]string, len(m.GetLabel()))
			for _, lp := range m.GetLabel() {
				got[lp.GetName()] = lp.GetValue()
			}
			if h := m.GetHistogram(); h != nil && maps.Equal(got, want) {
				return h.GetSampleCount()
			}
		}
	}
	t.Fatalf("no histogram series %s%v", family, want)
	return 0
}

// histogramBounds returns the explicit upper bounds of the first series in family.
func histogramBounds(t *testing.T, reg *prometheus.Registry, family string) []float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != family {
			continue
		}
		for _, m := range mf.GetMetric() {
			if h := m.GetHistogram(); h != nil {
				bounds := make([]float64, 0, len(h.GetBucket()))
				for _, b := range h.GetBucket() {
					bounds = append(bounds, b.GetUpperBound())
				}
				return bounds
			}
		}
	}
	t.Fatalf("no histogram family %s", family)
	return nil
}

// observeEveryFamily records one sample in each metric family.
func observeEveryFamily(m *Metrics) {
	m.ObserveHTTPRequest("GetObject", 200, time.Millisecond, 10, 20)
	m.ObserveErasureWrite(time.Millisecond, nil)
	m.ObserveErasureRead(time.Millisecond, errors.New("read failed"))
	m.IncDriveError("drive-1", "io")
	m.ObserveRaftApply("p0", time.Millisecond)
	m.SetBufpoolInUse(1024)
	m.SetGCQueueDepth(3)
}

func TestNewMetricsFamilies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		family   string
		wantType string
	}{
		{family: "unio_http_requests_total", wantType: "COUNTER"},
		{family: "unio_http_request_duration_seconds", wantType: "HISTOGRAM"},
		{family: "unio_http_request_bytes_in_total", wantType: "COUNTER"},
		{family: "unio_http_response_bytes_out_total", wantType: "COUNTER"},
		{family: "unio_erasure_write_duration_seconds", wantType: "HISTOGRAM"},
		{family: "unio_erasure_read_duration_seconds", wantType: "HISTOGRAM"},
		{family: "unio_drive_errors_total", wantType: "COUNTER"},
		{family: "unio_raft_apply_duration_seconds", wantType: "HISTOGRAM"},
		{family: "unio_bufpool_in_use_bytes", wantType: "GAUGE"},
		{family: "unio_gc_queue_depth", wantType: "GAUGE"},
		{family: "go_goroutines", wantType: "GAUGE"},
		{family: "process_cpu_seconds_total", wantType: "COUNTER"},
	}
	m := NewMetrics()
	observeEveryFamily(m)
	types := familyTypes(t, m.Registry())
	for _, tc := range tests {
		t.Run(tc.family, func(t *testing.T) {
			t.Parallel()
			if got := types[tc.family]; got != tc.wantType {
				t.Fatalf("family %s type = %q, want %q", tc.family, got, tc.wantType)
			}
		})
	}
}

func TestMetricsAreIsolatedFromGlobalRegistry(t *testing.T) {
	t.Parallel()
	a := NewMetrics()
	b := NewMetrics()
	if a.Registry() == b.Registry() {
		t.Fatal("two instances share a registry")
	}
	observeEveryFamily(a)

	if _, ok := familyTypes(t, b.Registry())["unio_drive_errors_total"]; ok {
		t.Fatal("observation on instance a leaked into instance b")
	}
	for name := range familyTypes(t, prometheus.DefaultGatherer) {
		if strings.HasPrefix(name, "unio_") {
			t.Fatalf("family %s registered on the global default registry", name)
		}
	}
}

func TestObserveHTTPRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		op         string
		status     int
		in, out    int64
		wantOp     string
		wantStatus string
		wantIn     float64
		wantOut    float64
	}{
		{
			name: "counts request and bytes", op: "GetObject", status: 200, in: 100, out: 2048,
			wantOp: "GetObject", wantStatus: "200", wantIn: 100, wantOut: 2048,
		},
		{
			name: "zero bytes still create series", op: "HeadObject", status: 404, in: 0, out: 0,
			wantOp: "HeadObject", wantStatus: "404", wantIn: 0, wantOut: 0,
		},
		{
			name: "status below range becomes other", op: "GetObject", status: 0, in: 1, out: 1,
			wantOp: "GetObject", wantStatus: "other", wantIn: 1, wantOut: 1,
		},
		{
			name: "status above range becomes other", op: "GetObject", status: 1000, in: 1, out: 1,
			wantOp: "GetObject", wantStatus: "other", wantIn: 1, wantOut: 1,
		},
		{
			name: "op with unsafe characters becomes other", op: "Get/Object?x=1", status: 200, in: 3, out: 4,
			wantOp: "other", wantStatus: "200", wantIn: 3, wantOut: 4,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMetrics()
			m.ObserveHTTPRequest(tc.op, tc.status, 5*time.Millisecond, tc.in, tc.out)
			reg := m.Registry()
			labels := map[string]string{"op": tc.wantOp, "status": tc.wantStatus}
			if got := sampleValue(t, reg, "unio_http_requests_total", labels); got != 1 {
				t.Errorf("requests_total = %v, want 1", got)
			}
			if got := histogramCount(t, reg, "unio_http_request_duration_seconds", map[string]string{"op": tc.wantOp}); got != 1 {
				t.Errorf("duration sample count = %d, want 1", got)
			}
			if got := sampleValue(t, reg, "unio_http_request_bytes_in_total", map[string]string{"op": tc.wantOp}); got != tc.wantIn {
				t.Errorf("bytes_in = %v, want %v", got, tc.wantIn)
			}
			if got := sampleValue(t, reg, "unio_http_response_bytes_out_total", map[string]string{"op": tc.wantOp}); got != tc.wantOut {
				t.Errorf("bytes_out = %v, want %v", got, tc.wantOut)
			}
		})
	}
}

func TestObserveHTTPRequestIgnoresNegativeBytes(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	m.ObserveHTTPRequest("PutObject", 200, time.Millisecond, 100, 200)
	m.ObserveHTTPRequest("PutObject", 200, time.Millisecond, -50, -1)
	op := map[string]string{"op": "PutObject"}
	if got := sampleValue(t, m.Registry(), "unio_http_request_bytes_in_total", op); got != 100 {
		t.Fatalf("bytes_in = %v, want 100 (negative input must be ignored, not subtracted)", got)
	}
	if got := sampleValue(t, m.Registry(), "unio_http_response_bytes_out_total", op); got != 200 {
		t.Fatalf("bytes_out = %v, want 200", got)
	}
}

func TestObserveErasure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		wantResult string
	}{
		{name: "success is ok", err: nil, wantResult: "ok"},
		{name: "failure is error", err: errors.New("drive offline"), wantResult: "error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMetrics()
			m.ObserveErasureWrite(2*time.Millisecond, tc.err)
			m.ObserveErasureRead(3*time.Millisecond, tc.err)
			labels := map[string]string{"result": tc.wantResult}
			if got := histogramCount(t, m.Registry(), "unio_erasure_write_duration_seconds", labels); got != 1 {
				t.Errorf("write sample count = %d, want 1", got)
			}
			if got := histogramCount(t, m.Registry(), "unio_erasure_read_duration_seconds", labels); got != 1 {
				t.Errorf("read sample count = %d, want 1", got)
			}
		})
	}
}

func TestObserveDriveError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		drive     string
		kind      string
		wantDrive string
		wantKind  string
	}{
		{name: "valid labels kept", drive: "drive-1", kind: "checksum", wantDrive: "drive-1", wantKind: "checksum"},
		{name: "long drive becomes other", drive: strings.Repeat("d", 65), kind: "io", wantDrive: "other", wantKind: "io"},
		{name: "kind with space becomes other", drive: "d1", kind: "bad kind", wantDrive: "d1", wantKind: "other"},
		{name: "empty labels become other", drive: "", kind: "", wantDrive: "other", wantKind: "other"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMetrics()
			m.IncDriveError(tc.drive, tc.kind)
			m.IncDriveError(tc.drive, tc.kind)
			labels := map[string]string{"drive": tc.wantDrive, "kind": tc.wantKind}
			if got := sampleValue(t, m.Registry(), "unio_drive_errors_total", labels); got != 2 {
				t.Fatalf("drive_errors_total = %v, want 2", got)
			}
		})
	}
}

func TestCardinalityIsBounded(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	for i := range 500 {
		m.IncDriveError(strings.Repeat("x", 65+i), "io")
	}
	got := sampleValue(t, m.Registry(), "unio_drive_errors_total", map[string]string{"drive": "other", "kind": "io"})
	if got != 500 {
		t.Fatalf("other series = %v, want 500", got)
	}
	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == "unio_drive_errors_total" && len(mf.GetMetric()) != 1 {
			t.Fatalf("drive_errors_total has %d series, want 1", len(mf.GetMetric()))
		}
	}
}

func TestObserveRaftApply(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		partition string
		want      string
	}{
		{name: "valid partition", partition: "part-7", want: "part-7"},
		{name: "unsafe partition", partition: "../etc", want: "other"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMetrics()
			m.ObserveRaftApply(tc.partition, 4*time.Millisecond)
			labels := map[string]string{"partition": tc.want}
			if got := histogramCount(t, m.Registry(), "unio_raft_apply_duration_seconds", labels); got != 1 {
				t.Fatalf("raft apply sample count = %d, want 1", got)
			}
		})
	}
}

func TestGauges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		set    func(*Metrics)
		family string
		want   float64
	}{
		{name: "bufpool bytes", set: func(m *Metrics) { m.SetBufpoolInUse(4096) }, family: "unio_bufpool_in_use_bytes", want: 4096},
		{name: "bufpool reset", set: func(m *Metrics) { m.SetBufpoolInUse(4096); m.SetBufpoolInUse(0) }, family: "unio_bufpool_in_use_bytes", want: 0},
		{name: "gc queue depth", set: func(m *Metrics) { m.SetGCQueueDepth(12) }, family: "unio_gc_queue_depth", want: 12},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMetrics()
			tc.set(m)
			if got := sampleValue(t, m.Registry(), tc.family, nil); got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.family, got, tc.want)
			}
		})
	}
}

func TestHistogramBuckets(t *testing.T) {
	t.Parallel()
	t.Run("http duration uses the explicit 1ms to 60s ladder", func(t *testing.T) {
		t.Parallel()
		m := NewMetrics()
		m.ObserveHTTPRequest("GetObject", 200, time.Millisecond, 0, 0)
		want := []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
		got := histogramBounds(t, m.Registry(), "unio_http_request_duration_seconds")
		if len(got) != len(want) {
			t.Fatalf("bounds = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("bounds = %v, want %v", got, want)
			}
		}
	})

	erasure := []struct {
		name    string
		family  string
		observe func(*Metrics)
	}{
		{name: "erasure write", family: "unio_erasure_write_duration_seconds", observe: func(m *Metrics) { m.ObserveErasureWrite(time.Millisecond, nil) }},
		{name: "erasure read", family: "unio_erasure_read_duration_seconds", observe: func(m *Metrics) { m.ObserveErasureRead(time.Millisecond, nil) }},
		{name: "raft apply", family: "unio_raft_apply_duration_seconds", observe: func(m *Metrics) { m.ObserveRaftApply("p0", time.Millisecond) }},
	}
	for _, tc := range erasure {
		t.Run(tc.name+" is exponential from 100us to about 10s", func(t *testing.T) {
			t.Parallel()
			m := NewMetrics()
			tc.observe(m)
			got := histogramBounds(t, m.Registry(), tc.family)
			if len(got) < 2 {
				t.Fatalf("bounds = %v, too few", got)
			}
			if math.Abs(got[0]-0.0001) > 1e-12 {
				t.Fatalf("first bound = %v, want 0.0001", got[0])
			}
			for i := 1; i < len(got); i++ {
				if math.Abs(got[i]/got[i-1]-2) > 1e-9 {
					t.Fatalf("bounds not doubling at %d: %v", i, got)
				}
			}
			if last := got[len(got)-1]; last < 10 || last > 20 {
				t.Fatalf("last bound = %v, want about 10 seconds", last)
			}
		})
	}
}
