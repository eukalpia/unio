package handlers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestUploadPartRejectsPartNumberOutOfRange checks the part number of UploadPart
// against the S3 range 1 to 10000. Zero, a number above the range and a value
// that is not a number are refused with InvalidArgument. The upload id is a
// real one, so only the part number can be at fault.
func TestUploadPartRejectsPartNumberOutOfRange(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "uploads")
	id := s.startUpload(t, "uploads", "k")

	cases := []struct {
		name       string
		partNumber string
	}{
		{name: "zero", partNumber: "0"},
		{name: "above the maximum", partNumber: "10001"},
		{name: "not a number", partNumber: "one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := "/uploads/k?partNumber=" + tc.partNumber + "&uploadId=" + id
			r := s.do(t, http.MethodPut, target, []byte("part"), nil)
			expectError(t, r, http.StatusBadRequest, "InvalidArgument")
		})
	}
}

// completeBody builds a CompleteMultipartUpload document that lists the given
// part numbers in the given order, each with a placeholder ETag.
func completeBody(parts ...int) []byte {
	var b strings.Builder
	b.WriteString("<CompleteMultipartUpload>")
	for _, n := range parts {
		fmt.Fprintf(&b, `<Part><PartNumber>%d</PartNumber><ETag>"%032x"</ETag></Part>`, n, n)
	}
	b.WriteString("</CompleteMultipartUpload>")
	return []byte(b.String())
}

// TestCompleteMultipartUploadRejectsPartOrder checks that a completion request
// whose part numbers are not strictly ascending is refused with
// InvalidPartOrder, both when the order is reversed and when a number repeats.
func TestCompleteMultipartUploadRejectsPartOrder(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "uploads")

	cases := []struct {
		name  string
		parts []int
	}{
		{name: "descending part numbers", parts: []int{2, 1}},
		{name: "repeated part number", parts: []int{1, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := s.startUpload(t, "uploads", "k")
			r := s.do(t, http.MethodPost, "/uploads/k?uploadId="+id, completeBody(tc.parts...), nil)
			expectError(t, r, http.StatusBadRequest, "InvalidPartOrder")
		})
	}
}
