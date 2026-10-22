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
		{name: "node id invalid", mutate: func(c *Config) { c.Node.ID = "no_de" }, wantErr: "node.id: host \"no_de\""},
		{name: "node id matches no endpoint", mutate: func(c *Config) { c.Node.ID = "node9" }, wantErr: "no endpoint belongs to node \"node9\""},
		{name: "node id matches an endpoint ignoring case", mutate: func(c *Config) { c.Node.ID = "NODE1" }},
		{name: "node id with a trailing dot", mutate: func(c *Config) { c.Node.ID = "node1." }, wantErr: "trailing dot"},
		{name: "region with upper case", mutate: func(c *Config) { c.Node.Region = "US-EAST-1" }, wantErr: "node.region \"US-EAST-1\""},
		{name: "region empty", mutate: func(c *Config) { c.Node.Region = "" }, wantErr: "node.region \"\""},
		{name: "region longer than 32", mutate: func(c *Config) { c.Node.Region = strings.Repeat("a", 33) }, wantErr: "node.region"},
		{name: "region of 32 characters", mutate: func(c *Config) { c.Node.Region = strings.Repeat("a", 32) }},
		{name: "region with underscore", mutate: func(c *Config) { c.Node.Region = "eu_west" }, wantErr: "node.region"},
		{name: "virtual host domains", mutate: func(c *Config) { c.Node.Domains = []string{"s3.example.com", "S3.Local"} }},
		{name: "domain is an ip address", mutate: func(c *Config) { c.Node.Domains = []string{"10.0.0.1"} }, wantErr: "is an IP address"},
		{name: "domain with trailing dot", mutate: func(c *Config) { c.Node.Domains = []string{"s3.example.com."} }, wantErr: "trailing dot"},
		{name: "domain with underscore", mutate: func(c *Config) { c.Node.Domains = []string{"s_3.example.com"} }, wantErr: "node.domains entry \"s_3.example.com\""},
		{name: "domain with surrounding blank", mutate: func(c *Config) { c.Node.Domains = []string{" s3.example.com"} }, wantErr: "surrounding blanks"},
		{name: "empty domain", mutate: func(c *Config) { c.Node.Domains = []string{""} }, wantErr: "surrounding blanks"},
		{name: "kubernetes fqdn endpoints", mutate: func(c *Config) {
			c.Node.ID = "unio-0.unio-hl.default.svc.cluster.local"
			c.Cluster.Endpoints = []string{"https://unio-{0...3}.unio-hl.default.svc.cluster.local:9100/data{1...4}"}
		}},
		{name: "ipv4 endpoints", mutate: func(c *Config) {
			c.Node.ID = "10.0.0.1"
			c.Cluster.Endpoints = []string{"https://10.0.0.{1...4}:9100/data{1...4}"}
		}},
		{name: "ipv6 endpoint", mutate: func(c *Config) {
			c.Node.ID = "fd00::1"
			c.Cluster.Endpoints = []string{"https://[fd00::1]:9100/data{1...4}"}
		}},
		{name: "two hosts with the same first label", mutate: func(c *Config) {
			c.Node.ID = "unio-0.a.example"
			c.Cluster.Endpoints = []string{"https://unio-0.a.example:9100/data1", "https://unio-0.b.example:9100/data1"}
		}, wantErr: "both name node \"unio-0\""},
		{name: "ipv4 host and dns host name the same node", mutate: func(c *Config) {
			c.Node.ID = "ip-10-0-0-5"
			c.Cluster.Endpoints = []string{"https://ip-10-0-0-5:9100/data1", "https://10.0.0.5:9100/data1"}
		}, wantErr: "both name node \"ip-10-0-0-5\""},
		{name: "endpoint host with an underscore", mutate: func(c *Config) {
			c.Node.ID = "node_1"
			c.Cluster.Endpoints = []string{"https://node_1:9100/data1"}
		}, wantErr: "host \"node_1\""},
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
		{name: "zero read idle timeout", mutate: func(c *Config) { c.Timeouts.ReadIdle = 0 }, wantErr: "timeouts.read_idle"},
		{name: "zero write idle timeout", mutate: func(c *Config) { c.Timeouts.WriteIdle = 0 }, wantErr: "timeouts.write_idle"},
		{name: "write idle timeout over a day", mutate: func(c *Config) { c.Timeouts.WriteIdle = 25 * time.Hour }, wantErr: "timeouts.write_idle"},
		{name: "write cap zero means no cap", mutate: func(c *Config) { c.Timeouts.Write = 0 }},
		{name: "write cap set", mutate: func(c *Config) { c.Timeouts.Write = 30 * time.Minute }},
		{name: "negative write cap", mutate: func(c *Config) { c.Timeouts.Write = -time.Second }, wantErr: "timeouts.write -1s must be 0 for no cap"},
		{name: "write cap over a day", mutate: func(c *Config) { c.Timeouts.Write = 25 * time.Hour }, wantErr: "timeouts.write 25h0m0s must be 0 for no cap"},
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
		{name: "root user short", mutate: func(c *Config) { c.Auth.RootUser = NewSecret("short") }, wantErr: "root access key must be at least 12"},
		{name: "root user with a hyphen", mutate: func(c *Config) { c.Auth.RootUser = NewSecret("unio-root-adm01") }, wantErr: "only letters and digits"},
		{name: "root user of 128 letters and digits", mutate: func(c *Config) { c.Auth.RootUser = NewSecret(strings.Repeat("R", 128)) }},
		{name: "root user longer than 128", mutate: func(c *Config) { c.Auth.RootUser = NewSecret(strings.Repeat("R", 129)) }, wantErr: "at most 128"},
		{name: "root password missing", mutate: func(c *Config) { c.Auth.RootPassword = Secret{} }, wantErr: "auth.root_password is not set"},
		{name: "root password short", mutate: func(c *Config) { c.Auth.RootPassword = NewSecret("Pw7Kd2Rx9Lm4") }, wantErr: "root secret key must be at least 32"},
		{name: "root password longer than 256", mutate: func(c *Config) { c.Auth.RootPassword = NewSecret(strings.Repeat("Ab3", 90)) }, wantErr: "root secret key must be at most 256"},
		{name: "root password with a space", mutate: func(c *Config) {
			c.Auth.RootPassword = NewSecret("Pw7Kd2Rx9Lm4 Vq8Zt1Nb6Hc3Yf5Gs0Ja")
		}, wantErr: "printable characters without spaces"},
		{name: "root password with few distinct characters", mutate: func(c *Config) {
			c.Auth.RootPassword = NewSecret(strings.Repeat("ab", 16))
		}, wantErr: "at least 10 distinct characters"},
		{name: "root password of 256 characters", mutate: func(c *Config) { c.Auth.RootPassword = NewSecret(strings.Repeat("Ab3Cd5Ef7Gh9Jk", 18) + "Ab3C") }},
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
