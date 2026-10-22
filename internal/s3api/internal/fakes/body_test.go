package fakes

import (
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestReadPayload(t *testing.T) {
	boom := errors.New("content-sha256 mismatch")
	tests := []struct {
		name     string
		body     object.Body
		want     string
		wantErr  error
		wantCode s3err.Code
	}{
		{name: "complete body", body: newTestBody([]byte("payload")), want: "payload"},
		{name: "empty body", body: newTestBody(nil), want: ""},
		{name: "read failure is returned unchanged", body: newFailingBody([]byte("par"), boom), wantErr: boom},
		{
			name:     "fewer bytes than declared",
			body:     &testBody{reader: newTestBody([]byte("ab")).reader, size: 5},
			wantCode: s3err.IncompleteBody,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := readPayload(tt.body)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("readPayload error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			wantCode(t, err, tt.wantCode)
			if tt.wantCode == "" && string(data) != tt.want {
				t.Errorf("readPayload = %q, want %q", data, tt.want)
			}
		})
	}
}
