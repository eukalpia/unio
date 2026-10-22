package handlers_test

import (
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
	"net/http"
	"testing"
)

// TestNumericQueryParametersAreStrict checks that numeric query parameters
// accept only plain decimal digits within their documented range. A leading
// plus sign, a hexadecimal form and a fraction are not numbers in S3's
// vocabulary and must be refused with InvalidArgument. The plus sign is sent
// percent-encoded, so that it reaches the server as a plus rather than a space.
func TestNumericQueryParametersAreStrict(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "numeric")
	id := s.startUpload(t, "numeric", "k")

	for _, value := range []string{"%2B1", "%201", "0x1", "1.0", "1_000"} {
		t.Run("partNumber "+value, func(t *testing.T) {
			r := s.do(t, http.MethodPut, "/numeric/k?partNumber="+value+"&uploadId="+id, []byte("x"), nil)
			expectError(t, r, http.StatusBadRequest, "InvalidArgument")
		})
	}
	for _, value := range []string{"%2B5", "0x5", "5.0"} {
		t.Run("max-keys "+value, func(t *testing.T) {
			r := s.do(t, http.MethodGet, "/numeric?list-type=2&max-keys="+value, nil, nil)
			expectError(t, r, http.StatusBadRequest, "InvalidArgument")
		})
	}
	if r := s.do(t, http.MethodPut, "/numeric/k?partNumber=1&uploadId="+id, []byte("x"), nil); r.status != http.StatusOK {
		t.Fatalf("plain partNumber=1: status %d", r.status)
	}
}
