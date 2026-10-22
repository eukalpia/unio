package handlers

import (
	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/apierr"
	"github.com/eukalpia/unio/internal/s3api/headers"
	"github.com/eukalpia/unio/internal/s3api/xmldec"
	"github.com/eukalpia/unio/internal/s3api/xmlenc"
)

// DeleteObject deletes one object version. Deleting a missing key succeeds,
// as in S3.
func (e *Env) DeleteObject(c *Call) error {
	conds, err := headers.ParseConditions(c.Header)
	if err != nil {
		return err
	}
	res, err := e.Layer.DeleteObject(c.Ctx, c.Route.Bucket, c.Route.Key, object.DeleteOptions{
		VersionID:  c.Route.Query.Get("versionId"),
		Conditions: conds,
	})
	if err != nil {
		return err
	}
	if res.DeleteMarker {
		c.RC.Response.Header.Set("x-amz-delete-marker", "true")
	}
	setVersionHeader(c, res.DeleteMarkerVersionID)
	writeStatus(c, 204)
	return nil
}

// DeleteObjects deletes up to object.MaxDeleteObjects keys named in an XML
// body. Each key is authorized separately; a key the caller may not delete is
// reported as an error entry, not as a failed request. Per-key failures from
// the layer are reported the same way.
func (e *Env) DeleteObjects(c *Call) error {
	data, err := c.readXML()
	if err != nil {
		return err
	}
	req, err := xmldec.ParseDeleteObjects(data)
	if err != nil {
		return err
	}
	bucket := c.Route.Bucket
	var failures []xmlenc.DeleteError
	allowed := make([]object.ToDelete, 0, len(req.Objects))
	for _, o := range req.Objects {
		if err := c.Authorize(auth.ActionDeleteObject, bucket, o.Key); err != nil {
			failures = append(failures, deleteError(o.Key, o.VersionID, err))
			continue
		}
		allowed = append(allowed, o)
	}

	results, err := e.Layer.DeleteObjects(c.Ctx, bucket, allowed)
	if err != nil {
		return err
	}
	out := xmlenc.DeleteResult{}
	for _, r := range results {
		if r.Err != nil {
			failures = append(failures, deleteError(r.Key, r.VersionID, r.Err))
			continue
		}
		if !req.Quiet {
			out.Deleted = append(out.Deleted, xmlenc.DeletedObject{
				Key:                   r.Key,
				VersionID:             r.VersionID,
				DeleteMarker:          r.DeleteMarker,
				DeleteMarkerVersionID: r.DeleteMarkerVersionID,
			})
		}
	}
	out.Errors = failures
	writeXML(c, 200, xmlenc.AppendDeleteResult(make([]byte, 0, 256), &out))
	return nil
}

// deleteError describes a failed key of a DeleteObjects request.
func deleteError(key, versionID string, err error) xmlenc.DeleteError {
	code, msg := apierr.Describe(err)
	return xmlenc.DeleteError{Key: key, VersionID: versionID, Code: string(code), Message: msg}
}
