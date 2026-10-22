package handlers

import (
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/headers"
	"github.com/eukalpia/unio/internal/s3api/xmlenc"
)

// PutObject stores the request body as the current version of an object.
// The body is verified while it is read; the layer commits only after the
// whole body passed verification.
func (e *Env) PutObject(c *Call) error {
	attrs, err := headers.ParseObjectAttributes(c.Header)
	if err != nil {
		return err
	}
	body, sum, err := c.objectBody(object.MaxPutSize)
	if err != nil {
		return err
	}
	attrs.ChecksumAlgorithm = sum.Algorithm
	info, err := e.Layer.PutObject(c.Ctx, c.Route.Bucket, c.Route.Key, body, attrs)
	if err != nil {
		return err
	}
	c.RC.Response.Header.Set("ETag", headers.QuoteETag(info.ETag))
	setVersionHeader(c, info.VersionID)
	setChecksumHeaders(c, info.Checksum)
	writeStatus(c, 200)
	return nil
}

// setVersionHeader sets x-amz-version-id when the version has an id.
func setVersionHeader(c *Call, versionID string) {
	if versionID != "" {
		c.RC.Response.Header.Set("x-amz-version-id", versionID)
	}
}

// setChecksumHeaders sets the x-amz-checksum-<algorithm> response header for
// a computed checksum. A zero checksum sets nothing.
func setChecksumHeaders(c *Call, cs object.Checksum) {
	if cs.IsZero() {
		return
	}
	c.RC.Response.Header.Set("x-amz-checksum-"+strings.ToLower(cs.Algorithm.String()), cs.Base64())
}

// checksumFields maps a checksum to the XML elements of multipart and copy
// responses, together with the ChecksumType element value.
func checksumFields(cs object.Checksum) (xmlenc.Checksums, string) {
	var sums xmlenc.Checksums
	if cs.IsZero() {
		return sums, ""
	}
	v := cs.Base64()
	switch cs.Algorithm {
	case object.ChecksumCRC32:
		sums.CRC32 = v
	case object.ChecksumCRC32C:
		sums.CRC32C = v
	case object.ChecksumCRC64NVME:
		sums.CRC64NVME = v
	case object.ChecksumSHA1:
		sums.SHA1 = v
	case object.ChecksumSHA256:
		sums.SHA256 = v
	}
	if cs.Type == object.ChecksumComposite {
		return sums, "COMPOSITE"
	}
	return sums, "FULL_OBJECT"
}
