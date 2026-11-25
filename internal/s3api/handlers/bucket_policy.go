package handlers

import (
	"io"

	"github.com/eukalpia/unio/internal/auth/policy"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/headers"
	"github.com/eukalpia/unio/internal/s3err"
)

// GetBucketPolicy returns the stored policy document unchanged, or
// NoSuchBucketPolicy when the bucket has none.
func (e *Env) GetBucketPolicy(c *Call) error {
	info, err := e.Layer.GetBucketInfo(c.Ctx, c.Route.Bucket)
	if err != nil {
		return err
	}
	if len(info.Policy) == 0 {
		return s3err.Errorf(s3err.NoSuchBucketPolicy, "the bucket policy does not exist")
	}
	c.RC.Data(200, "application/json", info.Policy)
	return nil
}

// PutBucketPolicy validates the document with policy.ParseBucket and stores
// it as sent. A document the parser refuses is MalformedPolicy and nothing is
// stored, so a policy is never kept in a form that would be read differently
// from how it was written.
func (e *Env) PutBucketPolicy(c *Call) error {
	doc, err := c.readPolicy()
	if err != nil {
		return err
	}
	if len(doc) == 0 {
		return s3err.Errorf(s3err.MalformedPolicy, "the policy document is empty")
	}
	if _, err := policy.ParseBucket(doc, c.Route.Bucket); err != nil {
		return s3err.Wrap(s3err.MalformedPolicy, err)
	}
	if err := e.Layer.SetBucketPolicy(c.Ctx, c.Route.Bucket, doc); err != nil {
		return err
	}
	e.policyChanged(c.Route.Bucket)
	writeStatus(c, 204)
	return nil
}

// DeleteBucketPolicy removes the policy. Removing an absent policy succeeds,
// as in S3.
func (e *Env) DeleteBucketPolicy(c *Call) error {
	if err := e.Layer.SetBucketPolicy(c.Ctx, c.Route.Bucket, nil); err != nil {
		return err
	}
	e.policyChanged(c.Route.Bucket)
	writeStatus(c, 204)
	return nil
}

func (e *Env) policyChanged(bucket string) {
	if e.PolicyChanged != nil {
		e.PolicyChanged(bucket)
	}
}

// readPolicy reads the policy document in full, verified and bounded by
// object.MaxBucketPolicySize.
func (c *Call) readPolicy() ([]byte, error) {
	if c.Auth.DecodedLength > object.MaxBucketPolicySize {
		return nil, s3err.Errorf(s3err.MalformedPolicy, "the policy document exceeds %d bytes", object.MaxBucketPolicySize)
	}
	md5, err := headers.ParseContentMD5(c.Header)
	if err != nil {
		return nil, err
	}
	body, err := c.verifiedBody(md5, headers.ChecksumRequest{})
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(body, object.MaxBucketPolicySize+1))
	if err != nil {
		if s3err.CodeOf(err) == s3err.InternalError {
			return nil, s3err.Wrap(s3err.IncompleteBody, err)
		}
		return nil, err
	}
	if len(data) > object.MaxBucketPolicySize {
		return nil, s3err.Errorf(s3err.MalformedPolicy, "the policy document exceeds %d bytes", object.MaxBucketPolicySize)
	}
	return data, nil
}
