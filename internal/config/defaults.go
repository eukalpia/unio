package config

import "time"

// Built-in defaults. They are the values used when no source sets a key.
const (
	// DefaultS3Address is the default S3 listen address.
	DefaultS3Address = ":9000"
	// DefaultInternodeAddress is the default internode listen address.
	DefaultInternodeAddress = ":9100"
	// DefaultRegion is the default S3 region of the node.
	DefaultRegion = "us-east-1"
	// DefaultPprofAddress is the default loopback listen address of pprof.
	DefaultPprofAddress = "127.0.0.1:9190"
	// DefaultPartitions is the default number of metadata partitions.
	DefaultPartitions = 16
	// DefaultInlineThreshold is the default largest inline object (ADR-0005).
	DefaultInlineThreshold = 4 * kiB
	// DefaultMemoryBudget is the default buffer memory budget.
	DefaultMemoryBudget = 1 * giB
	// DefaultMaxRequests is the default number of concurrent S3 requests.
	DefaultMaxRequests = 1024
	// DefaultRequestQueue is the default number of queued S3 requests.
	DefaultRequestQueue = 4096
	// DefaultBackgroundConcurrency is the default number of background jobs.
	DefaultBackgroundConcurrency = 4
	// DefaultReadHeaderTimeout is the default header read timeout.
	DefaultReadHeaderTimeout = 10 * time.Second
	// DefaultReadIdleTimeout is the default bound on a stalled request read.
	DefaultReadIdleTimeout = 60 * time.Second
	// DefaultWriteTimeout is the default cap on one response write. Zero means
	// no cap.
	DefaultWriteTimeout = 0
	// DefaultWriteIdleTimeout is the default bound on a stalled response write.
	DefaultWriteIdleTimeout = 60 * time.Second
	// DefaultIdleTimeout is the default keep-alive idle timeout.
	DefaultIdleTimeout = 2 * time.Minute
	// DefaultShutdownTimeout is the default graceful shutdown deadline.
	DefaultShutdownTimeout = 30 * time.Second
	// DefaultLogLevel is the default log level.
	DefaultLogLevel = "info"
	// DefaultLogFormat is the default log format.
	DefaultLogFormat = "json"
)

// Defaults returns the configuration before any source is applied. Keys
// with no default (node id, endpoints, secrets) are left empty and must be
// set by the operator.
func Defaults() Config {
	return Config{
		Node: NodeConfig{
			S3Address:        DefaultS3Address,
			InternodeAddress: DefaultInternodeAddress,
			Region:           DefaultRegion,
		},
		Metadata: MetadataConfig{Partitions: DefaultPartitions},
		Storage: StorageConfig{
			InlineThreshold: DefaultInlineThreshold,
			MemoryBudget:    DefaultMemoryBudget,
		},
		Limits: LimitsConfig{
			MaxRequests:           DefaultMaxRequests,
			RequestQueue:          DefaultRequestQueue,
			BackgroundConcurrency: DefaultBackgroundConcurrency,
		},
		Timeouts: TimeoutsConfig{
			ReadHeader: DefaultReadHeaderTimeout,
			ReadIdle:   DefaultReadIdleTimeout,
			Write:      DefaultWriteTimeout,
			WriteIdle:  DefaultWriteIdleTimeout,
			Idle:       DefaultIdleTimeout,
			Shutdown:   DefaultShutdownTimeout,
		},
		Log:     LogConfig{Level: DefaultLogLevel, Format: DefaultLogFormat},
		Metrics: MetricsConfig{PprofAddress: DefaultPprofAddress},
	}
}
