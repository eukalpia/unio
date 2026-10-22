package handlers

import (
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/xmldec"
	"github.com/eukalpia/unio/internal/s3api/xmlenc"
)

// PutObjectTagging replaces the tag set of one object version with the tags of
// a Tagging document. An empty TagSet removes every tag. The data, the ETag and
// the modification time do not change, and no version is created.
func (e *Env) PutObjectTagging(c *Call) error {
	data, err := c.readXML()
	if err != nil {
		return err
	}
	tags, err := xmldec.ParseTagging(data)
	if err != nil {
		return err
	}
	info, err := e.Layer.PutObjectTags(c.Ctx, c.Route.Bucket, c.Route.Key, object.TagOptions{
		VersionID: c.Route.Query.Get("versionId"),
		Tags:      tags,
	})
	if err != nil {
		return err
	}
	setVersionHeader(c, info.VersionID)
	writeStatus(c, 200)
	return nil
}

// GetObjectTagging returns the tag set of one object version as a Tagging
// document.
func (e *Env) GetObjectTagging(c *Call) error {
	info, tags, err := e.Layer.GetObjectTags(c.Ctx, c.Route.Bucket, c.Route.Key, c.Route.Query.Get("versionId"))
	if err != nil {
		return err
	}
	setVersionHeader(c, info.VersionID)
	writeXML(c, 200, xmlenc.AppendTagging(make([]byte, 0, 256), tags))
	return nil
}

// DeleteObjectTagging removes every tag of one object version.
func (e *Env) DeleteObjectTagging(c *Call) error {
	info, err := e.Layer.DeleteObjectTags(c.Ctx, c.Route.Bucket, c.Route.Key, c.Route.Query.Get("versionId"))
	if err != nil {
		return err
	}
	setVersionHeader(c, info.VersionID)
	writeStatus(c, 204)
	return nil
}
