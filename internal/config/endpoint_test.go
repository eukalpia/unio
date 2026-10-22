package config

import (
	"strings"
	"testing"
)

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Endpoint
		wantErr string
	}{
		{name: "https host name", raw: "https://node1:9100/data1", want: Endpoint{Scheme: "https", Host: "node1", Port: 9100, Path: "/data1"}},
		{name: "http ip address", raw: "http://10.0.0.5:9100/d", want: Endpoint{Scheme: "http", Host: "10.0.0.5", Port: 9100, Path: "/d"}},
		{name: "ipv6 host", raw: "https://[fd00::1]:9100/data1", want: Endpoint{Scheme: "https", Host: "fd00::1", Port: 9100, Path: "/data1"}},
		{name: "fully qualified name", raw: "https://node1.cluster.local:9100/mnt/disk-2", want: Endpoint{Scheme: "https", Host: "node1.cluster.local", Port: 9100, Path: "/mnt/disk-2"}},
		{name: "upper case scheme", raw: "HTTPS://node1:9100/data1", want: Endpoint{Scheme: "https", Host: "node1", Port: 9100, Path: "/data1"}},
		{name: "wrong scheme", raw: "ftp://node1:9100/data1", wantErr: "scheme"},
		{name: "credentials", raw: "https://user:secret@node1:9100/data1", wantErr: "credentials"},
		{name: "missing port", raw: "https://node1/data1", wantErr: "explicit port"},
		{name: "port zero", raw: "https://node1:0/data1", wantErr: "explicit port"},
		{name: "port too large", raw: "https://node1:70000/data1", wantErr: "explicit port"},
		{name: "root path", raw: "https://node1:9100/", wantErr: "root directory"},
		{name: "no path", raw: "https://node1:9100", wantErr: "absolute"},
		{name: "trailing slash", raw: "https://node1:9100/data1/", wantErr: "must be clean"},
		{name: "dot dot", raw: "https://node1:9100/../data1", wantErr: "must be clean"},
		{name: "repeated slash", raw: "https://node1:9100//data1", wantErr: "must be clean"},
		{name: "query", raw: "https://node1:9100/data1?x=1", wantErr: "query"},
		{name: "fragment", raw: "https://node1:9100/data1#x", wantErr: "query"},
		{name: "underscore host", raw: "https://node_1:9100/data1", wantErr: "invalid host"},
		{name: "leading hyphen host", raw: "https://-node:9100/data1", wantErr: "invalid host"},
		{name: "empty", raw: "", wantErr: "1 to 512"},
		{name: "control character in path", raw: "https://node1:9100/da\x01ta", wantErr: "not a valid URL"},
		{name: "not a url", raw: "::", wantErr: "not a valid URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEndpoint(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseEndpoint(%q) error = %v, want substring %q", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEndpoint(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseEndpoint(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestEndpointStringRoundTrip(t *testing.T) {
	for _, raw := range []string{"https://node1:9100/data1", "https://[fd00::1]:9100/d", "http://10.0.0.5:9101/a/b"} {
		ep, err := ParseEndpoint(raw)
		if err != nil {
			t.Fatalf("ParseEndpoint(%q): %v", raw, err)
		}
		if ep.String() != raw {
			t.Fatalf("String() = %q, want %q", ep.String(), raw)
		}
		again, err := ParseEndpoint(ep.String())
		if err != nil || again != ep {
			t.Fatalf("round trip of %q changed the endpoint: %+v, %v", raw, again, err)
		}
	}
}

// FuzzParseEndpoint checks that the parser never panics and that accepted
// endpoints round-trip through their canonical form.
func FuzzParseEndpoint(f *testing.F) {
	for _, seed := range []string{
		"https://node1:9100/data1", "http://[::1]:9100/d", "https://a:1/b", "ftp://x", "https://u@h:1/x",
		"https://h:1/../x", "https://h:99999/x", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		ep, err := ParseEndpoint(raw)
		if err != nil {
			return
		}
		canon := ep.String()
		again, err := ParseEndpoint(canon)
		if err != nil {
			t.Fatalf("canonical form %q of %q does not parse: %v", canon, raw, err)
		}
		if again != ep {
			t.Fatalf("round trip changed %q: %+v != %+v", raw, again, ep)
		}
	})
}
