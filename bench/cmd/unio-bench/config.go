package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/eukalpia/unio/bench/internal/objstore"
	"github.com/eukalpia/unio/bench/internal/workload"
)

// Environment variables that carry the credentials. Credentials are never
// accepted as flags, so they do not appear in shell history or process lists.
const (
	accessKeyEnv = "UNIO_BENCH_ACCESS_KEY"
	// #nosec G101 -- false positive: this is the name of an environment variable, not a credential value
	secretKeyEnv = "UNIO_BENCH_SECRET_KEY"
)

// Bounds for the numeric flags.
const (
	minConcurrency = 1
	maxConcurrency = 512
	minDuration    = time.Second
	maxListKeys    = 10_000_000
	maxListPage    = 1000
)

// config is the validated command line. It has no fields for credentials.
type config struct {
	endpoint    string
	bucket      string
	workloads   []string
	concurrency int
	duration    time.Duration
	warmup      time.Duration
	listKeys    int
	listPage    int
	out         string
	markdown    string
	pathStyle   bool
	region      string
	seed        uint64
}

// parseConfig parses and validates args. Usage messages go to stderr. A
// request for help returns flag.ErrHelp.
func parseConfig(args []string, stderr io.Writer) (config, error) {
	fs := flag.NewFlagSet("unio-bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c config
	var workloadList string
	fs.StringVar(&c.endpoint, "endpoint", "", "S3 endpoint URL, for example http://localhost:9001 (required)")
	fs.StringVar(&c.bucket, "bucket", "", "bucket to use, created if missing (default unio-bench-<random>)")
	fs.StringVar(&workloadList, "workload", "all", "comma-separated workloads: put-4k,get-4k,put-1m,get-1m,put-64m,get-64m,list,mixed, or all")
	fs.IntVar(&c.concurrency, "concurrency", 8, fmt.Sprintf("parallel workers, %d to %d", minConcurrency, maxConcurrency))
	fs.DurationVar(&c.duration, "duration", 20*time.Second, "measured time per workload, at least 1s")
	fs.DurationVar(&c.warmup, "warmup", 5*time.Second, "unmeasured warmup per workload")
	fs.IntVar(&c.listKeys, "list-keys", 10000, fmt.Sprintf("objects pre-filled for the list workload, up to %d", maxListKeys))
	fs.IntVar(&c.listPage, "list-page", 1000, fmt.Sprintf("MaxKeys per LIST page, 1 to %d", maxListPage))
	fs.StringVar(&c.out, "out", "", "JSON result path (default stdout)")
	fs.StringVar(&c.markdown, "markdown", "", "optional Markdown report path")
	fs.BoolVar(&c.pathStyle, "path-style", true, "use path-style addressing")
	fs.StringVar(&c.region, "region", "us-east-1", "signing region")
	fs.Uint64Var(&c.seed, "seed", 1, "seed for reproducible key and operation choices")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := c.validate(workloadList); err != nil {
		return config{}, err
	}
	return c, nil
}

// validate checks every flag and fills in the default bucket name.
func (c *config) validate(workloadList string) error {
	if err := validateEndpoint(c.endpoint); err != nil {
		return err
	}
	workloads, err := workload.ParseNames(workloadList)
	if err != nil {
		return err
	}
	c.workloads = workloads
	if c.concurrency < minConcurrency || c.concurrency > maxConcurrency {
		return fmt.Errorf("-concurrency must be between %d and %d, got %d", minConcurrency, maxConcurrency, c.concurrency)
	}
	if c.duration < minDuration {
		return fmt.Errorf("-duration must be at least %v, got %v", minDuration, c.duration)
	}
	if c.warmup < 0 {
		return fmt.Errorf("-warmup must not be negative, got %v", c.warmup)
	}
	if c.listKeys < 1 || c.listKeys > maxListKeys {
		return fmt.Errorf("-list-keys must be between 1 and %d, got %d", maxListKeys, c.listKeys)
	}
	if c.listPage < 1 || c.listPage > maxListPage {
		return fmt.Errorf("-list-page must be between 1 and %d, got %d", maxListPage, c.listPage)
	}
	if strings.TrimSpace(c.region) == "" {
		return errors.New("-region must not be empty")
	}
	if err := checkOutputPaths(c.out, c.markdown); err != nil {
		return err
	}
	if c.bucket == "" {
		name, err := defaultBucket()
		if err != nil {
			return err
		}
		c.bucket = name
	}
	return validateBucket(c.bucket)
}

// validateEndpoint requires an absolute http or https URL with a host and no
// userinfo, query or fragment, so the endpoint stored in the report can never
// hold a credential or a presigned signature.
func validateEndpoint(raw string) error {
	if raw == "" {
		return errors.New("-endpoint is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("-endpoint is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("-endpoint scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("-endpoint has no host")
	}
	if u.User != nil {
		return fmt.Errorf("-endpoint must not contain credentials; set %s and %s instead", accessKeyEnv, secretKeyEnv)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("-endpoint must not contain a query or a fragment")
	}
	return nil
}

// defaultBucket returns a bucket name with a random suffix, unique enough for
// a benchmark run.
func defaultBucket() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate bucket name: %w", err)
	}
	return "unio-bench-" + hex.EncodeToString(b[:]), nil
}

// validateBucket applies the length and character rules that S3 bucket names
// share across implementations.
func validateBucket(name string) error {
	if len(name) < 3 || len(name) > 63 {
		return fmt.Errorf("-bucket must be 3 to 63 characters, got %d", len(name))
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
		default:
			return fmt.Errorf("-bucket %q may contain only lowercase letters, digits, hyphens and dots", name)
		}
	}
	return nil
}

// readCredentials reads the keys from the environment through getenv. Error
// messages name the variable but never its value.
func readCredentials(getenv func(string) string) (objstore.Credentials, error) {
	creds := objstore.Credentials{
		AccessKey: getenv(accessKeyEnv),
		SecretKey: getenv(secretKeyEnv),
	}
	if creds.AccessKey == "" {
		return objstore.Credentials{}, fmt.Errorf("%s is not set; export the access key in the environment", accessKeyEnv)
	}
	if creds.SecretKey == "" {
		return objstore.Credentials{}, fmt.Errorf("%s is not set; export the secret key in the environment", secretKeyEnv)
	}
	return creds, nil
}
