package handlers

import (
	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/headers"
	"github.com/eukalpia/unio/internal/s3api/xmlenc"
)

// sourceHeader is the copy source header of CopyObject and UploadPartCopy.
const sourceHeader = "X-Amz-Copy-Source"

// CopyObject copies an object server-side. The caller needs read access to
// the source and write access to the destination. The metadata directive
// decides whether the destination takes the source attributes (COPY) or the
// attributes of this request (REPLACE).
func (e *Env) CopyObject(c *Call) error {
	src, err := headers.ParseCopySource(c.Header.Get(sourceHeader))
	if err != nil {
		return err
	}
	if err := c.Authorize(auth.ActionGetObject, src.Bucket, src.Key); err != nil {
		return err
	}
	srcConds, err := headers.ParseCopyConditions(c.Header)
	if err != nil {
		return err
	}
	directive, err := headers.ParseMetadataDirective(c.Header)
	if err != nil {
		return err
	}
	put, err := headers.ParseObjectAttributes(c.Header)
	if err != nil {
		return err
	}
	info, err := e.Layer.CopyObject(c.Ctx, src, c.Route.Bucket, c.Route.Key, object.CopyOptions{
		SourceConditions: srcConds,
		Directive:        directive,
		Put:              put,
	})
	if err != nil {
		return err
	}
	setVersionHeader(c, info.VersionID)
	if src.VersionID != "" {
		c.RC.Response.Header.Set("x-amz-copy-source-version-id", src.VersionID)
	}
	sums, sumType := checksumFields(info.Checksum)
	res := xmlenc.CopyObjectResult{
		ETag:         headers.QuoteETag(info.ETag),
		LastModified: info.ModTime,
		ChecksumType: sumType,
		Checksums:    sums,
	}
	writeXML(c, 200, xmlenc.AppendCopyObject(make([]byte, 0, 256), &res))
	return nil
}
