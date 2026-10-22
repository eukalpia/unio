package config

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Bounds enforced by Validate.
const (
	minErasureSetDrives   = 4
	maxErasureSetDrives   = 16
	maxMetadataPartitions = 1024
	maxInlineThreshold    = 64 * kiB
	minMemoryBudget       = 64 * miB
	maxRequests           = 100000
	maxRequestQueue       = 1000000
	maxBackgroundJobs     = 1024
	maxTimeout            = 24 * time.Hour
	twoNodes              = 2
)

// report collects errors and warnings while validation runs.
type report struct {
	errs     []error
	warnings []string
}

// errorf records a validation error.
func (r *report) errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf(format, args...))
}

// warnf records a warning.
func (r *report) warnf(format string, args ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
}

// Validate checks c and returns the warnings and the errors. Every problem is
// reported, not just the first, and each message names the setting and the
// environment variable or flag that fixes it. Validate does not modify c.
func Validate(c *Config) ([]string, error) {
	var r report
	checkNode(c, &r)
	drives := checkCluster(c, &r)
	checkErasure(c, drives, &r)
	checkMetadata(c, &r)
	checkStorage(c, &r)
	checkLimits(c, &r)
	checkTimeouts(c, &r)
	checkTLS(c, &r)
	checkAuth(c, &r)
	checkLog(c, &r)
	checkMetrics(c, &r)
	sort.Strings(r.warnings)
	if len(r.errs) > 0 {
		return r.warnings, errors.Join(r.errs...)
	}
	return r.warnings, nil
}

// checkNode validates the node identity and listen addresses.
func checkNode(c *Config, r *report) {
	if c.Node.ID == "" {
		r.errorf("node.id is required: set UNIO_NODE_ID to this node's name, which must equal the host of its endpoints (for example node1)")
	} else if !validHost(c.Node.ID) {
		r.errorf("node.id %q is not a valid host name; use letters, digits, '-' and '.'", c.Node.ID)
	}
	s3Port, err := listenPort(c.Node.S3Address)
	if err != nil {
		r.errorf("node.s3_address: %v (UNIO_S3_ADDRESS)", err)
	}
	internodePort, err := listenPort(c.Node.InternodeAddress)
	if err != nil {
		r.errorf("node.internode_address: %v (UNIO_INTERNODE_ADDRESS)", err)
	}
	if err == nil && s3Port == internodePort {
		r.errorf("node.s3_address and node.internode_address must use different ports (both are %d)", s3Port)
	}
}

// checkCluster validates the endpoints, the cluster secret and node membership.
func checkCluster(c *Config, r *report) []Endpoint {
	internodePort, portErr := listenPort(c.Node.InternodeAddress)
	if len(c.Cluster.Endpoints) == 0 {
		r.errorf("cluster.endpoints is required: list the drives of every node, for example https://node{1...4}:9100/data{1...4} (UNIO_ENDPOINTS)")
	}
	drives, err := ExpandEndpoints(c.Cluster.Endpoints)
	if err != nil {
		r.errorf("cluster.endpoints: %v", err)
		drives = nil
	}
	checkClusterSecret(c, r)
	if len(drives) == 0 {
		return nil
	}
	checkSchemes(c, drives, r)
	if c.Cluster.InsecureInternode {
		r.warnf("INSECURE INTERNODE TRAFFIC: cluster.insecure_internode is enabled. Traffic between nodes is neither encrypted nor authenticated. Use this for benchmarks and development only.")
	}
	local := localDrives(c.Node.ID, drives)
	if c.Node.ID != "" && len(local) == 0 {
		r.errorf("no endpoint belongs to node %q: the host of at least one endpoint must equal node.id (endpoint hosts: %s)", c.Node.ID, strings.Join(hosts(drives), ", "))
	}
	if portErr == nil {
		checkLocalPorts(local, internodePort, r)
	}
	checkNodeCount(drives, r)
	return drives
}

// checkClusterSecret validates the cluster secret from the environment.
func checkClusterSecret(c *Config, r *report) {
	if !c.Cluster.Secret.IsSet() {
		r.errorf("cluster.secret is not set: set UNIO_CLUSTER_SECRET or UNIO_CLUSTER_SECRET_FILE (at least %d characters; unio init generates one)", MinSecretLength)
		return
	}
	if err := CheckSecretValue(c.Cluster.Secret.Expose()); err != nil {
		r.errorf("cluster.secret %v (UNIO_CLUSTER_SECRET)", err)
	}
}

// checkSchemes requires https endpoints unless insecure internode traffic is explicitly allowed.
func checkSchemes(c *Config, drives []Endpoint, r *report) {
	if c.Cluster.InsecureInternode {
		return
	}
	for _, d := range drives {
		if d.Scheme != "https" {
			r.errorf("endpoint %s uses plain http; internode endpoints must use https (cluster.insecure_internode is for development only)", d)
			return
		}
	}
}

// checkLocalPorts requires local endpoints to use the internode port this node listens on.
func checkLocalPorts(local []Endpoint, internodePort int, r *report) {
	for _, d := range local {
		if d.Port != internodePort {
			r.errorf("endpoint %s uses port %d but node.internode_address listens on %d; they must match", d, d.Port, internodePort)
			return
		}
	}
}

// checkNodeCount warns about topologies with weak fault tolerance, and about
// uneven drive counts across nodes.
func checkNodeCount(drives []Endpoint, r *report) {
	counts := make(map[string]int)
	for _, d := range drives {
		counts[d.Host]++
	}
	switch {
	case len(counts) == 1 && len(drives) == 1:
		r.warnf("single drive: development mode with no erasure coding and no redundancy")
	case len(counts) == twoNodes:
		r.warnf("two nodes: metadata is not fault tolerant; losing either node stops metadata writes")
	}
	first := -1
	for _, n := range counts {
		if first == -1 {
			first = n
		} else if n != first {
			r.warnf("nodes hold unequal numbers of drives; erasure sets will be spread unevenly")
			return
		}
	}
}

// checkErasure validates erasure-set size and parity against the drive count.
func checkErasure(c *Config, drives []Endpoint, r *report) {
	total := len(drives)
	if total == 0 {
		return
	}
	if total == 1 {
		if c.Erasure.SetDriveCount != 0 && c.Erasure.SetDriveCount != 1 {
			r.errorf("erasure.set_drive_count must be 0 or 1 for a single drive (UNIO_ERASURE_SET_DRIVE_COUNT)")
		}
		if c.Erasure.Parity != 0 {
			r.errorf("erasure.parity must be 0 for a single drive, which has no erasure coding (UNIO_ERASURE_PARITY)")
		}
		return
	}
	set := c.Erasure.SetDriveCount
	if set == 0 {
		auto, err := AutoSetDriveCount(total)
		if err != nil {
			r.errorf("erasure: %v (set erasure.set_drive_count to choose a size)", err)
			return
		}
		set = auto
	} else if set < minErasureSetDrives || set > maxErasureSetDrives || total%set != 0 {
		r.errorf("erasure.set_drive_count %d must be between %d and %d and divide the total drive count %d (UNIO_ERASURE_SET_DRIVE_COUNT)",
			set, minErasureSetDrives, maxErasureSetDrives, total)
		return
	}
	switch parity := c.Erasure.Parity; {
	case parity < 0:
		r.errorf("erasure.parity must not be negative (UNIO_ERASURE_PARITY)")
	case parity > set/2:
		r.errorf("erasure.parity %d is out of range: it must be between 1 and %d for erasure sets of %d drives (UNIO_ERASURE_PARITY)",
			parity, set/2, set)
	}
}

// checkMetadata validates the metadata partition count.
func checkMetadata(c *Config, r *report) {
	if p := c.Metadata.Partitions; p < 1 || p > maxMetadataPartitions {
		r.errorf("metadata.partitions %d must be between 1 and %d (UNIO_METADATA_PARTITIONS)", p, maxMetadataPartitions)
	}
}

// checkStorage validates the inline threshold and the memory budget.
func checkStorage(c *Config, r *report) {
	if t := c.Storage.InlineThreshold; t < 0 || t > maxInlineThreshold {
		r.errorf("storage.inline_threshold %s must be between 0 and %s (UNIO_INLINE_THRESHOLD)",
			t, Size(maxInlineThreshold))
	}
	if b := c.Storage.MemoryBudget; b < minMemoryBudget {
		r.errorf("storage.memory_budget %s must be at least %s (UNIO_MEMORY_BUDGET)", b, Size(minMemoryBudget))
	}
}

// checkLimits validates concurrency limits.
func checkLimits(c *Config, r *report) {
	l := c.Limits
	if l.MaxRequests < 1 || l.MaxRequests > maxRequests {
		r.errorf("limits.max_requests %d must be between 1 and %d (UNIO_MAX_REQUESTS)", l.MaxRequests, maxRequests)
	}
	if l.RequestQueue < 0 || l.RequestQueue > maxRequestQueue {
		r.errorf("limits.request_queue %d must be between 0 and %d (UNIO_REQUEST_QUEUE)", l.RequestQueue, maxRequestQueue)
	}
	if l.BackgroundConcurrency < 1 || l.BackgroundConcurrency > maxBackgroundJobs {
		r.errorf("limits.background_concurrency %d must be between 1 and %d (UNIO_BACKGROUND_CONCURRENCY)",
			l.BackgroundConcurrency, maxBackgroundJobs)
	}
}

// checkTimeouts validates that every timeout is positive and not absurdly large.
func checkTimeouts(c *Config, r *report) {
	t := c.Timeouts
	for _, item := range []struct {
		name string
		env  string
		d    time.Duration
	}{
		{"timeouts.read_header", "UNIO_READ_HEADER_TIMEOUT", t.ReadHeader},
		{"timeouts.read", "UNIO_READ_TIMEOUT", t.Read},
		{"timeouts.write", "UNIO_WRITE_TIMEOUT", t.Write},
		{"timeouts.idle", "UNIO_IDLE_TIMEOUT", t.Idle},
		{"timeouts.shutdown", "UNIO_SHUTDOWN_TIMEOUT", t.Shutdown},
	} {
		if item.d <= 0 || item.d > maxTimeout {
			r.errorf("%s %s must be greater than 0 and at most %s (%s)", item.name, item.d, maxTimeout, item.env)
		}
	}
}

// checkTLS validates the S3 endpoint certificate settings.
func checkTLS(c *Config, r *report) {
	cert, key := c.TLS.CertFile, c.TLS.KeyFile
	if (cert == "") != (key == "") {
		r.errorf("tls.cert_file and tls.key_file must be set together (UNIO_TLS_CERT_FILE, UNIO_TLS_KEY_FILE)")
	}
	for _, p := range []string{cert, key} {
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err != nil {
			r.errorf("tls file %s is not readable: %v", p, err)
		} else if !info.Mode().IsRegular() {
			r.errorf("tls file %s is not a regular file", p)
		}
	}
	if c.TLS.Require && !c.TLSEnabled() {
		r.errorf("tls.require is set but no certificate is configured: set UNIO_TLS_CERT_FILE and UNIO_TLS_KEY_FILE, or unset UNIO_REQUIRE_TLS")
	}
	if !c.TLSEnabled() {
		r.warnf("the S3 endpoint will serve plain HTTP: set UNIO_TLS_CERT_FILE and UNIO_TLS_KEY_FILE for HTTPS, or UNIO_REQUIRE_TLS=true to forbid plain HTTP")
	}
}

// checkAuth validates the root credentials, which must be set and strong.
func checkAuth(c *Config, r *report) {
	user := c.Auth.RootUser.Expose()
	password := c.Auth.RootPassword.Expose()
	if user == "" {
		r.errorf("auth.root_user is not set: set UNIO_ROOT_USER or UNIO_ROOT_USER_FILE (unio init generates one)")
	} else if err := CheckRootUser(user); err != nil {
		r.errorf("auth.root_user %v (UNIO_ROOT_USER)", err)
	}
	if password == "" {
		r.errorf("auth.root_password is not set: set UNIO_ROOT_PASSWORD or UNIO_ROOT_PASSWORD_FILE (unio init generates one)")
	} else if err := CheckRootPassword(user, password); err != nil {
		r.errorf("auth.root_password %v (UNIO_ROOT_PASSWORD)", err)
	}
}

// checkLog validates the log level and format.
func checkLog(c *Config, r *report) {
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		r.errorf("log.level %q must be one of debug, info, warn or error (UNIO_LOG_LEVEL)", c.Log.Level)
	}
	switch strings.ToLower(c.Log.Format) {
	case "json", "text":
	default:
		r.errorf("log.format %q must be json or text (UNIO_LOG_FORMAT)", c.Log.Format)
	}
}

// checkMetrics validates the metrics token, which must be strong and distinct from the cluster secret.
func checkMetrics(c *Config, r *report) {
	token := c.Metrics.Token
	if !token.IsSet() {
		r.errorf("metrics.token is not set: set UNIO_METRICS_TOKEN or UNIO_METRICS_TOKEN_FILE (at least %d characters)", MinSecretLength)
		return
	}
	if err := CheckSecretValue(token.Expose()); err != nil {
		r.errorf("metrics.token %v (UNIO_METRICS_TOKEN)", err)
	}
	if c.Cluster.Secret.IsSet() && subtle.ConstantTimeCompare([]byte(token.Expose()), []byte(c.Cluster.Secret.Expose())) == 1 {
		r.errorf("metrics.token must differ from cluster.secret (UNIO_METRICS_TOKEN)")
	}
}

// AutoSetDriveCount returns the erasure-set size chosen when
// erasure.set_drive_count is 0: the largest size between 4 and 16 that divides
// total. A single drive gets a set of one (development mode).
func AutoSetDriveCount(total int) (int, error) {
	if total == 1 {
		return 1, nil
	}
	for s := min(maxErasureSetDrives, total); s >= minErasureSetDrives; s-- {
		if total%s == 0 {
			return s, nil
		}
	}
	return 0, fmt.Errorf("cannot form erasure sets of %d to %d drives from %d drives; use a total that has a divisor in that range",
		minErasureSetDrives, maxErasureSetDrives, total)
}

// DefaultParity returns the parity used when erasure.parity is 0: 2 for sets
// of up to 8 drives, 4 for larger sets, and 0 for a single drive.
func DefaultParity(setDrives int) int {
	switch {
	case setDrives <= 1:
		return 0
	case setDrives <= 8:
		return 2
	default:
		return 4
	}
}

// listenPort validates a listen address and returns its port.
func listenPort(addr string) (int, error) {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("%q must have the form :port or host:port", addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port in %q must be between 1 and 65535", addr)
	}
	return port, nil
}

// localDrives returns the endpoints whose host equals nodeID.
func localDrives(nodeID string, drives []Endpoint) []Endpoint {
	var out []Endpoint
	for _, d := range drives {
		if d.Host == nodeID {
			out = append(out, d)
		}
	}
	return out
}

// hosts returns the distinct host names in drives, sorted.
func hosts(drives []Endpoint) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, d := range drives {
		if _, ok := seen[d.Host]; !ok {
			seen[d.Host] = struct{}{}
			out = append(out, d.Host)
		}
	}
	sort.Strings(out)
	return out
}
