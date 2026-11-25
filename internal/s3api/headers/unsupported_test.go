package headers

import (
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

// TestRejectUnsupported checks that a header for a feature outside the v0.1
// scope is refused with NotImplemented, and that the canned ACLs that grant
// nothing beyond the owner's access are accepted.
func TestRejectUnsupported(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   s3err.Code
	}{
		{"no unsupported headers", http.Header{"Content-Type": {"text/plain"}}, ""},
		{"private canned acl", http.Header{"X-Amz-Acl": {"private"}}, ""},
		{"owner full control canned acl", http.Header{"X-Amz-Acl": {"bucket-owner-full-control"}}, ""},
		{"public-read canned acl", http.Header{"X-Amz-Acl": {"public-read"}}, s3err.NotImplemented},
		{"public-read-write canned acl", http.Header{"X-Amz-Acl": {"public-read-write"}}, s3err.NotImplemented},
		{"grant header", http.Header{"X-Amz-Grant-Read": {`id="x"`}}, s3err.NotImplemented},
		{"object lock mode", http.Header{"X-Amz-Object-Lock-Mode": {"GOVERNANCE"}}, s3err.NotImplemented},
		{"object lock legal hold", http.Header{"X-Amz-Object-Lock-Legal-Hold": {"ON"}}, s3err.NotImplemented},
		{"website redirect", http.Header{"X-Amz-Website-Redirect-Location": {"/other"}}, s3err.NotImplemented},
		{"user metadata that mentions an acl", http.Header{"X-Amz-Meta-Acl": {"public-read"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RejectUnsupported(tt.header)
			if got := s3err.CodeOf(err); (err == nil && tt.want != "") || (err != nil && got != tt.want) {
				t.Fatalf("code = %q (err %v), want %q", got, err, tt.want)
			}
		})
	}
}

// TestParseTaggingDirective checks that COPY and an absent directive are
// accepted, REPLACE is NotImplemented, and any other value is InvalidArgument.
func TestParseTaggingDirective(t *testing.T) {
	tests := []struct {
		name  string
		value []string
		want  s3err.Code
	}{
		{"absent", nil, ""},
		{"copy", []string{"COPY"}, ""},
		{"replace", []string{"REPLACE"}, s3err.NotImplemented},
		{"lower case", []string{"copy"}, s3err.InvalidArgument},
		{"repeated", []string{"COPY", "COPY"}, s3err.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			for _, v := range tt.value {
				h.Add("X-Amz-Tagging-Directive", v)
			}
			err := ParseTaggingDirective(h)
			if got := s3err.CodeOf(err); (err == nil && tt.want != "") || (err != nil && got != tt.want) {
				t.Fatalf("code = %q (err %v), want %q", got, err, tt.want)
			}
		})
	}
}
