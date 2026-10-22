// Package config loads and validates the configuration of a Unio node.
//
// Sources apply in this order, later ones overriding earlier ones:
// built-in defaults, the YAML file (strict: unknown keys are errors), the
// UNIO_* environment variables, and explicitly set command-line flags.
// Secrets are the exception: they come from the environment only (directly or
// through a *_FILE variable), never from the YAML file or from flags, so they
// never appear in a file that may be committed or in the process table.
//
// Validation reports every problem at once. Warnings (for example plain HTTP
// on the S3 endpoint) are returned separately from errors.
package config

import "time"

// Config is the complete configuration of one node.
type Config struct {
	Node     NodeConfig     `yaml:"node"`
	TLS      TLSConfig      `yaml:"tls"`
	Cluster  ClusterConfig  `yaml:"cluster"`
	Erasure  ErasureConfig  `yaml:"erasure"`
	Metadata MetadataConfig `yaml:"metadata"`
	Storage  StorageConfig  `yaml:"storage"`
	Limits   LimitsConfig   `yaml:"limits"`
	Timeouts TimeoutsConfig `yaml:"timeouts"`
	Auth     AuthConfig     `yaml:"auth"`
	Log      LogConfig      `yaml:"log"`
	Metrics  MetricsConfig  `yaml:"metrics"`
}

// NodeConfig identifies this node and where it listens.
type NodeConfig struct {
	// ID is this node's name. It must equal the host part of the drive
	// endpoints that belong to this node, for example node1.
	ID string `yaml:"id"`
	// S3Address is the listen address of the S3 API, for example ":9000".
	S3Address string `yaml:"s3_address"`
	// InternodeAddress is the listen address of the internode protocol, for
	// example ":9100".
	InternodeAddress string `yaml:"internode_address"`
}

// TLSConfig holds the optional certificate of the S3 endpoint.
type TLSConfig struct {
	// CertFile is the PEM certificate chain of the S3 endpoint.
	CertFile string `yaml:"cert_file"`
	// KeyFile is the PEM private key matching CertFile.
	KeyFile string `yaml:"key_file"`
	// Require forbids starting the S3 endpoint without TLS.
	Require bool `yaml:"require"`
}

// ClusterConfig describes the drives of the whole cluster.
type ClusterConfig struct {
	// Endpoints are drive endpoint patterns, possibly with ellipses, for
	// example https://node{1...4}:9100/data{1...4}. Together they list every
	// drive of every node, including this one.
	Endpoints []string `yaml:"endpoints"`
	// Secret is the cluster secret from which the internode CA is derived.
	// It must come from the environment (UNIO_CLUSTER_SECRET).
	Secret Secret `yaml:"secret"`
	// Drives is the expansion of Endpoints. Load fills it in; it is not read
	// from the file.
	Drives []Endpoint `yaml:"-"`
}

// ErasureConfig controls erasure-set layout. Zero values mean "automatic".
type ErasureConfig struct {
	// SetDriveCount is the number of drives in each erasure set (4 to 16, or
	// 1 for a single-drive development node). Zero picks the largest size in
	// 4 to 16 that divides the total drive count.
	SetDriveCount int `yaml:"set_drive_count"`
	// Parity is the number of parity shards M per set. Zero picks 2 for sets
	// of up to 8 drives and 4 for larger sets.
	Parity int `yaml:"parity"`
}

// MetadataConfig controls the metadata plane.
type MetadataConfig struct {
	// Partitions is the number of metadata partitions. It is fixed at
	// cluster creation. The default is 16.
	Partitions int `yaml:"partitions"`
}

// StorageConfig controls the data tiers and memory use.
type StorageConfig struct {
	// InlineThreshold is the largest object stored inline in metadata. It
	// may be 0 (disabled) up to 64 KiB.
	InlineThreshold Size `yaml:"inline_threshold"`
	// MemoryBudget bounds the buffer pool. Requests wait for memory rather
	// than allocating past it.
	MemoryBudget Size `yaml:"memory_budget"`
}

// LimitsConfig bounds concurrency.
type LimitsConfig struct {
	// MaxRequests is the number of S3 requests processed at the same time.
	MaxRequests int `yaml:"max_requests"`
	// RequestQueue is the number of requests waiting for a slot. Beyond it,
	// clients get 503 SlowDown.
	RequestQueue int `yaml:"request_queue"`
	// BackgroundConcurrency bounds GC, compaction and repair jobs.
	BackgroundConcurrency int `yaml:"background_concurrency"`
}

// TimeoutsConfig bounds how long connections and requests may take.
type TimeoutsConfig struct {
	// ReadHeader bounds how long a client may take to send request headers.
	ReadHeader time.Duration `yaml:"read_header"`
	// Read bounds reading a whole request, including its body.
	Read time.Duration `yaml:"read"`
	// Write bounds writing a whole response.
	Write time.Duration `yaml:"write"`
	// Idle bounds how long a keep-alive connection may stay idle.
	Idle time.Duration `yaml:"idle"`
	// Shutdown bounds the graceful drain on SIGINT or SIGTERM.
	Shutdown time.Duration `yaml:"shutdown"`
}

// AuthConfig holds the root credentials. They have no defaults and must be
// provided through the environment.
type AuthConfig struct {
	// RootUser is the access key ID of the root account.
	RootUser Secret `yaml:"root_user"`
	// RootPassword is the secret access key of the root account.
	RootPassword Secret `yaml:"root_password"`
}

// LogConfig controls the structured log output.
type LogConfig struct {
	// Level is one of debug, info, warn or error.
	Level string `yaml:"level"`
	// Format is json or text.
	Format string `yaml:"format"`
}

// MetricsConfig controls the token-protected observability endpoints.
type MetricsConfig struct {
	// Token is the bearer token for /unio/metrics and, when enabled, pprof.
	// It must come from the environment and differ from the cluster secret.
	Token Secret `yaml:"token"`
	// Pprof enables the pprof endpoints. They are off by default.
	Pprof bool `yaml:"pprof"`
}

// LocalDrives returns the drives owned by this node, that is, the endpoints
// whose host equals Node.ID. It returns nil before Load has resolved the
// drives.
func (c *Config) LocalDrives() []Endpoint {
	var local []Endpoint
	for _, d := range c.Cluster.Drives {
		if d.Host == c.Node.ID {
			local = append(local, d)
		}
	}
	return local
}

// TLSEnabled reports whether the S3 endpoint is configured to serve HTTPS.
func (c *Config) TLSEnabled() bool {
	return c.TLS.CertFile != "" && c.TLS.KeyFile != ""
}
