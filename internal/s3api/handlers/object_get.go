package handlers

import (
	"errors"
	"fmt"
	"math"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/headers"
	"github.com/eukalpia/unio/internal/s3err"
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
	// A number beyond the parts of the object is not a malformed request: the
	// layer answers InvalidPartNumber for it. Only a number that cannot be an
	// int32 is rejected here.
	partNumber, err := queryInt(q, "partNumber", 0, 1, math.MaxInt32)
	if err != nil {
		return err
	}
	opts := object.GetOptions{
		VersionID:  q.Get("versionId"),
		Range:      rng,
		PartNumber: partNumber,
		Conditions: conds,
	}
	// A partial read is not checked against the checksum of the whole object, so
	// its response carries no checksum, as S3 does.
	checksumMode := headers.ParseChecksumMode(c.Header) && rng == nil

	if head {
		info, err := e.Layer.HeadObject(c.Ctx, c.Route.Bucket, c.Route.Key, opts)
		if err != nil {
			return readError(c, partNumber, err)
		}
		if info.Size < 0 {
			return setContentLength(&c.RC.Response.Header, info.Size)
		}
		setObjectHeaders(c, info, checksumMode)
		applyOverrides(c)
		c.RC.SetStatusCode(200)
		return setContentLength(&c.RC.Response.Header, info.Size)
	}

	reader, err := e.Layer.GetObject(c.Ctx, c.Route.Bucket, c.Route.Key, opts)
	if err != nil {
		return readError(c, partNumber, err)
	}
	if reader.Length < 0 {
		_ = reader.Close()
		return setContentLength(&c.RC.Response.Header, reader.Length)
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
	if err := setContentLength(&c.RC.Response.Header, reader.Length); err != nil {
		buf.Release()
		_ = reader.Close()
		return err
	}
	e.streamObject(c, reader, buf)
	return nil
}

// readError returns the error of a failed read, as the client sees it. A 304
// response carries the ETag and Last-Modified of the object, as RFC 9110
// requires, and the layer reports them in the cause of the NotModified error.
// A part number that names no part of the object is InvalidPartNumber, which S3
// answers with 416; the layer reports it as InvalidArgument with a cause that
// says so.
func readError(c *Call, partNumber int, err error) error {
	var validators interface{ NotModifiedInfo() object.Info }
	if s3err.Is(err, s3err.NotModified) && errors.As(err, &validators) {
		info := validators.NotModifiedInfo()
		h := &c.RC.Response.Header
		h.Set("ETag", headers.QuoteETag(info.ETag))
		h.Set("Last-Modified", httpTime(info.ModTime))
	}
	var missing interface{ NoSuchPart() bool }
	if partNumber > 0 && errors.As(err, &missing) && missing.NoSuchPart() {
		return s3err.Errorf(s3err.InvalidPartNumber, "The requested partnumber is not satisfiable.")
	}
	return err
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
