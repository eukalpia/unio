package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// setting binds one non-secret key to its command-line flag, its environment
// variable and the function that applies a textual value to a Config.
type setting struct {
	flag   string
	env    string
	usage  string
	isBool bool
	set    func(c *Config, v string) error
}

// FlagSpec describes one command-line flag. The CLI registers flags from these
// so that the flag names, environment names and help text live in one place.
type FlagSpec struct {
	// Name is the flag name without leading dashes, for example "node-id".
	Name string
	// Env is the environment variable that sets the same key.
	Env string
	// Usage is the help text.
	Usage string
	// Bool reports whether the flag takes no value (--flag or --flag=false).
	Bool bool
	// Hidden marks a removed flag. It is registered only so that using it
	// reports the removal, and it is left out of the help text.
	Hidden bool
}

// FlagSpecs returns the flags that set configuration keys, in a stable order,
// followed by the hidden flags of removed settings. Secrets have no flags on
// purpose: a flag value is visible in the process table.
func FlagSpecs() []FlagSpec {
	var specs []FlagSpec
	for _, s := range settings() {
		specs = append(specs, FlagSpec{Name: s.flag, Env: s.env, Usage: s.usage, Bool: s.isBool})
	}
	for _, r := range removedSettings() {
		specs = append(specs, FlagSpec{Name: r.flag, Env: r.env, Usage: "removed setting", Bool: r.isBool, Hidden: true})
	}
	return specs
}

// settings returns the table of non-secret keys. It is rebuilt on each call
// so that no package-level state is needed.
func settings() []setting {
	return []setting{
		strSetting("node-id", "UNIO_NODE_ID", "name of this node; must match the host of its endpoints",
			func(c *Config) *string { return &c.Node.ID }),
		strSetting("s3-address", "UNIO_S3_ADDRESS", "S3 listen address",
			func(c *Config) *string { return &c.Node.S3Address }),
		strSetting("internode-address", "UNIO_INTERNODE_ADDRESS", "internode listen address",
			func(c *Config) *string { return &c.Node.InternodeAddress }),
		strSetting("region", "UNIO_REGION", "S3 region of this node, e.g. us-east-1",
			func(c *Config) *string { return &c.Node.Region }),
		listSetting("domains", "UNIO_DOMAINS", "comma-separated virtual-host suffixes, e.g. s3.example.com",
			func(c *Config) *[]string { return &c.Node.Domains }),
		listSetting("endpoints", "UNIO_ENDPOINTS", "comma-separated drive endpoint patterns, e.g. https://node{1...4}:9100/data{1...4}",
			func(c *Config) *[]string { return &c.Cluster.Endpoints }),
		strSetting("tls-cert-file", "UNIO_TLS_CERT_FILE", "PEM certificate chain for the S3 endpoint",
			func(c *Config) *string { return &c.TLS.CertFile }),
		strSetting("tls-key-file", "UNIO_TLS_KEY_FILE", "PEM private key for the S3 endpoint",
			func(c *Config) *string { return &c.TLS.KeyFile }),
		boolSetting("require-tls", "UNIO_REQUIRE_TLS", "refuse to start the S3 endpoint without TLS",
			func(c *Config) *bool { return &c.TLS.Require }),
		intSetting("erasure-set-drive-count", "UNIO_ERASURE_SET_DRIVE_COUNT", "drives per erasure set (0 = automatic)",
			func(c *Config) *int { return &c.Erasure.SetDriveCount }),
		intSetting("erasure-parity", "UNIO_ERASURE_PARITY", "parity shards per set (0 = automatic)",
			func(c *Config) *int { return &c.Erasure.Parity }),
		intSetting("metadata-partitions", "UNIO_METADATA_PARTITIONS", "number of metadata partitions (fixed at cluster creation)",
			func(c *Config) *int { return &c.Metadata.Partitions }),
		strSetting("metadata-dir", "UNIO_METADATA_DIR", "absolute directory of the metadata store (default: .unio/meta under the first local drive)",
			func(c *Config) *string { return &c.Metadata.Dir }),
		sizeSetting("inline-threshold", "UNIO_INLINE_THRESHOLD", "largest object stored inline in metadata, e.g. 4KiB",
			func(c *Config) *Size { return &c.Storage.InlineThreshold }),
		sizeSetting("memory-budget", "UNIO_MEMORY_BUDGET", "memory budget for I/O buffers, e.g. 1GiB",
			func(c *Config) *Size { return &c.Storage.MemoryBudget }),
		strSetting("storage-sync", "UNIO_STORAGE_SYNC", "when drive writes reach stable storage: always (each write) or batch (in groups; a power loss can lose the last batch)",
			func(c *Config) *string { return &c.Storage.Sync }),
		intSetting("max-requests", "UNIO_MAX_REQUESTS", "concurrent S3 requests",
			func(c *Config) *int { return &c.Limits.MaxRequests }),
		intSetting("request-queue", "UNIO_REQUEST_QUEUE", "S3 requests waiting for a slot before 503 SlowDown",
			func(c *Config) *int { return &c.Limits.RequestQueue }),
		intSetting("background-concurrency", "UNIO_BACKGROUND_CONCURRENCY", "concurrent background jobs (GC, compaction, repair)",
			func(c *Config) *int { return &c.Limits.BackgroundConcurrency }),
		durSetting("read-header-timeout", "UNIO_READ_HEADER_TIMEOUT", "time allowed to read request headers",
			func(c *Config) *time.Duration { return &c.Timeouts.ReadHeader }),
		durSetting("read-idle-timeout", "UNIO_READ_IDLE_TIMEOUT", "time a request read may stall without data",
			func(c *Config) *time.Duration { return &c.Timeouts.ReadIdle }),
		durSetting("write-timeout", "UNIO_WRITE_TIMEOUT", "cap on the time to write one response; 0 means no cap",
			func(c *Config) *time.Duration { return &c.Timeouts.Write }),
		durSetting("write-idle-timeout", "UNIO_WRITE_IDLE_TIMEOUT", "time a response write may stall without the client accepting data",
			func(c *Config) *time.Duration { return &c.Timeouts.WriteIdle }),
		durSetting("idle-timeout", "UNIO_IDLE_TIMEOUT", "idle keep-alive connection timeout",
			func(c *Config) *time.Duration { return &c.Timeouts.Idle }),
		durSetting("shutdown-timeout", "UNIO_SHUTDOWN_TIMEOUT", "graceful shutdown deadline",
			func(c *Config) *time.Duration { return &c.Timeouts.Shutdown }),
		strSetting("log-level", "UNIO_LOG_LEVEL", "log level: debug, info, warn or error",
			func(c *Config) *string { return &c.Log.Level }),
		strSetting("log-format", "UNIO_LOG_FORMAT", "log format: json or text",
			func(c *Config) *string { return &c.Log.Format }),
		boolSetting("pprof", "UNIO_PPROF", "enable the token-protected pprof endpoints",
			func(c *Config) *bool { return &c.Metrics.Pprof }),
		strSetting("pprof-address", "UNIO_PPROF_ADDRESS", "loopback listen address of the pprof endpoints, e.g. 127.0.0.1:9190",
			func(c *Config) *string { return &c.Metrics.PprofAddress }),
	}
}

// strSetting builds a setting for a string key.
func strSetting(flag, env, usage string, field func(*Config) *string) setting {
	return setting{flag: flag, env: env, usage: usage, set: func(c *Config, v string) error {
		*field(c) = v
		return nil
	}}
}

// listSetting builds a setting for a comma-separated list of strings.
func listSetting(flag, env, usage string, field func(*Config) *[]string) setting {
	return setting{flag: flag, env: env, usage: usage, set: func(c *Config, v string) error {
		*field(c) = splitList(v)
		return nil
	}}
}

// intSetting builds a setting for a decimal integer.
func intSetting(flag, env, usage string, field func(*Config) *int) setting {
	return setting{flag: flag, env: env, usage: usage, set: func(c *Config, v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("expected an integer, got %q", v)
		}
		*field(c) = n
		return nil
	}}
}

// boolSetting builds a setting for a boolean.
func boolSetting(flag, env, usage string, field func(*Config) *bool) setting {
	return setting{flag: flag, env: env, usage: usage, isBool: true, set: func(c *Config, v string) error {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("expected true or false, got %q", v)
		}
		*field(c) = b
		return nil
	}}
}

// sizeSetting builds a setting for a byte size such as 4KiB.
func sizeSetting(flag, env, usage string, field func(*Config) *Size) setting {
	return setting{flag: flag, env: env, usage: usage, set: func(c *Config, v string) error {
		n, err := ParseSize(v)
		if err != nil {
			return err
		}
		*field(c) = n
		return nil
	}}
}

// durSetting builds a setting for a duration such as 30s.
func durSetting(flag, env, usage string, field func(*Config) *time.Duration) setting {
	return setting{flag: flag, env: env, usage: usage, set: func(c *Config, v string) error {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("expected a duration such as 30s, got %q", v)
		}
		*field(c) = d
		return nil
	}}
}

// splitList splits a comma-separated value, trimming blanks and dropping empty items.
func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
