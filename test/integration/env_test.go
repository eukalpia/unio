package integration

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	const (
		access = "AKIAUNIOTESTKEY01"
		secret = "unio-test-secret-key-0123456789abcdef"
	)
	valid := map[string]string{
		envEndpoint:  "http://localhost:9001",
		envAccessKey: access,
		envSecretKey: secret,
	}
	with := func(overrides map[string]string) map[string]string {
		env := make(map[string]string, len(valid)+len(overrides))
		for k, v := range valid {
			env[k] = v
		}
		for k, v := range overrides {
			env[k] = v
		}
		return env
	}

	tests := []struct {
		name        string
		env         map[string]string
		want        config
		wantUnset   bool
		wantInvalid bool
	}{
		{
			name: "defaults region",
			env:  valid,
			want: config{endpoint: "http://localhost:9001", accessKey: access, secretKey: secret, region: defaultRegion},
		},
		{
			name: "explicit region and trailing slash",
			env: with(map[string]string{
				envEndpoint: "https://s3.example.test/",
				envRegion:   "eu-west-1",
			}),
			want: config{endpoint: "https://s3.example.test", accessKey: access, secretKey: secret, region: "eu-west-1"},
		},
		{
			name: "surrounding whitespace is trimmed",
			env: with(map[string]string{
				envEndpoint:  "  http://127.0.0.1:9000  ",
				envAccessKey: " " + access + " ",
			}),
			want: config{endpoint: "http://127.0.0.1:9000", accessKey: access, secretKey: secret, region: defaultRegion},
		},
		{
			name:      "endpoint unset",
			env:       map[string]string{envAccessKey: access, envSecretKey: secret},
			wantUnset: true,
		},
		{
			name:      "endpoint blank",
			env:       with(map[string]string{envEndpoint: "   "}),
			wantUnset: true,
		},
		{
			name:        "access key missing",
			env:         with(map[string]string{envAccessKey: ""}),
			wantInvalid: true,
		},
		{
			name: "secret key missing",
			env: map[string]string{
				envEndpoint:  "http://localhost:9001",
				envAccessKey: access,
			},
			wantInvalid: true,
		},
		{
			name:        "unsupported scheme",
			env:         with(map[string]string{envEndpoint: "ftp://localhost:9001"}),
			wantInvalid: true,
		},
		{
			name:        "missing host",
			env:         with(map[string]string{envEndpoint: "http://"}),
			wantInvalid: true,
		},
		{
			name:        "embedded credentials are rejected",
			env:         with(map[string]string{envEndpoint: "http://user:pass@localhost:9001"}),
			wantInvalid: true,
		},
		{
			name:        "path is rejected",
			env:         with(map[string]string{envEndpoint: "http://localhost:9001/prefix"}),
			wantInvalid: true,
		},
		{
			name:        "query is rejected",
			env:         with(map[string]string{envEndpoint: "http://localhost:9001/?x=1"}),
			wantInvalid: true,
		},
		{
			name: "CA file with an https endpoint",
			env: with(map[string]string{
				envEndpoint: "https://localhost:9443",
				envCAFile:   "ca.pem",
			}),
			want: config{endpoint: "https://localhost:9443", accessKey: access, secretKey: secret, region: defaultRegion, caFile: "ca.pem"},
		},
		{
			name: "CA file with a plain http endpoint",
			env: with(map[string]string{
				envCAFile: "ca.pem",
			}),
			wantInvalid: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lookup := func(key string) (string, bool) {
				v, ok := tc.env[key]
				return v, ok
			}
			got, err := parseConfig(lookup)
			switch {
			case tc.wantUnset:
				if !errors.Is(err, errNotConfigured) {
					t.Fatalf("parseConfig() error = %v, want errNotConfigured", err)
				}
			case tc.wantInvalid:
				if err == nil {
					t.Fatalf("parseConfig() error = nil, want an error")
				}
				// Error text must never carry a credential value.
				for _, forbidden := range []string{access, secret, "user:pass"} {
					if strings.Contains(err.Error(), forbidden) {
						t.Fatalf("parseConfig() error leaks a credential: %q", err.Error())
					}
				}
			default:
				if err != nil {
					t.Fatalf("parseConfig() error = %v", err)
				}
				if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(config{})); diff != "" {
					t.Errorf("parseConfig() mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
