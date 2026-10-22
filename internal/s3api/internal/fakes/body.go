package fakes

import (
	"io"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// readPayload reads body to io.EOF and returns its bytes. A read error,
// including an integrity failure, is returned unchanged so that the caller
// can refuse to commit. A payload whose length differs from the declared size
// is an IncompleteBody error. The body's MD5 and Checksum are valid only once
// this returns.
func readPayload(body object.Body) ([]byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != body.Size() {
		return nil, s3err.Errorf(s3err.IncompleteBody, "payload has %d bytes, declared %d", len(data), body.Size())
	}
	return data, nil
}
