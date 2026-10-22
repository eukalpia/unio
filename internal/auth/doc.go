// Package auth defines authentication and authorization: identities, the
// Authenticator that verifies S3 signatures, the Authorizer that evaluates
// policies, and IAM user and key management.
//
// Implementations: auth/sigv4 (AWS Signature Version 4 in all variants),
// auth/policy (IAM policy evaluation), auth/iam (users and keys on
// meta.SystemKV). Package auth/authtest provides fakes for tests.
package auth
