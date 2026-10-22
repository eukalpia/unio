package workload

import (
	"slices"
	"testing"
)

func TestNamesCanonicalOrder(t *testing.T) {
	want := []string{"put-4k", "get-4k", "put-1m", "get-1m", "put-64m", "get-64m", "list", "mixed"}
	if got := Names(); !slices.Equal(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
}

func TestParseNames(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{name: "all", in: "all", want: Names()},
		{name: "single", in: "mixed", want: []string{"mixed"}},
		{name: "keeps given order", in: "get-4k,put-4k", want: []string{"get-4k", "put-4k"}},
		{name: "trims spaces", in: " put-1m , list ", want: []string{"put-1m", "list"}},
		{name: "deduplicates", in: "get-4k,get-4k,put-4k,get-4k", want: []string{"get-4k", "put-4k"}},
		{name: "empty item is an error", in: "get-4k,,put-4k", wantErr: true},
		{name: "unknown name", in: "get-4k,bogus", wantErr: true},
		{name: "empty list", in: "", wantErr: true},
		{name: "all cannot be mixed", in: "all,get-4k", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseNames(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseNames(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if !tc.wantErr && !slices.Equal(got, tc.want) {
				t.Fatalf("ParseNames(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
