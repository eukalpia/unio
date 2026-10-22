// Package policy parses and evaluates IAM identity policies for the S3 API.
//
// The supported subset is the one Unio enforces today: Version 2012-10-17,
// Statement entries with Effect Allow or Deny, Action and Resource given as
// a string or a list, and wildcards '*' and '?' in both. Condition,
// NotAction and NotResource are refused with MalformedPolicy rather than
// ignored, because ignoring a Condition would widen the access it guards.
//
// Evaluation follows the IAM rules: an explicit Deny in any matching
// statement wins, an Allow must match otherwise, and everything else is
// denied. EvaluateAll applies these rules to a set of documents, such as a
// user's inline policy together with its attached managed policies, as one
// union.
package policy
