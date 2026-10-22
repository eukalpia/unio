package handlers

import (
	"encoding/base64"
	"net/url"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/headers"
	"github.com/eukalpia/unio/internal/s3api/xmlenc"
	"github.com/eukalpia/unio/internal/s3err"
)

// defaultStorageClass is reported for every object, which has one class.
const defaultStorageClass = "STANDARD"

// ListObjectsV2 lists objects of a bucket, page by page. The continuation
// token is the base64 encoding of the last key of the previous page.
func (e *Env) ListObjectsV2(c *Call) error {
	q := c.Route.Query
	maxKeys, err := listPageSize(q)
	if err != nil {
		return err
	}
	encoding, err := encodingType(q)
	if err != nil {
		return err
	}
	after := q.Get("start-after")
	token := q.Get("continuation-token")
	if token != "" {
		raw, err := base64.StdEncoding.DecodeString(token)
		if err != nil {
			return s3err.Errorf(s3err.InvalidArgument, "the continuation token provided is incorrect")
		}
		after = string(raw)
	}
	fetchOwner := q.Get("fetch-owner") == "true"

	prefix, delimiter := q.Get("prefix"), q.Get("delimiter")
	page, err := e.listPage(c, prefix, delimiter, after, maxKeys)
	if err != nil {
		return err
	}
	res := xmlenc.ListObjectsV2Result{
		Name:              c.Route.Bucket,
		Prefix:            prefix,
		Delimiter:         delimiter,
		StartAfter:        q.Get("start-after"),
		ContinuationToken: token,
		EncodingType:      encoding,
		MaxKeys:           maxKeys,
		IsTruncated:       page.IsTruncated,
		Contents:          contents(page.Objects, fetchOwner, c.ownerName()),
		CommonPrefixes:    page.CommonPrefixes,
	}
	res.KeyCount = len(res.Contents) + len(res.CommonPrefixes)
	if page.IsTruncated {
		res.NextContinuationToken = base64.StdEncoding.EncodeToString([]byte(page.NextAfter))
	}
	writeXML(c, 200, xmlenc.AppendListObjectsV2(make([]byte, 0, 4096), &res))
	return nil
}

// ListObjectsV1 lists objects of a bucket with the marker-based paging of the
// original API.
func (e *Env) ListObjectsV1(c *Call) error {
	q := c.Route.Query
	maxKeys, err := listPageSize(q)
	if err != nil {
		return err
	}
	encoding, err := encodingType(q)
	if err != nil {
		return err
	}
	prefix, delimiter, marker := q.Get("prefix"), q.Get("delimiter"), q.Get("marker")
	page, err := e.listPage(c, prefix, delimiter, marker, maxKeys)
	if err != nil {
		return err
	}
	res := xmlenc.ListObjectsV1Result{
		Name:           c.Route.Bucket,
		Prefix:         prefix,
		Marker:         marker,
		Delimiter:      delimiter,
		EncodingType:   encoding,
		MaxKeys:        maxKeys,
		IsTruncated:    page.IsTruncated,
		Contents:       contents(page.Objects, false, ""),
		CommonPrefixes: page.CommonPrefixes,
	}
	if page.IsTruncated && delimiter != "" {
		res.NextMarker = page.NextAfter
	}
	writeXML(c, 200, xmlenc.AppendListObjectsV1(make([]byte, 0, 4096), &res))
	return nil
}

// listPage runs one listing page. A page size of zero returns an empty page
// without listing, after checking that the bucket exists.
func (e *Env) listPage(c *Call, prefix, delimiter, after string, maxKeys int) (object.ListResult, error) {
	if maxKeys == 0 {
		if _, err := e.Layer.GetBucketInfo(c.Ctx, c.Route.Bucket); err != nil {
			return object.ListResult{}, err
		}
		return object.ListResult{}, nil
	}
	return e.Layer.ListObjects(c.Ctx, c.Route.Bucket, object.ListOptions{
		Prefix:    prefix,
		Delimiter: delimiter,
		After:     after,
		MaxKeys:   maxKeys,
	})
}

// listPageSize parses max-keys: 0 to object.MaxListKeys, default MaxListKeys.
func listPageSize(q url.Values) (int, error) {
	return queryInt(q, "max-keys", object.MaxListKeys, 0, object.MaxListKeys)
}

// encodingType validates encoding-type, which may only be "url".
func encodingType(q url.Values) (string, error) {
	switch v := q.Get("encoding-type"); v {
	case "", "url":
		return v, nil
	default:
		return "", s3err.Errorf(s3err.InvalidArgument, "invalid encoding method specified in request")
	}
}

// contents converts layer objects into listing entries.
func contents(objs []object.Info, withOwner bool, owner string) []xmlenc.Content {
	out := make([]xmlenc.Content, 0, len(objs))
	for _, o := range objs {
		entry := xmlenc.Content{
			Key:          o.Key,
			LastModified: o.ModTime,
			ETag:         headers.QuoteETag(o.ETag),
			Size:         o.Size,
			StorageClass: defaultStorageClass,
		}
		if withOwner {
			entry.Owner = &xmlenc.Owner{ID: owner, DisplayName: owner}
		}
		out = append(out, entry)
	}
	return out
}
