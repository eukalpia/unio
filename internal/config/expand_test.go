package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestExpandPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
		wantErr string
	}{
		{name: "no ellipsis", pattern: "https://node1:9100/data1", want: []string{"https://node1:9100/data1"}},
		{name: "empty pattern expands to itself", pattern: "", want: []string{""}},
		{name: "single range", pattern: "node{1...3}", want: []string{"node1", "node2", "node3"}},
		{name: "single value range", pattern: "x{5...5}", want: []string{"x5"}},
		{
			name:    "two ranges, leftmost varies slowest",
			pattern: "https://n{1...2}:9100/d{1...2}",
			want:    []string{"https://n1:9100/d1", "https://n1:9100/d2", "https://n2:9100/d1", "https://n2:9100/d2"},
		},
		{
			name:    "minio style cluster",
			pattern: "https://node{1...4}:9100/data{1...4}",
			want: []string{
				"https://node1:9100/data1", "https://node1:9100/data2", "https://node1:9100/data3", "https://node1:9100/data4",
				"https://node2:9100/data1", "https://node2:9100/data2", "https://node2:9100/data3", "https://node2:9100/data4",
				"https://node3:9100/data1", "https://node3:9100/data2", "https://node3:9100/data3", "https://node3:9100/data4",
				"https://node4:9100/data1", "https://node4:9100/data2", "https://node4:9100/data3", "https://node4:9100/data4",
			},
		},
		{name: "zero padded range", pattern: "d{01...03}", want: []string{"d01", "d02", "d03"}},
		{name: "padding width follows start", pattern: "d{08...10}", want: []string{"d08", "d09", "d10"}},
		{name: "literal dots are not a range", pattern: "a...b{1...2}", want: []string{"a...b1", "a...b2"}},
		{name: "unclosed brace", pattern: "a{1...2", wantErr: "unclosed '{'"},
		{name: "stray closing brace", pattern: "a}1", wantErr: "stray '}'"},
		{name: "nested braces", pattern: "a{1{2...3}}", wantErr: "unclosed '{'"},
		{name: "reversed range", pattern: "{3...1}", wantErr: "reversed"},
		{name: "two dots instead of three", pattern: "{1..3}", wantErr: "must have the form"},
		{name: "empty braces", pattern: "{}", wantErr: "must have the form"},
		{name: "non numeric bounds", pattern: "{a...b}", wantErr: "decimal numbers"},
		{name: "signed bounds", pattern: "{-1...3}", wantErr: "decimal numbers"},
		{name: "too many separators", pattern: "{1...2...3}", wantErr: "decimal numbers"},
		{name: "bound too long", pattern: "{1...1234567890}", wantErr: "decimal numbers"},
		{name: "expansion over the limit", pattern: "{1...4096}{1...2}", wantErr: "more than 4096 endpoints"},
		{name: "pattern too long", pattern: strings.Repeat("a", 4097), wantErr: "longer than 4096"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandPattern(tt.pattern)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ExpandPattern(%q) error = %v, want substring %q", tt.pattern, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExpandPattern(%q) unexpected error: %v", tt.pattern, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ExpandPattern(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestExpandEndpoints(t *testing.T) {
	t.Run("minio style cluster yields 16 drives", func(t *testing.T) {
		got, err := ExpandEndpoints([]string{"https://node{1...4}:9100/data{1...4}"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 16 {
			t.Fatalf("got %d endpoints, want 16", len(got))
		}
		if got[0].String() != "https://node1:9100/data1" || got[15].String() != "https://node4:9100/data4" {
			t.Fatalf("unexpected first or last endpoint: %s, %s", got[0], got[15])
		}
		if got[5].Host != "node2" || got[5].Path != "/data2" || got[5].Port != 9100 {
			t.Fatalf("endpoint 5 = %+v", got[5])
		}
	})

	errTests := []struct {
		name     string
		patterns []string
		wantErr  string
	}{
		{name: "duplicate across patterns", patterns: []string{"https://n1:9100/d1", "https://n1:9100/d{1...1}"}, wantErr: "duplicate endpoint"},
		{name: "invalid endpoint", patterns: []string{"ftp://n1:9100/d1"}, wantErr: "scheme"},
		{name: "bad pattern reports pattern", patterns: []string{"https://n{1...x}:9100/d1"}, wantErr: "decimal numbers"},
		{name: "total over the cap", patterns: []string{"https://a{1...3000}:9100/x", "https://b{1...3000}:9100/x"}, wantErr: "more than 4096"},
		{name: "all problems are reported", patterns: []string{"ftp://a:1/x", "https://b:9100/"}, wantErr: "ftp://"},
	}
	for _, tt := range errTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ExpandEndpoints(tt.patterns)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ExpandEndpoints(%v) error = %v, want substring %q", tt.patterns, err, tt.wantErr)
			}
		})
	}

	t.Run("all problems are joined", func(t *testing.T) {
		_, err := ExpandEndpoints([]string{"ftp://a:1/x", "https://b:9100/"})
		var joined interface{ Unwrap() []error }
		if !errors.As(err, &joined) || len(joined.Unwrap()) != 2 {
			t.Fatalf("want two joined errors, got %v", err)
		}
	})
}

// FuzzExpandPattern checks that the expander never panics, that its output is
// bounded, and that a pattern without braces expands to itself.
func FuzzExpandPattern(f *testing.F) {
	for _, seed := range []string{
		"", "a", "{1...3}", "https://node{1...4}:9100/data{1...4}", "x{01...10}y{1...2}",
		"{3...1}", "}{", "{1...2}{1...2}", "{1...4096}{1...2}", "{a...b}", "{1..2}", "{}",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, pattern string) {
		out, err := ExpandPattern(pattern)
		if err != nil {
			return
		}
		if len(out) == 0 || len(out) > MaxEndpoints {
			t.Fatalf("ExpandPattern(%q) returned %d results", pattern, len(out))
		}
		if !strings.ContainsAny(pattern, "{}") && (len(out) != 1 || out[0] != pattern) {
			t.Fatalf("ExpandPattern(%q) = %v, want the pattern itself", pattern, out)
		}
		for _, s := range out {
			if strings.ContainsAny(s, "{}") {
				t.Fatalf("ExpandPattern(%q) produced braces in %q", pattern, s)
			}
		}
	})
}
