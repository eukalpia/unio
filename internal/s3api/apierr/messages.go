package apierr

import "github.com/eukalpia/unio/internal/s3err"

// genericMessages are the client-safe messages used when an error has no
// explicit message.
var genericMessages = map[s3err.Code]string{
	s3err.AccessDenied:                "Access Denied",
	s3err.BadDigest:                   "The Content-MD5 you specified did not match what we received.",
	s3err.BucketAlreadyExists:         "The requested bucket name is not available.",
	s3err.BucketNotEmpty:              "The bucket you tried to delete is not empty.",
	s3err.EntityTooLarge:              "Your proposed upload exceeds the maximum allowed size.",
	s3err.EntityTooSmall:              "Your proposed upload is smaller than the minimum allowed object size.",
	s3err.IncompleteBody:              "You did not provide the number of bytes specified by the Content-Length HTTP header.",
	s3err.InternalError:               "We encountered an internal error. Please try again.",
	s3err.InvalidArgument:             "Invalid argument.",
	s3err.InvalidBucketName:           "The specified bucket is not valid.",
	s3err.InvalidDigest:               "The Content-MD5 you specified is not valid.",
	s3err.InvalidPart:                 "One or more of the specified parts could not be found.",
	s3err.InvalidPartOrder:            "The list of parts was not in ascending order.",
	s3err.InvalidRange:                "The requested range is not satisfiable",
	s3err.InvalidRequest:              "The request is not valid.",
	s3err.KeyTooLongError:             "Your key is too long.",
	s3err.MalformedXML:                "The XML you provided was not well-formed or did not validate against our published schema.",
	s3err.MetadataTooLarge:            "Your metadata headers exceed the maximum allowed metadata size.",
	s3err.MethodNotAllowed:            "The specified method is not allowed against this resource.",
	s3err.MissingContentLength:        "You must provide the Content-Length HTTP header.",
	s3err.NoSuchBucket:                "The specified bucket does not exist.",
	s3err.NoSuchKey:                   "The specified key does not exist.",
	s3err.NoSuchUpload:                "The specified upload does not exist.",
	s3err.NotImplemented:              "A header you provided implies functionality that is not implemented.",
	s3err.PreconditionFailed:          "At least one of the preconditions you specified did not hold.",
	s3err.RequestTimeout:              "Your socket connection to the server was not read from or written to within the timeout period.",
	s3err.ServiceUnavailable:          "Service is unable to handle request.",
	s3err.SignatureDoesNotMatch:       "The request signature we calculated does not match the signature you provided.",
	s3err.SlowDown:                    "Please reduce your request rate.",
	s3err.XAmzContentSHA256Mismatch:   "The provided 'x-amz-content-sha256' header does not match what was computed.",
	s3err.XAmzContentChecksumMismatch: "The trailing checksum did not match what we received.",
}
