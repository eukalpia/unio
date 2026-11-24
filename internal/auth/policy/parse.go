package policy

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
)

// Limits on a policy document. They bound parsing and evaluation cost.
const (
	// MaxDocumentSize is the largest policy document accepted, in bytes.
	MaxDocumentSize = 20 << 10
	// Version is the only accepted policy language version.
	Version = "2012-10-17"

	maxStatements  = 128
	maxListItems   = 128
	maxActionLen   = 128
	maxResourceLen = 1024
	maxSidLen      = 256
	// s3ResourcePrefix starts every S3 resource ARN the policy may name.
	s3ResourcePrefix = "arn:aws:s3:::"
)

// Keys that are valid in a document or statement object.
var (
	topKeys             = []string{"Version", "Statement", "Id"}
	statementKeys       = []string{"Sid", "Effect", "Action", "Resource"}
	bucketStatementKeys = []string{"Sid", "Effect", "Principal", "Action", "Resource"}
	// unsupportedKeys are valid IAM elements that this version refuses.
	unsupportedKeys = map[string]string{
		"Condition":   "Condition is not supported in this version",
		"NotAction":   "NotAction is not supported in this version",
		"NotResource": "NotResource is not supported in this version",
	}
)

// kind selects the rules of a policy document.
type kind struct {
	// bucket is empty for an identity policy. For a bucket policy it names
	// the bucket the policy is attached to.
	bucket string
}

// Parse parses and validates an identity policy document. It is strict: unknown
// elements, duplicate keys, wrong types and unsupported elements are all
// errors, and nothing is ignored. Parse returns an *Error.
func Parse(doc []byte) (*Policy, error) {
	return parse(doc, kind{})
}

// ParseBucket parses and validates the policy document of bucket. The rules
// are those of Parse with three differences:
//
//   - every statement must name a Principal, and the only principal accepted
//     is everyone: "*" or {"AWS": "*"}. A statement therefore applies to
//     every request, signed or anonymous;
//   - every Resource must lie inside bucket: arn:aws:s3:::bucket or a pattern
//     under arn:aws:s3:::bucket/. A bare "*" is refused, so a policy cannot
//     reach past the bucket it is stored on;
//   - every Action must be an S3 action ("s3:..." or "*", which then means
//     s3:*), so a bucket policy can never grant unio:Admin.
//
// ParseBucket returns an *Error.
func ParseBucket(doc []byte, bucket string) (*Policy, error) {
	if bucket == "" {
		return nil, invalid("bucket name is required")
	}
	return parse(doc, kind{bucket: bucket})
}

func parse(doc []byte, k kind) (*Policy, error) {
	if len(doc) > MaxDocumentSize {
		return nil, invalid("policy document exceeds %d bytes", MaxDocumentSize)
	}
	if !utf8.Valid(doc) || !sonic.Valid(doc) {
		return nil, invalid("policy document is not valid JSON")
	}
	var root ast.Node
	if err := root.UnmarshalJSON(doc); err != nil {
		return nil, invalid("policy document is not valid JSON")
	}
	top, err := members(&root, "document", topKeys, false)
	if err != nil {
		return nil, err
	}
	if err := checkVersion(top); err != nil {
		return nil, err
	}
	if id, ok := top["Id"]; ok {
		if _, err := stringValue(id, "Id"); err != nil {
			return nil, err
		}
	}
	stmts, err := statementList(top, k)
	if err != nil {
		return nil, err
	}
	return &Policy{statements: stmts}, nil
}

// checkVersion requires Version to be present and equal to Version.
func checkVersion(top map[string]ast.Node) error {
	v, ok := top["Version"]
	if !ok {
		return invalid("Version is required")
	}
	s, err := stringValue(v, "Version")
	if err != nil {
		return err
	}
	if s != Version {
		return invalid("unsupported Version, expected %s", Version)
	}
	return nil
}

// statementList parses the Statement array into compiled statements.
func statementList(top map[string]ast.Node, k kind) ([]statement, error) {
	sv, ok := top["Statement"]
	if !ok {
		return nil, invalid("Statement is required")
	}
	if sv.TypeSafe() != ast.V_ARRAY {
		return nil, invalid("Statement must be an array")
	}
	it, err := sv.Values()
	if err != nil {
		return nil, invalid("Statement must be an array")
	}
	var out []statement
	var item ast.Node
	for it.Next(&item) {
		if len(out) == maxStatements {
			return nil, invalid("Statement has more than %d entries", maxStatements)
		}
		st, err := parseStatement(item, k)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if len(out) == 0 {
		return nil, invalid("Statement must not be empty")
	}
	return out, nil
}

// parseStatement validates one statement object.
func parseStatement(n ast.Node, k kind) (statement, error) {
	allowed := statementKeys
	if k.bucket != "" {
		allowed = bucketStatementKeys
	}
	f, err := members(&n, "statement", allowed, k.bucket != "")
	if err != nil {
		return statement{}, err
	}
	if k.bucket != "" {
		p, ok := f["Principal"]
		if !ok {
			return statement{}, invalid("statement Principal is required in a bucket policy")
		}
		if err := checkEveryonePrincipal(p); err != nil {
			return statement{}, err
		}
	}
	if sid, ok := f["Sid"]; ok {
		s, err := stringValue(sid, "Sid")
		if err != nil {
			return statement{}, err
		}
		if len(s) > maxSidLen {
			return statement{}, invalid("Sid is longer than %d bytes", maxSidLen)
		}
	}
	eff, ok := f["Effect"]
	if !ok {
		return statement{}, invalid("statement Effect is required")
	}
	effect, err := stringValue(eff, "Effect")
	if err != nil {
		return statement{}, err
	}
	var allow bool
	switch effect {
	case "Allow":
		allow = true
	case "Deny":
	default:
		return statement{}, invalid("Effect must be Allow or Deny")
	}
	checkAction, checkResource := validAction, validResource
	if k.bucket != "" {
		checkAction = validBucketAction
		checkResource = func(r string) error { return validBucketResource(k.bucket, r) }
	}
	actions, err := stringList(f, "Action", checkAction)
	if err != nil {
		return statement{}, err
	}
	if k.bucket != "" {
		// "*" in a bucket policy means every S3 action and nothing else.
		for i, a := range actions {
			if a == "*" {
				actions[i] = "s3:*"
			}
		}
	}
	resources, err := stringList(f, "Resource", checkResource)
	if err != nil {
		return statement{}, err
	}
	return statement{allow: allow, actions: actions, resources: resources}, nil
}

// members returns the members of an object node. Every key must be one of
// allowed, must not repeat, and must not be an unsupported element. ctx names
// the object for error messages.
func members(n *ast.Node, ctx string, allowed []string, principalOK bool) (map[string]ast.Node, error) {
	if n.TypeSafe() != ast.V_OBJECT {
		return nil, invalid("%s must be an object", ctx)
	}
	it, err := n.Properties()
	if err != nil {
		return nil, invalid("%s must be an object", ctx)
	}
	out := make(map[string]ast.Node)
	var p ast.Pair
	for it.Next(&p) {
		if msg, ok := unsupportedKeys[p.Key]; ok {
			return nil, invalid("%s", msg)
		}
		if p.Key == "Principal" && !principalOK {
			return nil, invalid("Principal is not allowed in an identity policy")
		}
		if !slices.Contains(allowed, p.Key) {
			return nil, invalid("unknown element in %s", ctx)
		}
		if _, dup := out[p.Key]; dup {
			return nil, invalid("duplicate element in %s", ctx)
		}
		out[p.Key] = p.Value
	}
	return out, nil
}

// stringValue returns the string held by a JSON string node.
func stringValue(n ast.Node, field string) (string, error) {
	if n.TypeSafe() != ast.V_STRING {
		return "", invalid("%s must be a string", field)
	}
	s, err := n.String()
	if err != nil {
		return "", invalid("%s must be a string", field)
	}
	return s, nil
}

// stringList reads a required field that is a string or a non-empty array
// of strings, validating each entry with check.
func stringList(f map[string]ast.Node, field string, check func(string) error) ([]string, error) {
	n, ok := f[field]
	if !ok {
		return nil, invalid("%s is required", field)
	}
	switch n.TypeSafe() {
	case ast.V_STRING:
		s, err := stringValue(n, field)
		if err != nil {
			return nil, err
		}
		if err := check(s); err != nil {
			return nil, err
		}
		return []string{s}, nil
	case ast.V_ARRAY:
	default:
		return nil, invalid("%s must be a string or an array of strings", field)
	}
	it, err := n.Values()
	if err != nil {
		return nil, invalid("%s must be an array", field)
	}
	var out []string
	var item ast.Node
	for it.Next(&item) {
		if len(out) == maxListItems {
			return nil, invalid("%s has more than %d entries", field, maxListItems)
		}
		s, err := stringValue(item, field)
		if err != nil {
			return nil, err
		}
		if err := check(s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, invalid("%s must not be empty", field)
	}
	return out, nil
}

// validAction accepts "*" or a "service:name" pattern built from letters,
// digits and the characters : * ? . _ -.
func validAction(a string) error {
	if a == "*" {
		return nil
	}
	if a == "" || len(a) > maxActionLen || !strings.Contains(a, ":") || !allBytes(a, isActionChar) {
		return invalid("malformed Action")
	}
	return nil
}

// validResource accepts "*" or an S3 resource ARN that starts with
// arn:aws:s3::: and contains no spaces or control characters.
func validResource(r string) error {
	if r == "*" {
		return nil
	}
	if len(r) > maxResourceLen || !strings.HasPrefix(r, s3ResourcePrefix) || !allBytes(r, isPrintableNoSpace) {
		return invalid("malformed Resource, expected an arn:aws:s3::: ARN")
	}
	return nil
}

// checkEveryonePrincipal accepts the principal that means everyone: "*",
// {"AWS": "*"} or {"AWS": ["*"]}. Named principals are refused rather than
// ignored, so a policy never grants more than it appears to.
func checkEveryonePrincipal(n ast.Node) error {
	const msg = `only Principal "*" (everyone) is supported in a bucket policy`
	switch n.TypeSafe() {
	case ast.V_STRING:
		s, err := stringValue(n, "Principal")
		if err != nil || s != "*" {
			return invalid(msg)
		}
		return nil
	case ast.V_OBJECT:
	default:
		return invalid(msg)
	}
	f, err := members(&n, "Principal", []string{"AWS"}, false)
	if err != nil {
		return invalid(msg)
	}
	aws, ok := f["AWS"]
	if !ok {
		return invalid(msg)
	}
	list, err := stringList(map[string]ast.Node{"AWS": aws}, "AWS", func(s string) error {
		if s != "*" {
			return invalid(msg)
		}
		return nil
	})
	if err != nil || len(list) == 0 {
		return invalid(msg)
	}
	return nil
}

// validBucketAction accepts "*" or an action pattern of the s3 service.
func validBucketAction(a string) error {
	if a == "*" {
		return nil
	}
	if err := validAction(a); err != nil {
		return err
	}
	if !strings.HasPrefix(strings.ToLower(a), "s3:") {
		return invalid("a bucket policy may name only s3: actions")
	}
	return nil
}

// validBucketResource accepts the bucket ARN and patterns under it.
func validBucketResource(bucket, r string) error {
	if err := validResource(r); err != nil {
		return err
	}
	arn := s3ResourcePrefix + bucket
	if r == arn || strings.HasPrefix(r, arn+"/") {
		return nil
	}
	return invalid("a bucket policy Resource must be %s or lie under %s/", arn, arn)
}

func isActionChar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	case c == ':', c == '*', c == '?', c == '.', c == '_', c == '-':
		return true
	}
	return false
}

func isPrintableNoSpace(c byte) bool {
	return 0x21 <= c && c <= 0x7e
}

// allBytes reports whether every byte of s satisfies ok.
func allBytes(s string, ok func(byte) bool) bool {
	for i := 0; i < len(s); i++ {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}
