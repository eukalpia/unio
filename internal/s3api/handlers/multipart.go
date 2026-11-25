package handlers

import (
	"net/url"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/headers"
	"github.com/eukalpia/unio/internal/s3api/xmldec"
	"github.com/eukalpia/unio/internal/s3api/xmlenc"
	"github.com/eukalpia/unio/internal/s3err"
)

// timeFormat is the S3 timestamp format of XML responses.
const timeFormat = "2006-01-02T15:04:05.000Z"

// CreateMultipartUpload starts a multipart upload and returns its id.
func (e *Env) CreateMultipartUpload(c *Call) error {
	attrs, err := headers.ParseObjectAttributes(c.Header)
	if err != nil {
		return err
	}
	uploadID, err := e.Layer.NewMultipartUpload(c.Ctx, c.Route.Bucket, c.Route.Key, attrs)
	if err != nil {
		return err
	}
	// S3 reports the checksum algorithm of the upload in a header, not in the
	// body, so that clients learn the algorithm the parts must carry.
	if attrs.ChecksumAlgorithm != object.ChecksumNone {
		c.RC.Response.Header.Set("x-amz-checksum-algorithm", attrs.ChecksumAlgorithm.String())
	}
	res := xmlenc.InitiateMultipartUploadResult{Bucket: c.Route.Bucket, Key: c.Route.Key, UploadID: uploadID}
	writeXML(c, 200, xmlenc.AppendInitiateMultipartUpload(make([]byte, 0, 256), &res))
	return nil
}

// UploadPart stores one part of a multipart upload.
func (e *Env) UploadPart(c *Call) error {
	num, uploadID, err := partParams(c.Route.Query)
	if err != nil {
		return err
	}
	body, _, err := c.objectBody(object.MaxPartSize)
	if err != nil {
		return err
	}
	part, err := e.Layer.PutObjectPart(c.Ctx, c.Route.Bucket, c.Route.Key, uploadID, num, body)
	if err != nil {
		return err
	}
	c.RC.Response.Header.Set("ETag", headers.QuoteETag(part.ETag))
	setChecksumHeaders(c, part.Checksum)
	writeStatus(c, 200)
	return nil
}

// UploadPartCopy stores one part copied from another object, optionally only
// a byte range of it (x-amz-copy-source-range).
func (e *Env) UploadPartCopy(c *Call) error {
	num, uploadID, err := partParams(c.Route.Query)
	if err != nil {
		return err
	}
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
	rng, err := headers.ParseCopySourceRange(c.Header.Get("X-Amz-Copy-Source-Range"))
	if err != nil {
		return err
	}
	part, err := e.Layer.CopyObjectPart(c.Ctx, src, c.Route.Bucket, c.Route.Key, uploadID, num, object.CopyOptions{
		SourceConditions: srcConds,
		SourceRange:      rng,
	})
	if err != nil {
		return err
	}
	if src.VersionID != "" {
		c.RC.Response.Header.Set("x-amz-copy-source-version-id", src.VersionID)
	}
	writeXML(c, 200, appendCopyPartResult(make([]byte, 0, 256), part))
	return nil
}

// appendCopyPartResult encodes the CopyPartResult document of UploadPartCopy.
// It is kept here because xmlenc covers the documents that are shared with
// other operations.
func appendCopyPartResult(dst []byte, part object.PartInfo) []byte {
	dst = append(dst, `<?xml version="1.0" encoding="UTF-8"?><CopyPartResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><LastModified>`...)
	dst = time.Unix(0, part.ModTime).UTC().AppendFormat(dst, timeFormat)
	dst = append(dst, `</LastModified><ETag>`...)
	dst = xmlenc.AppendEscape(dst, headers.QuoteETag(part.ETag))
	return append(dst, `</ETag></CopyPartResult>`...)
}

// CompleteMultipartUpload turns the listed parts into the object. The body
// names the parts; the layer validates their order and ETags atomically.
func (e *Env) CompleteMultipartUpload(c *Call) error {
	uploadID, err := uploadIDParam(c.Route.Query)
	if err != nil {
		return err
	}
	conds, err := headers.ParseConditions(c.Header)
	if err != nil {
		return err
	}
	// A digest of the whole object sent on completion is not verified by the
	// object layer, so the request is refused rather than committed unverified.
	if headers.HasChecksumDigest(c.Header) {
		return s3err.Errorf(s3err.NotImplemented, "a checksum sent on completion is not supported by this server")
	}
	data, err := c.readXML()
	if err != nil {
		return err
	}
	parts, err := xmldec.ParseCompleteMultipartUpload(data)
	if err != nil {
		return err
	}
	info, err := e.Layer.CompleteMultipartUpload(c.Ctx, c.Route.Bucket, c.Route.Key, uploadID, parts, object.PutOptions{Conditions: conds})
	if err != nil {
		return err
	}
	setVersionHeader(c, info.VersionID)
	sums, sumType := checksumFields(info.Checksum)
	res := xmlenc.CompleteMultipartUploadResult{
		Location:     c.location(),
		Bucket:       c.Route.Bucket,
		Key:          c.Route.Key,
		ETag:         headers.QuoteETag(info.ETag),
		ChecksumType: sumType,
		Checksums:    sums,
	}
	writeXML(c, 200, xmlenc.AppendCompleteMultipartUpload(make([]byte, 0, 512), &res))
	return nil
}

// location returns the absolute URL of the request's object.
func (c *Call) location() string {
	scheme := string(c.RC.Request.URI().Scheme())
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + string(c.RC.Request.Host()) + "/" + c.Route.Bucket + "/" + c.Route.Key
}

// AbortMultipartUpload discards an upload and its parts.
func (e *Env) AbortMultipartUpload(c *Call) error {
	uploadID, err := uploadIDParam(c.Route.Query)
	if err != nil {
		return err
	}
	if err := e.Layer.AbortMultipartUpload(c.Ctx, c.Route.Bucket, c.Route.Key, uploadID); err != nil {
		return err
	}
	writeStatus(c, 204)
	return nil
}

// ListParts lists the parts of an upload, page by page.
func (e *Env) ListParts(c *Call) error {
	q := c.Route.Query
	uploadID, err := uploadIDParam(q)
	if err != nil {
		return err
	}
	maxParts, err := queryInt(q, "max-parts", object.MaxListKeys, 1, object.MaxListKeys)
	if err != nil {
		return err
	}
	marker, err := queryInt(q, "part-number-marker", 0, 0, object.MaxPartNumber)
	if err != nil {
		return err
	}
	page, err := e.Layer.ListParts(c.Ctx, c.Route.Bucket, c.Route.Key, uploadID, object.ListPartsOptions{
		PartNumberMarker: marker,
		MaxParts:         maxParts,
	})
	if err != nil {
		return err
	}
	owner := c.owner()
	res := xmlenc.ListPartsResult{
		Bucket:               c.Route.Bucket,
		Key:                  c.Route.Key,
		UploadID:             uploadID,
		StorageClass:         defaultStorageClass,
		ChecksumAlgorithm:    page.ChecksumAlgorithm.String(),
		Owner:                &owner,
		PartNumberMarker:     marker,
		NextPartNumberMarker: page.NextPartNumberMarker,
		MaxParts:             maxParts,
		IsTruncated:          page.IsTruncated,
		Parts:                make([]xmlenc.Part, 0, len(page.Parts)),
	}
	for _, p := range page.Parts {
		sums, _ := checksumFields(p.Checksum)
		res.Parts = append(res.Parts, xmlenc.Part{
			PartNumber:   p.PartNumber,
			LastModified: time.Unix(0, p.ModTime),
			ETag:         headers.QuoteETag(p.ETag),
			Size:         p.Size,
			Checksums:    sums,
		})
	}
	writeXML(c, 200, xmlenc.AppendListParts(make([]byte, 0, 1024), &res))
	return nil
}

// ListMultipartUploads lists the uploads in progress of a bucket.
func (e *Env) ListMultipartUploads(c *Call) error {
	q := c.Route.Query
	maxUploads, err := queryInt(q, "max-uploads", object.MaxListKeys, 1, object.MaxListKeys)
	if err != nil {
		return err
	}
	encoding, err := encodingType(q)
	if err != nil {
		return err
	}
	prefix, delimiter := q.Get("prefix"), q.Get("delimiter")
	keyMarker, uploadIDMarker := q.Get("key-marker"), q.Get("upload-id-marker")
	page, err := e.Layer.ListMultipartUploads(c.Ctx, c.Route.Bucket, object.ListUploadsOptions{
		Prefix:         prefix,
		Delimiter:      delimiter,
		KeyMarker:      keyMarker,
		UploadIDMarker: uploadIDMarker,
		MaxUploads:     maxUploads,
	})
	if err != nil {
		return err
	}
	owner := c.owner()
	res := xmlenc.ListMultipartUploadsResult{
		Bucket:             c.Route.Bucket,
		KeyMarker:          keyMarker,
		UploadIDMarker:     uploadIDMarker,
		NextKeyMarker:      page.NextKeyMarker,
		NextUploadIDMarker: page.NextUploadIDMarker,
		Prefix:             prefix,
		Delimiter:          delimiter,
		EncodingType:       encoding,
		MaxUploads:         maxUploads,
		IsTruncated:        page.IsTruncated,
		Uploads:            make([]xmlenc.Upload, 0, len(page.Uploads)),
		CommonPrefixes:     page.CommonPrefixes,
	}
	for _, u := range page.Uploads {
		res.Uploads = append(res.Uploads, xmlenc.Upload{
			Key:          u.Key,
			UploadID:     u.UploadID,
			StorageClass: defaultStorageClass,
			Initiated:    time.Unix(0, u.Initiated),
			Owner:        &owner,
		})
	}
	writeXML(c, 200, xmlenc.AppendListMultipartUploads(make([]byte, 0, 1024), &res))
	return nil
}

// owner returns the owner element for listings.
func (c *Call) owner() xmlenc.Owner {
	name := c.ownerName()
	return xmlenc.Owner{ID: name, DisplayName: name}
}

// partParams reads the partNumber and uploadId parameters of UploadPart and
// UploadPartCopy.
func partParams(q url.Values) (int, string, error) {
	num, err := queryInt(q, "partNumber", 0, 1, object.MaxPartNumber)
	if err != nil {
		return 0, "", err
	}
	if num == 0 {
		return 0, "", s3err.Errorf(s3err.InvalidArgument, "partNumber must be an integer between 1 and %d", object.MaxPartNumber)
	}
	uploadID, err := uploadIDParam(q)
	return num, uploadID, err
}

// uploadIDParam reads the uploadId parameter, which every multipart call
// requires.
func uploadIDParam(q url.Values) (string, error) {
	id := q.Get("uploadId")
	if id == "" {
		return "", s3err.Errorf(s3err.InvalidArgument, "uploadId is required")
	}
	return id, nil
}
