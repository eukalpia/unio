package admin

import (
	"net/http"
	"time"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// objectView is one object of an object listing.
type objectView struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	ETag         string `json:"etag"`
	LastModified string `json:"lastModified"`
}

// listingView is the body of GET buckets/{name}/objects. NextToken is null on
// the last page; otherwise it is the token of the next request.
type listingView struct {
	Prefixes  []string     `json:"prefixes"`
	Objects   []objectView `json:"objects"`
	NextToken *string      `json:"nextToken"`
}

// listObjects answers GET buckets/{name}/objects.
func (s *Service) listObjects(c *call) (Response, error) {
	bucket := c.params[0]
	if err := object.ValidateBucketName(bucket); err != nil {
		return Response{}, err
	}
	prefix, err := boundedQuery(c.query, "prefix", object.MaxKeyLength)
	if err != nil {
		return Response{}, err
	}
	delimiter, err := boundedQuery(c.query, "delimiter", maxParamLen)
	if err != nil {
		return Response{}, err
	}
	token, err := boundedQuery(c.query, "token", object.MaxKeyLength)
	if err != nil {
		return Response{}, err
	}
	limit, err := listLimitParam(c.query)
	if err != nil {
		return Response{}, err
	}
	res, err := s.cfg.Layer.ListObjects(c.ctx, bucket, object.ListOptions{
		Prefix:    prefix,
		Delimiter: delimiter,
		After:     token,
		MaxKeys:   limit,
	})
	if err != nil {
		return Response{}, err
	}
	view := listingView{
		Prefixes: make([]string, 0, len(res.CommonPrefixes)),
		Objects:  make([]objectView, 0, len(res.Objects)),
	}
	view.Prefixes = append(view.Prefixes, res.CommonPrefixes...)
	for _, o := range res.Objects {
		view.Objects = append(view.Objects, objectView{
			Key:          o.Key,
			Size:         o.Size,
			ETag:         o.ETag,
			LastModified: o.ModTime.UTC().Format(time.RFC3339),
		})
	}
	if res.IsTruncated {
		next := res.NextAfter
		view.NextToken = &next
	}
	return jsonResponse(http.StatusOK, view)
}

// deleteObject answers DELETE buckets/{name}/objects?key=. Deleting a key that
// does not exist succeeds, as it does on the S3 API.
func (s *Service) deleteObject(c *call) (Response, error) {
	bucket := c.params[0]
	if err := object.ValidateBucketName(bucket); err != nil {
		return Response{}, err
	}
	key, err := requiredQuery(c.query, "key", maxKeyQuery)
	if err != nil {
		return Response{}, err
	}
	if err := object.ValidateKey(key); err != nil {
		return Response{}, err
	}
	if _, err := s.cfg.Layer.DeleteObject(c.ctx, bucket, key, object.DeleteOptions{}); err != nil && !s3err.Is(err, s3err.NoSuchKey) {
		return Response{}, err
	}
	return emptyResponse(http.StatusNoContent), nil
}
