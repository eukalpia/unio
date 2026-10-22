package ids

import (
	"strings"
	"testing"
)

func TestNodeIDFromHost(t *testing.T) {
	long253 := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	long254 := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62)
	tests := []struct {
		name    string
		host    string
		want    NodeID
		wantErr bool
	}{
		{name: "plain host name", host: "node1", want: "node1"},
		{name: "kubernetes fqdn", host: "unio-0.unio-hl.default.svc.cluster.local", want: "unio-0"},
		{name: "fqdn with two labels", host: "node1.cluster.local", want: "node1"},
		{name: "uppercase fqdn", host: "UNIO-0.Unio-HL.Default.SVC", want: "unio-0"},
		{name: "surrounding blanks are trimmed", host: "  node1  ", want: "node1"},
		{name: "ipv4", host: "10.0.0.5", want: "ip-10-0-0-5"},
		{name: "ipv4 zeros", host: "0.0.0.0", want: "ip-0-0-0-0"},
		{name: "ipv6 loopback", host: "::1", want: "ip6-00000000000000000000000000000001"},
		{name: "ipv6 full form uppercase", host: "FD00:0:0:0:0:0:0:1", want: "ip6-fd000000000000000000000000000001"},
		{name: "ipv4 mapped ipv6 is not an ipv4 id", host: "::ffff:10.0.0.5", want: "ip6-00000000000000000000ffff0a000005"},
		{name: "63 character label", host: strings.Repeat("a", 63) + ".example", want: NodeID(strings.Repeat("a", 63))},
		{name: "253 character name", host: long253, want: NodeID(strings.Repeat("a", 63))},
		{name: "digits first label", host: "0node.example", want: "0node"},
		{name: "hyphen inside first label", host: "a-b.example", want: "a-b"},
		{name: "empty", host: "", wantErr: true},
		{name: "blanks only", host: "   ", wantErr: true},
		{name: "trailing dot", host: "node1.", wantErr: true},
		{name: "empty middle label", host: "node1..local", wantErr: true},
		{name: "label longer than 63", host: strings.Repeat("a", 64) + ".example", wantErr: true},
		{name: "leading hyphen label", host: "-node.example", wantErr: true},
		{name: "trailing hyphen label", host: "node-.example", wantErr: true},
		{name: "leading hyphen in later label", host: "node1.-example", wantErr: true},
		{name: "underscore", host: "no_de.example", wantErr: true},
		{name: "underscore only", host: "_", wantErr: true},
		{name: "254 character name", host: long254, wantErr: true},
		{name: "space inside", host: "node 1", wantErr: true},
		{name: "non ascii", host: "nodé", wantErr: true},
		{name: "ipv6 with zone", host: "fe80::1%eth0", wantErr: true},
		{name: "bracketed ipv6", host: "[::1]", wantErr: true},
		{name: "malformed ipv6", host: "fd00::zz", wantErr: true},
		{name: "ipv6 with too many groups", host: "1:2:3:4:5:6:7:8:9", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NodeIDFromHost(tt.host)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NodeIDFromHost(%q) = %q, want an error", tt.host, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NodeIDFromHost(%q) error: %v", tt.host, err)
			}
			if got != tt.want {
				t.Fatalf("NodeIDFromHost(%q) = %q, want %q", tt.host, got, tt.want)
			}
			if err := ValidateNodeID(got); err != nil {
				t.Fatalf("NodeIDFromHost(%q) = %q, which fails ValidateNodeID: %v", tt.host, got, err)
			}
		})
	}
}

// FuzzNodeIDFromHost checks that every host either fails or yields a node id
// that passes ValidateNodeID.
func FuzzNodeIDFromHost(f *testing.F) {
	for _, seed := range []string{
		"unio-0.unio-hl.default.svc.cluster.local", "node1", "10.0.0.5", "::1", "fd00::1",
		"UPPER.case", "node1.", "-x", "a_b", "", "::ffff:1.2.3.4", "1.2.3.4.5", "ip-1-2-3-4",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, host string) {
		id, err := NodeIDFromHost(host)
		if err != nil {
			return
		}
		if err := ValidateNodeID(id); err != nil {
			t.Fatalf("NodeIDFromHost(%q) = %q, which fails ValidateNodeID: %v", host, id, err)
		}
	})
}

func TestValidateNodeID(t *testing.T) {
	for _, id := range []NodeID{"a", "node-1", "0node", NodeID(strings.Repeat("a", 63))} {
		if err := ValidateNodeID(id); err != nil {
			t.Errorf("ValidateNodeID(%q) = %v, want nil", id, err)
		}
	}
	for _, id := range []NodeID{"", "Node1", "-node", "node-", "node.1", "node_1", NodeID(strings.Repeat("a", 64))} {
		if err := ValidateNodeID(id); err == nil {
			t.Errorf("ValidateNodeID(%q) = nil, want an error", id)
		}
	}
}
