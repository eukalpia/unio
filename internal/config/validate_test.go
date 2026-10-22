package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	tlsCert := writeFile(t, "cert.pem", "cert")
	tlsKey := writeFile(t, "key.pem", "key")
	tests := []struct {
		name         string
		mutate       func(c *Config)
		wantErr      string // substring of the error, empty for success
		wantWarnings string // substring of a warning, empty for none expected
	}{
		{name: "valid cluster"},
		{name: "node id missing", mutate: func(c *Config) { c.Node.ID = "" }, wantErr: "node.id is required"},
		{name: "node id invalid", mutate: func(c *Config) { c.Node.ID = "no_de" }, wantErr: "not a valid host name"},
		{name: "node id matches no endpoint", mutate: func(c *Config) { c.Node.ID = "node9" }, wantErr: "no endpoint belongs to node \"node9\""},
		{name: "s3 address without port", mutate: func(c *Config) { c.Node.S3Address = "9000" }, wantErr: "node.s3_address"},
		{name: "s3 port out of range", mutate: func(c *Config) { c.Node.S3Address = ":70000" }, wantErr: "node.s3_address"},
		{name: "same ports", mutate: func(c *Config) { c.Node.S3Address = ":9100" }, wantErr: "must use different ports"},
		{name: "internode port mismatch", mutate: func(c *Config) { c.Node.InternodeAddress = ":9200" }, wantErr: "listens on 9200"},
		{name: "no endpoints", mutate: func(c *Config) { c.Cluster.Endpoints = nil }, wantErr: "cluster.endpoints is required"},
		{name: "bad endpoint pattern", mutate: func(c *Config) { c.Cluster.Endpoints = []string{"https://node{1...x}:9100/d"} }, wantErr: "cluster.endpoints"},
		{name: "plain http without insecure flag", mutate: func(c *Config) {
			c.Cluster.Endpoints = []string{"http://node{1...4}:9100/data{1...4}"}
		}, wantErr: "internode traffic is always mutual TLS"},
		{name: "one plain http endpoint among https ones", mutate: func(c *Config) {
			c.Cluster.Endpoints = []string{"https://node{1...4}:9100/data{1...4}", "http://node5:9100/data1"}
		}, wantErr: "endpoint http://node5:9100/data1 uses plain http"},
		{name: "cluster secret missing", mutate: func(c *Config) { c.Cluster.Secret = Secret{} }, wantErr: "cluster.secret is not set"},
		{name: "cluster secret too short", mutate: func(c *Config) { c.Cluster.Secret = NewSecret("short") }, wantErr: "cluster.secret must be at least 32"},
		{name: "erasure set does not divide drives", mutate: func(c *Config) { c.Erasure.SetDriveCount = 5 }, wantErr: "must be between 4 and 16 and divide"},
		{name: "erasure set below minimum", mutate: func(c *Config) { c.Erasure.SetDriveCount = 2 }, wantErr: "must be between 4 and 16"},
		{name: "parity above half the set", mutate: func(c *Config) {
			c.Erasure.SetDriveCount = 8
			c.Erasure.Parity = 5
		}, wantErr: "erasure.parity 5 is out of range"},
		{name: "negative parity", mutate: func(c *Config) { c.Erasure.Parity = -1 }, wantErr: "must not be negative"},
		{name: "drive count without erasure set", mutate: func(c *Config) {
			c.Cluster.Endpoints = []string{"https://node{1...17}:9100/data1"}
			c.Node.ID = "node1"
		}, wantErr: "cannot form erasure sets"},
		{name: "single drive development mode", mutate: func(c *Config) {
			c.Cluster.Endpoints = []string{"https://node1:9100/data1"}
		}, wantWarnings: "single drive"},
		{name: "single drive with parity", mutate: func(c *Config) {
			c.Cluster.Endpoints = []string{"https://node1:9100/data1"}
			c.Erasure.Parity = 2
		}, wantErr: "parity must be 0 for a single drive"},
		{name: "two nodes warn about metadata", mutate: func(c *Config) {
			c.Cluster.Endpoints = []string{"https://node{1...2}:9100/data{1...4}"}
		}, wantWarnings: "two nodes"},
		{name: "unequal drives warn", mutate: func(c *Config) {
			c.Cluster.Endpoints = []string{"https://node1:9100/data{1...4}", "https://node2:9100/data{1...3}"}
		}, wantWarnings: "unequal numbers of drives"},
		{name: "metadata partitions zero", mutate: func(c *Config) { c.Metadata.Partitions = 0 }, wantErr: "metadata.partitions 0"},
		{name: "inline threshold too large", mutate: func(c *Config) { c.Storage.InlineThreshold = 128 * kiB }, wantErr: "storage.inline_threshold"},
		{name: "memory budget too small", mutate: func(c *Config) { c.Storage.MemoryBudget = miB }, wantErr: "storage.memory_budget"},
		{name: "max requests zero", mutate: func(c *Config) { c.Limits.MaxRequests = 0 }, wantErr: "limits.max_requests"},
		{name: "negative queue", mutate: func(c *Config) { c.Limits.RequestQueue = -1 }, wantErr: "limits.request_queue"},
		{name: "zero background concurrency", mutate: func(c *Config) { c.Limits.BackgroundConcurrency = 0 }, wantErr: "limits.background_concurrency"},
		{name: "zero read timeout", mutate: func(c *Config) { c.Timeouts.Read = 0 }, wantErr: "timeouts.read"},
		{name: "timeout over a day", mutate: func(c *Config) { c.Timeouts.Idle = 25 * time.Hour }, wantErr: "timeouts.idle"},
		{name: "cert without key", mutate: func(c *Config) { c.TLS.CertFile = tlsCert }, wantErr: "must be set together"},
		{name: "cert file missing", mutate: func(c *Config) {
			c.TLS.CertFile = filepath.Join(t.TempDir(), "nope.pem")
			c.TLS.KeyFile = tlsKey
		}, wantErr: "is not readable"},
		{name: "tls required without certificate", mutate: func(c *Config) { c.TLS.Require = true }, wantErr: "tls.require is set"},
		{name: "tls configured", mutate: func(c *Config) {
			c.TLS.CertFile = tlsCert
			c.TLS.KeyFile = tlsKey
		}},
		{name: "root user missing", mutate: func(c *Config) { c.Auth.RootUser = Secret{} }, wantErr: "auth.root_user is not set"},
		{name: "root user short", mutate: func(c *Config) { c.Auth.RootUser = NewSecret("short") }, wantErr: "auth.root_user must be at least 12"},
		{name: "root user is a default", mutate: func(c *Config) { c.Auth.RootUser = NewSecret("administrator") }, wantErr: "well-known default"},
		{name: "root password missing", mutate: func(c *Config) { c.Auth.RootPassword = Secret{} }, wantErr: "auth.root_password is not set"},
		{name: "root password short", mutate: func(c *Config) { c.Auth.RootPassword = NewSecret("Pw7Kd2Rx9Lm4") }, wantErr: "auth.root_password must be at least 32"},
		{name: "root password equals user", mutate: func(c *Config) {
			v := "Pw7Kd2Rx9Lm4Vq8Zt1Nb6Hc3Yf5Gs0JaAA"
			c.Auth.RootUser = NewSecret(v)
			c.Auth.RootPassword = NewSecret(v)
		}, wantErr: "must differ from the root user name"},
		{name: "root password is a template", mutate: func(c *Config) {
			c.Auth.RootPassword = NewSecret("password-password-password-password-1234")
		}, wantErr: "well-known default or template"},
		{name: "metrics token missing", mutate: func(c *Config) { c.Metrics.Token = Secret{} }, wantErr: "metrics.token is not set"},
		{name: "metrics token equals cluster secret", mutate: func(c *Config) { c.Metrics.Token = NewSecret(testSecret) }, wantErr: "must differ from cluster.secret"},
		{name: "bad log level", mutate: func(c *Config) { c.Log.Level = "verbose" }, wantErr: "log.level"},
		{name: "bad log format", mutate: func(c *Config) { c.Log.Format = "xml" }, wantErr: "log.format"},
		{name: "plain http warning on valid config", wantWarnings: "plain HTTP"},
		{name: "several problems are all reported", mutate: func(c *Config) {
			c.Node.ID = ""
			c.Auth.RootUser = Secret{}
			c.Log.Level = "loud"
		}, wantErr: "log.level"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			if tt.mutate != nil {
				tt.mutate(&c)
			}
			warnings, err := Validate(&c)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.wantErr)
			}
			if tt.wantWarnings != "" && !strings.Contains(strings.Join(warnings, "\n"), tt.wantWarnings) {
				t.Fatalf("warnings = %q, want substring %q", warnings, tt.wantWarnings)
			}
		})
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	c := validConfig()
	c.Node.ID = ""
	c.Auth.RootUser = Secret{}
	c.Log.Level = "loud"
	_, err := Validate(&c)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"node.id is required", "auth.root_user is not set", "log.level"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestValidateNeverEchoesSecrets(t *testing.T) {
	c := validConfig()
	c.Auth.RootPassword = NewSecret("changeme-changeme-changeme-changeme")
	c.Cluster.Secret = NewSecret("tooshort-but-secret-value-xyz")
	_, err := Validate(&c)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, v := range []string{"changeme-changeme", "tooshort-but-secret-value-xyz"} {
		if strings.Contains(err.Error(), v) {
			t.Fatalf("error leaks a secret value: %v", err)
		}
	}
}

func TestAutoSetDriveCount(t *testing.T) {
	tests := []struct {
		total   int
		want    int
		wantErr bool
	}{
		{total: 1, want: 1},
		{total: 2, wantErr: true},
		{total: 3, wantErr: true},
		{total: 4, want: 4},
		{total: 5, want: 5},
		{total: 8, want: 8},
		{total: 12, want: 12},
		{total: 16, want: 16},
		{total: 18, want: 9},
		{total: 20, want: 10},
		{total: 32, want: 16},
		{total: 17, wantErr: true},
	}
	for _, tt := range tests {
		got, err := AutoSetDriveCount(tt.total)
		if tt.wantErr {
			if err == nil {
				t.Errorf("AutoSetDriveCount(%d) = %d, want error", tt.total, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("AutoSetDriveCount(%d) = %d, %v; want %d", tt.total, got, err, tt.want)
		}
	}
}

func TestDefaultParity(t *testing.T) {
	tests := []struct{ set, want int }{
		{set: 1, want: 0}, {set: 4, want: 2}, {set: 8, want: 2}, {set: 9, want: 4}, {set: 16, want: 4},
	}
	for _, tt := range tests {
		if got := DefaultParity(tt.set); got != tt.want {
			t.Errorf("DefaultParity(%d) = %d, want %d", tt.set, got, tt.want)
		}
	}
}

func TestLocalDrivesEmptyBeforeResolve(t *testing.T) {
	c := validConfig()
	if got := c.LocalDrives(); got != nil {
		t.Fatalf("LocalDrives before resolve = %v, want nil", got)
	}
}

func TestTLSEnabledNeedsBothFiles(t *testing.T) {
	c := validConfig()
	c.TLS.CertFile = "cert"
	if c.TLSEnabled() {
		t.Fatal("TLS must need both files")
	}
}
