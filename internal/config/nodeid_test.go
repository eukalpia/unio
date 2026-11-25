package config

import (
	"context"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func TestNodeID(t *testing.T) {
	tests := []struct {
		name   string
		nodeID string
		want   ids.NodeID
	}{
		{name: "host name", nodeID: "node1", want: "node1"},
		{name: "host name in upper case", nodeID: "NODE1", want: "node1"},
		{name: "kubernetes fqdn", nodeID: "unio-0.unio-hl.default.svc.cluster.local", want: "unio-0"},
		{name: "ipv4", nodeID: "10.0.0.5", want: "ip-10-0-0-5"},
		{name: "ipv6", nodeID: "fd00::1", want: "ip6-fd000000000000000000000000000001"},
		{name: "unusable host", nodeID: "no_de", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			c.Node.ID = tt.nodeID
			if got := c.NodeID(); got != tt.want {
				t.Fatalf("NodeID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNodeIDAfterLoadMatchesEndpointHost(t *testing.T) {
	env := validEnv()
	env["UNIO_NODE_ID"] = "NODE3"
	cfg, _, err := Load(context.Background(), Options{Env: env})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NodeID() != "node3" {
		t.Fatalf("NodeID() = %q, want node3", cfg.NodeID())
	}
	if got := len(cfg.LocalDrives()); got != 4 {
		t.Fatalf("LocalDrives() = %d drives, want the 4 of node3 matched without case", got)
	}
}

func TestValidateNamesBothHostsOnCollision(t *testing.T) {
	c := validConfig()
	c.Node.ID = "unio-0.a.example"
	c.Cluster.Endpoints = []string{"https://unio-0.a.example:9100/data1", "https://UNIO-0.b.example:9100/data1"}
	_, err := Validate(&c)
	if err == nil {
		t.Fatal("two hosts with the same node id must be refused")
	}
	for _, want := range []string{"unio-0.a.example", "UNIO-0.b.example", `"unio-0"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

func TestValidateAcceptsHostsThatDifferOnlyInCase(t *testing.T) {
	c := validConfig()
	c.Cluster.Endpoints = []string{"https://NODE1:9100/data{1...2}", "https://node1:9100/data{3...4}"}
	if _, err := Validate(&c); err != nil {
		t.Fatalf("one host written in two cases is one node: %v", err)
	}
}

func TestDefaultRegionAndDomainsFromEnvironment(t *testing.T) {
	cfg, _, err := Load(context.Background(), Options{Env: validEnv()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Node.Region != "us-east-1" || len(cfg.Node.Domains) != 0 {
		t.Fatalf("defaults: region %q, domains %v", cfg.Node.Region, cfg.Node.Domains)
	}

	env := validEnv()
	env["UNIO_REGION"] = "eu-west-3"
	env["UNIO_DOMAINS"] = "s3.example.com, s3.local"
	cfg, _, err = Load(context.Background(), Options{Env: env, Flags: map[string]string{"region": "ap-south-1"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Node.Region != "ap-south-1" {
		t.Fatalf("flag must override the environment: region %q", cfg.Node.Region)
	}
	if got := strings.Join(cfg.Node.Domains, ","); got != "s3.example.com,s3.local" {
		t.Fatalf("domains = %q", got)
	}
}
