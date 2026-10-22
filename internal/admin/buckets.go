package admin

import (
	"net/http"
	"time"

	"github.com/eukalpia/unio/internal/object"
)

// bucketView is one bucket of GET buckets. Versioning is "Unversioned",
// "Enabled" or "Suspended".
type bucketView struct {
	Name       string `json:"name"`
	Created    string `json:"created"`
	Versioning string `json:"versioning"`
}

// nameBody is the body of POST buckets.
type nameBody struct {
	Name string `json:"name"`
}

// nameReply is the body of a 201 for a named resource.
type nameReply struct {
	Name string `json:"name"`
}

// listBuckets answers GET buckets.
func (s *Service) listBuckets(c *call) (Response, error) {
	infos, err := s.cfg.Layer.ListBuckets(c.ctx)
	if err != nil {
		return Response{}, err
	}
	out := make([]bucketView, 0, len(infos))
	for _, b := range infos {
		out = append(out, bucketView{
			Name:       b.Name,
			Created:    b.Created.UTC().Format(time.RFC3339),
			Versioning: versioningName(b.Versioning),
		})
	}
	return jsonResponse(http.StatusOK, out)
}

// versioningName returns the name a bucket's versioning state has in the API.
func versioningName(v object.VersioningState) string {
	switch v {
	case object.VersioningEnabled:
		return "Enabled"
	case object.VersioningSuspended:
		return "Suspended"
	}
	return "Unversioned"
}

// createBucket answers POST buckets. The bucket is owned by the session's
// user, as the S3 front end would own it.
func (s *Service) createBucket(c *call) (Response, error) {
	var in nameBody
	if err := decodeBody(c, &in); err != nil {
		return Response{}, err
	}
	if err := object.ValidateBucketName(in.Name); err != nil {
		return Response{}, err
	}
	if _, err := s.cfg.Layer.MakeBucket(c.ctx, in.Name, object.MakeBucketOptions{Owner: bucketOwner(c.p)}); err != nil {
		return Response{}, err
	}
	return jsonResponse(http.StatusCreated, nameReply(in))
}

// deleteBucket answers DELETE buckets/{name}. A bucket that holds objects is
// refused with BucketNotEmpty, and is never emptied by this call.
func (s *Service) deleteBucket(c *call) (Response, error) {
	name := c.params[0]
	if err := object.ValidateBucketName(name); err != nil {
		return Response{}, err
	}
	if err := s.cfg.Layer.DeleteBucket(c.ctx, name, object.DeleteBucketOptions{}); err != nil {
		return Response{}, err
	}
	return emptyResponse(http.StatusNoContent), nil
}
