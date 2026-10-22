package chaos

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Environment variables read by LoadConfig.
const (
	envComposeFile = "UNIO_CHAOS_COMPOSE_FILE"
	envProject     = "UNIO_CHAOS_PROJECT"
	envEndpoints   = "UNIO_CHAOS_ENDPOINTS"
	envBucket      = "UNIO_CHAOS_BUCKET"
	envDuration    = "UNIO_CHAOS_DURATION"
	envSeed        = "UNIO_CHAOS_SEED"
	envAccessKey   = "UNIO_TEST_ACCESS_KEY"
	envSecretKey   = "UNIO_TEST_SECRET_KEY"
)

// Defaults used when the matching variable is unset.
const (
	defaultComposeFile = "../../deploy/compose/cluster-4x4.yml"
	defaultProject     = "unio-4x4"
	defaultBucket      = "unio-chaos"
	defaultDuration    = 2 * time.Minute
)

var (
	projectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	bucketPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
)

// defaultEndpoints returns the host ports of node1 to node4.
func defaultEndpoints() []string {
	return []string{
		"http://localhost:9001",
		"http://localhost:9002",
		"http://localhost:9003",
		"http://localhost:9004",
	}
}

// Config holds the settings of a chaos run. It contains credentials, so log
// individual fields and never the whole value.
type Config struct {
	// ComposeFile is the absolute path of the Compose file of the cluster.
	ComposeFile string
	// Project is the Compose project name, which prefixes volume and network names.
	Project string
	// Endpoints are the S3 base URLs, ordered node1 first.
	Endpoints []string
	// Bucket is the bucket that receives the chaos workload.
	Bucket string
	// AccessKey and SecretKey authenticate the test client.
	AccessKey string
	SecretKey string
	// Duration bounds the random phase of TestDurability.
	Duration time.Duration
	// Seed drives the random schedule and payloads. It is used only when SeedSet is true.
	Seed    uint64
	SeedSet bool
}

// LoadConfig reads a Config from getenv. Unset optional variables take their
// defaults. Invalid values and missing credentials are errors, and the error
// text never includes a credential.
func LoadConfig(getenv func(string) string) (Config, error) {
	var cfg Config

	composeFile, err := filepath.Abs(valueOr(getenv(envComposeFile), defaultComposeFile))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", envComposeFile, err)
	}
	cfg.ComposeFile = composeFile

	cfg.Project = valueOr(getenv(envProject), defaultProject)
	if !projectPattern.MatchString(cfg.Project) {
		return Config{}, fmt.Errorf("%s %q: must match %s", envProject, cfg.Project, projectPattern)
	}

	if raw := getenv(envEndpoints); raw == "" {
		cfg.Endpoints = defaultEndpoints()
	} else {
		endpoints, err := parseEndpoints(raw)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", envEndpoints, err)
		}
		cfg.Endpoints = endpoints
	}

	cfg.Bucket = valueOr(getenv(envBucket), defaultBucket)
	if !bucketPattern.MatchString(cfg.Bucket) {
		return Config{}, fmt.Errorf("%s %q: must match %s", envBucket, cfg.Bucket, bucketPattern)
	}

	if cfg.AccessKey = getenv(envAccessKey); cfg.AccessKey == "" {
		return Config{}, fmt.Errorf("%s is required", envAccessKey)
	}
	if cfg.SecretKey = getenv(envSecretKey); cfg.SecretKey == "" {
		return Config{}, fmt.Errorf("%s is required", envSecretKey)
	}

	cfg.Duration = defaultDuration
	if raw := getenv(envDuration); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("%s %q: want a positive duration such as 2m", envDuration, raw)
		}
		cfg.Duration = d
	}

	if raw := getenv(envSeed); raw != "" {
		seed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("%s %q: want an unsigned integer", envSeed, raw)
		}
		cfg.Seed = seed
		cfg.SeedSet = true
	}
	return cfg, nil
}

// parseEndpoints splits a comma-separated list of base URLs and normalises
// each one to scheme://host.
func parseEndpoints(raw string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		ep := strings.TrimSpace(part)
		u, err := url.Parse(ep)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("invalid endpoint %q: want http(s)://host:port", ep)
		}
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("invalid endpoint %q: must not carry a path, query or fragment", ep)
		}
		out = append(out, u.Scheme+"://"+u.Host)
	}
	return out, nil
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
