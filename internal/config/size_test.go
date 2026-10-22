package config

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		in      string
		want    Size
		wantErr string
	}{
		{in: "0", want: 0},
		{in: "4096", want: 4096},
		{in: "4KiB", want: 4 * kiB},
		{in: "4kb", want: 4 * kiB},
		{in: "4 K", want: 4 * kiB},
		{in: " 64 MiB ", want: 64 * miB},
		{in: "64mib", want: 64 * miB},
		{in: "1GiB", want: giB},
		{in: "2g", want: 2 * giB},
		{in: "1TiB", want: tiB},
		{in: "", wantErr: "expected a number"},
		{in: "KiB", wantErr: "expected a number"},
		{in: "-1", wantErr: "expected a number"},
		{in: "1.5GiB", wantErr: "unknown unit"},
		{in: "10XB", wantErr: "unknown unit"},
		{in: "99999999999999999T", wantErr: "overflows"},
		{in: "99999999999999999999999", wantErr: "invalid size"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseSize(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseSize(%q) error = %v, want substring %q", tt.in, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ParseSize(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestSizeString(t *testing.T) {
	tests := []struct {
		in   Size
		want string
	}{
		{in: 0, want: "0"},
		{in: 1000, want: "1000"},
		{in: 4 * kiB, want: "4KiB"},
		{in: 64 * miB, want: "64MiB"},
		{in: giB, want: "1GiB"},
		{in: 2 * tiB, want: "2TiB"},
		{in: 1536, want: "1536"},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("Size(%d).String() = %q, want %q", int64(tt.in), got, tt.want)
		}
	}
}

func TestSizeYAML(t *testing.T) {
	var s struct {
		Budget Size `yaml:"budget"`
	}
	if err := yaml.Unmarshal([]byte("budget: 4KiB\n"), &s); err != nil || s.Budget != 4*kiB {
		t.Fatalf("unmarshal 4KiB = %d, %v", s.Budget, err)
	}
	if err := yaml.Unmarshal([]byte("budget: [1]\n"), &s); err == nil {
		t.Fatal("a sequence must not decode as a size")
	}
	if err := yaml.Unmarshal([]byte("budget: 4XB\n"), &s); err == nil {
		t.Fatal("an unknown unit must not decode")
	}
	out, err := yaml.Marshal(s)
	if err != nil || !strings.Contains(string(out), "budget: 4KiB") {
		t.Fatalf("marshal = %q, %v", out, err)
	}
}
