package handlers

import (
	"fmt"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/headers"
)

// responseOverrides maps the response-* query parameters, which let an
// authorized client change response headers of a GET, to the header they set.
var responseOverrides = [...][2]string{
	{"response-content-type", "Content-Type"},
	{"response-content-language", "Content-Language"},
	{"response-expires", "Expires"},
	{"response-cache-control", "Cache-Control"},
	{"response-content-disposition", "Content-Disposition"},
	{"response-content-encoding", "Content-Encoding"},
}

// GetObject serves GetObject, or HeadObject when head is true. A range request
// returns 206 with Content-Range; the body streams from the layer reader and
// is closed by the transport.
func (e *Env) GetObject(c *Call, head bool) error {
	q := c.Route.Query
	rng, err := headers.ParseRange(c.Header.Get("Range"))
	if err != nil {
		return err
	}
	conds, err := headers.ParseConditions(c.Header)
	if err != nil {
		return err
	}
	partNumber, err := queryInt(q, "partNumber", 0, 1, object.MaxPartNumber)
	if err != nil {
		return err
	}
	opts := object.GetOptions{
		VersionID:  q.Get("versionId"),
		Range:      rng,
		PartNumber: partNumber,
		Conditions: conds,
	}
	checksumMode := headers.ParseChecksumMode(c.Header)

	if head {
		info, err := e.Layer.HeadObject(c.Ctx, c.Route.Bucket, c.Route.Key, opts)
		if err != nil {
			return err
		}
		length, err := intLength(info.Size)
		if err != nil {
			return err
		}
		setObjectHeaders(c, info, checksumMode)
		applyOverrides(c)
		c.RC.SetStatusCode(200)
		c.RC.Response.Header.SetContentLength(length)
		return nil
	}

	reader, err := e.Layer.GetObject(c.Ctx, c.Route.Bucket, c.Route.Key, opts)
	if err != nil {
		return err
	}
	length, err := intLength(reader.Length)
	if err != nil {
		_ = reader.Close()
		return err
	}
	if reader.Length == 0 {
		_ = reader.Close()
		setObjectHeaders(c, reader.Info, checksumMode)
		applyOverrides(c)
		c.RC.SetStatusCode(rangeStatus(c, rng, reader))
		c.RC.Response.Header.SetContentLength(0)
		return nil
	}
	// The copy buffer is acquired before any header is set, so that a budget
	// or deadline failure is reported as a normal S3 error.
	buf, err := e.CopyPool.Get(c.Ctx)
	if err != nil {
		_ = reader.Close()
		return err
	}
	setObjectHeaders(c, reader.Info, checksumMode)
	applyOverrides(c)
	c.RC.SetStatusCode(rangeStatus(c, rng, reader))
	c.RC.Response.Header.SetContentLength(length)
	e.streamObject(c, reader, buf)
	return nil
}

// rangeStatus returns 206 with Content-Range for a range request, and 200 for
// a full read.
func rangeStatus(c *Call, rng *object.HTTPRange, reader *object.Reader) int {
	if rng == nil {
		return 200
	}
	c.RC.Response.Header.Set("Content-Range", contentRange(reader.Offset, reader.Length, reader.Info.Size))
	return 206
}

// contentRange formats the Content-Range value of a partial response.
func contentRange(offset, length, size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, size)
}

// setObjectHeaders sets the representation headers of an object: content
// attributes, ETag, Last-Modified, user metadata and version information.
// The checksum is sent only when the client asked for it with
// x-amz-checksum-mode.
func setObjectHeaders(c *Call, info object.Info, checksumMode bool) {
	h := &c.RC.Response.Header
	contentType := info.ContentType
	if contentType == "" {
		contentType = "binary/octet-stream"
	}
	h.Set("Content-Type", contentType)
	h.Set("ETag", headers.QuoteETag(info.ETag))
	h.Set("Last-Modified", httpTime(info.ModTime))
	h.Set("Accept-Ranges", "bytes")
	setIfSet(c, "Content-Encoding", info.ContentEncoding)
	setIfSet(c, "Content-Disposition", info.ContentDisposition)
	setIfSet(c, "Content-Language", info.ContentLanguage)
	setIfSet(c, "Cache-Control", info.CacheControl)
	setIfSet(c, "Expires", info.Expires)
	for k, v := range info.UserMetadata {
		h.Set("x-amz-meta-"+k, v)
	}
	if info.StorageClass != "" && info.StorageClass != "STANDARD" {
		h.Set("x-amz-storage-class", info.StorageClass)
	}
	setVersionHeader(c, info.VersionID)
	if info.DeleteMarker {
		h.Set("x-amz-delete-marker", "true")
	}
	if checksumMode {
		setChecksumHeaders(c, info.Checksum)
	}
}

// setIfSet sets a response header when the value is not empty.
func setIfSet(c *Call, name, value string) {
	if value != "" {
		c.RC.Response.Header.Set(name, value)
	}
}

// applyOverrides applies the response-* query parameters.
func applyOverrides(c *Call) {
	for _, o := range responseOverrides {
		if v := c.Route.Query.Get(o[0]); v != "" {
			c.RC.Response.Header.Set(o[1], v)
		}
	}
}
