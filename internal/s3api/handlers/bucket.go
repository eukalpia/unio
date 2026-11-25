package handlers

import (
	"encoding/base64"
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/xmldec"
	"github.com/eukalpia/unio/internal/s3api/xmlenc"
	"github.com/eukalpia/unio/internal/s3err"
)

// usEast1 is the region that S3 reports as an empty location constraint.
const usEast1 = "us-east-1"

// reservedBucket is the bucket name that the service namespace /unio/ owns, so
// it can never name a bucket.
const reservedBucket = "unio"

// CreateBucket creates a bucket. The optional body names the location, which
// must be this deployment's region.
func (e *Env) CreateBucket(c *Call) error {
	if c.Route.Bucket == reservedBucket {
		return s3err.Errorf(s3err.InvalidBucketName, "the bucket name is reserved for the service namespace")
	}
	location := ""
	if c.hasPayload() {
		data, err := c.readXML()
		if err != nil {
			return err
		}
		if location, err = xmldec.ParseCreateBucketConfiguration(data); err != nil {
			return err
		}
	}
	if location != "" && location != e.Region {
		return s3err.Errorf(s3err.InvalidLocationConstraint, "the specified location constraint is not valid for this deployment")
	}
	if _, err := e.Layer.MakeBucket(c.Ctx, c.Route.Bucket, object.MakeBucketOptions{Owner: c.ownerName()}); err != nil {
		// The default region of S3 accepts a request for a bucket the caller
		// already owns and answers 200. Other regions answer with
		// BucketAlreadyOwnedByYou, which is what this deployment does outside
		// us-east-1.
		if e.Region != usEast1 || !s3err.Is(err, s3err.BucketAlreadyOwnedByYou) {
			return err
		}
	}
	c.RC.Response.Header.Set("Location", "/"+c.Route.Bucket)
	writeStatus(c, 200)
	return nil
}

// HeadBucket reports whether a bucket exists and is accessible.
func (e *Env) HeadBucket(c *Call) error {
	if _, err := e.Layer.GetBucketInfo(c.Ctx, c.Route.Bucket); err != nil {
		return err
	}
	c.RC.Response.Header.Set("x-amz-bucket-region", e.Region)
	writeStatus(c, 200)
	return nil
}

// DeleteBucket deletes an empty bucket.
func (e *Env) DeleteBucket(c *Call) error {
	if err := e.Layer.DeleteBucket(c.Ctx, c.Route.Bucket, object.DeleteBucketOptions{}); err != nil {
		return err
	}
	writeStatus(c, 204)
	return nil
}

// ListBuckets lists the buckets of the deployment, a page at a time. The
// optional prefix keeps the buckets whose names start with it. max-buckets
// bounds the page, and a response that has more buckets carries the token for
// the next page, which names the last bucket of this one.
func (e *Env) ListBuckets(c *Call) error {
	q := c.Route.Query
	maxBuckets, err := queryInt(q, "max-buckets", maxBucketsPage, 1, maxBucketsPage)
	if err != nil {
		return err
	}
	after, err := bucketsAfter(q.Get("continuation-token"))
	if err != nil {
		return err
	}
	prefix := q.Get("prefix")
	buckets, err := e.Layer.ListBuckets(c.Ctx)
	if err != nil {
		return err
	}
	owner := c.ownerName()
	res := xmlenc.ListBucketsResult{
		Owner:   xmlenc.Owner{ID: owner, DisplayName: owner},
		Buckets: make([]xmlenc.Bucket, 0, min(len(buckets), maxBuckets)),
		Prefix:  prefix,
	}
	// The layer returns buckets ordered by name, so the page is the run of
	// matching buckets that follow the token.
	for _, b := range buckets {
		if (after != "" && b.Name <= after) || !strings.HasPrefix(b.Name, prefix) {
			continue
		}
		if len(res.Buckets) == maxBuckets {
			res.ContinuationToken = base64.StdEncoding.EncodeToString([]byte(res.Buckets[len(res.Buckets)-1].Name))
			break
		}
		res.Buckets = append(res.Buckets, xmlenc.Bucket{Name: b.Name, CreationDate: b.Created})
	}
	writeXML(c, 200, xmlenc.AppendListBuckets(make([]byte, 0, 256+len(res.Buckets)*96), &res))
	return nil
}

// maxBucketsPage is the largest page of ListBuckets and its default size.
const maxBucketsPage = 10000

// bucketsAfter decodes the continuation token of ListBuckets into the name of
// the last bucket of the previous page. An empty token starts at the first
// bucket.
func bucketsAfter(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", s3err.Errorf(s3err.InvalidArgument, "the continuation token provided is incorrect")
	}
	return string(raw), nil
}

// GetBucketLocation reports the region of a bucket. The us-east-1 region is
// reported as an empty constraint, as S3 does.
func (e *Env) GetBucketLocation(c *Call) error {
	if _, err := e.Layer.GetBucketInfo(c.Ctx, c.Route.Bucket); err != nil {
		return err
	}
	region := e.Region
	if region == usEast1 {
		region = ""
	}
	writeXML(c, 200, xmlenc.AppendLocationConstraint(make([]byte, 0, 256), region))
	return nil
}
