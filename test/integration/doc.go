// Package integration is a black-box test suite for S3-compatible endpoints.
//
// The suite talks to a running endpoint through the AWS SDK for Go v2 with
// path-style addressing. It is configured only through environment variables:
//
//	UNIO_TEST_ENDPOINT    base URL, for example http://localhost:9001 (required)
//	UNIO_TEST_ACCESS_KEY  access key ID (required when the endpoint is set)
//	UNIO_TEST_SECRET_KEY  secret access key (required when the endpoint is set)
//	UNIO_TEST_REGION      signing region (optional, default us-east-1)
//
// When UNIO_TEST_ENDPOINT is unset, every endpoint test is skipped with a
// message and only the configuration tests run. Each test creates its own
// bucket with a random name and removes everything it created.
//
// See README.md in this directory for how to run the suite.
package integration
