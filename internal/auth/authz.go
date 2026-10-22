package auth

import "context"

// Action is an S3 IAM action name such as "s3:GetObject".
type Action string

// IAM actions used by the v0.1 API.
const (
	ActionCreateBucket               Action = "s3:CreateBucket"
	ActionDeleteBucket               Action = "s3:DeleteBucket"
	ActionListAllMyBuckets           Action = "s3:ListAllMyBuckets"
	ActionListBucket                 Action = "s3:ListBucket"
	ActionListBucketMultipartUploads Action = "s3:ListBucketMultipartUploads"
	ActionGetBucketLocation          Action = "s3:GetBucketLocation"
	ActionGetObject                  Action = "s3:GetObject"
	ActionPutObject                  Action = "s3:PutObject"
	ActionDeleteObject               Action = "s3:DeleteObject"
	ActionAbortMultipartUpload       Action = "s3:AbortMultipartUpload"
	ActionListMultipartUploadParts   Action = "s3:ListMultipartUploadParts"
	ActionGetObjectTagging           Action = "s3:GetObjectTagging"
	ActionPutObjectTagging           Action = "s3:PutObjectTagging"
	ActionDeleteObjectTagging        Action = "s3:DeleteObjectTagging"
	ActionAdmin                      Action = "unio:Admin"
)

// Authorizer decides whether an identity may perform an action.
type Authorizer interface {
	// Authorize returns nil or an *s3err.Error with AccessDenied. bucket and
	// key may be empty for service-level actions.
	Authorize(ctx context.Context, id Identity, action Action, bucket, key string) error
}
