package router

import (
	"net/url"

	"github.com/eukalpia/unio/internal/s3err"
)

// unsupportedSubresources are S3 subresources of operations outside the v0.1
// scope. A request carrying one of them is rejected with NotImplemented
// rather than silently treated as the base operation.
var unsupportedSubresources = [...]string{
	"accelerate", "acl", "analytics", "attributes", "cors", "encryption",
	"intelligent-tiering", "inventory", "legal-hold", "lifecycle", "logging",
	"metrics", "notification", "object-lock", "ownershipControls", "policy",
	"policyStatus", "publicAccessBlock", "replication", "requestPayment",
	"restore", "retention", "select", "tagging", "torrent", "versioning",
	"versions", "website",
}

// dispatch picks the operation from the method, the target and the query.
func dispatch(method, bucket, key string, q url.Values, copySource bool) (Op, error) {
	for _, sub := range unsupportedSubresources {
		if sub == "tagging" && key != "" {
			// Object tagging is supported; bucket tagging is not.
			continue
		}
		if sub == "versions" && method == "GET" && key == "" && bucket != "" {
			// ListObjectVersions is supported; nothing else takes ?versions.
			continue
		}
		if _, ok := q[sub]; ok {
			return OpUnknown, s3err.Errorf(s3err.NotImplemented, "this operation is not supported")
		}
	}
	switch {
	case bucket == "":
		if method == "GET" {
			return OpListBuckets, nil
		}
		return OpUnknown, methodNotAllowed()
	case key == "":
		return dispatchBucket(method, q)
	default:
		return dispatchObject(method, q, copySource)
	}
}

func dispatchBucket(method string, q url.Values) (Op, error) {
	switch method {
	case "PUT":
		if has(q, "uploadId", "partNumber", "uploads", "delete") {
			return OpUnknown, s3err.Errorf(s3err.InvalidRequest, "invalid bucket subresource")
		}
		return OpCreateBucket, nil
	case "HEAD":
		return OpHeadBucket, nil
	case "DELETE":
		return OpDeleteBucket, nil
	case "GET":
		switch {
		case has(q, "location"):
			return OpGetBucketLocation, nil
		case has(q, "uploads"):
			return OpListMultipartUploads, nil
		case has(q, "versions"):
			return OpListObjectVersions, nil
		case has(q, "list-type"):
			if q.Get("list-type") != "2" {
				return OpUnknown, s3err.Errorf(s3err.InvalidArgument, "unsupported list-type")
			}
			return OpListObjectsV2, nil
		default:
			return OpListObjectsV1, nil
		}
	case "POST":
		if has(q, "delete") {
			return OpDeleteObjects, nil
		}
		return OpUnknown, s3err.Errorf(s3err.NotImplemented, "this operation is not supported")
	}
	return OpUnknown, methodNotAllowed()
}

func dispatchObject(method string, q url.Values, copySource bool) (Op, error) {
	if has(q, "tagging") {
		return dispatchObjectTagging(method, q)
	}
	switch method {
	case "GET":
		if has(q, "uploadId") {
			return OpListParts, nil
		}
		return OpGetObject, nil
	case "HEAD":
		return OpHeadObject, nil
	case "PUT":
		return dispatchObjectPut(q, copySource)
	case "POST":
		switch {
		case has(q, "uploads"):
			return OpCreateMultipartUpload, nil
		case has(q, "uploadId"):
			return OpCompleteMultipartUpload, nil
		}
		return OpUnknown, s3err.Errorf(s3err.NotImplemented, "this operation is not supported")
	case "DELETE":
		if has(q, "uploadId") {
			return OpAbortMultipartUpload, nil
		}
		return OpDeleteObject, nil
	}
	return OpUnknown, methodNotAllowed()
}

// dispatchObjectTagging picks the tagging operation of an object. The tagging
// subresource cannot be combined with the multipart subresources.
func dispatchObjectTagging(method string, q url.Values) (Op, error) {
	if has(q, "uploads", "uploadId", "partNumber") {
		return OpUnknown, s3err.Errorf(s3err.InvalidRequest, "tagging cannot be combined with a multipart subresource")
	}
	switch method {
	case "GET":
		return OpGetObjectTagging, nil
	case "PUT":
		return OpPutObjectTagging, nil
	case "DELETE":
		return OpDeleteObjectTagging, nil
	}
	return OpUnknown, methodNotAllowed()
}

func dispatchObjectPut(q url.Values, copySource bool) (Op, error) {
	hasPart, hasUpload := has(q, "partNumber"), has(q, "uploadId")
	switch {
	case hasPart && hasUpload:
		if copySource {
			return OpUploadPartCopy, nil
		}
		return OpUploadPart, nil
	case hasPart || hasUpload:
		return OpUnknown, s3err.Errorf(s3err.InvalidArgument, "partNumber and uploadId must be given together")
	case copySource:
		return OpCopyObject, nil
	}
	return OpPutObject, nil
}

// has reports whether any of the named parameters is present, even when empty.
func has(q url.Values, names ...string) bool {
	for _, n := range names {
		if _, ok := q[n]; ok {
			return true
		}
	}
	return false
}

func methodNotAllowed() error {
	return s3err.Errorf(s3err.MethodNotAllowed, "the specified method is not allowed against this resource")
}
