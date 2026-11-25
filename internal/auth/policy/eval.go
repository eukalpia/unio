package policy

import "github.com/eukalpia/unio/internal/auth"

// Decision is the outcome of evaluating a policy for one action on one
// resource.
type Decision uint8

// Evaluation outcomes.
const (
	// Implicit means no statement matched; the request is denied.
	Implicit Decision = iota
	// Allow means at least one Allow matched and no Deny did.
	Allow
	// Deny means a Deny statement matched. It wins over any Allow.
	Deny
)

// statement is a compiled Allow or Deny statement.
type statement struct {
	allow     bool
	actions   []string
	resources []string
}

// Policy is a validated identity policy. It is immutable after Parse and
// safe for concurrent use.
type Policy struct {
	statements []statement
}

// Evaluate decides whether the policy allows action on resource. The
// resource is an S3 ARN such as arn:aws:s3:::bucket/key, or "*" for
// service-level actions. Actions compare case-insensitively, resources
// case-sensitively. A nil policy yields Implicit.
func (p *Policy) Evaluate(action auth.Action, resource string) Decision {
	return EvaluateAll([]*Policy{p}, action, resource)
}

// EvaluateAll decides whether a set of policies allows action on resource.
// The set is what applies to one user: its inline policy and every managed
// policy attached to it. The rule is the union of their statements. An
// explicit Deny in any matching statement of any policy wins. Otherwise an
// Allow in any matching statement allows the request. Otherwise the result
// is Implicit, which callers must treat as a denial. Nil policies contribute
// no statements, and an empty set yields Implicit.
//
// The cost is linear in the total number of statements, and evaluation stops
// at the first matching Deny.
func EvaluateAll(policies []*Policy, action auth.Action, resource string) Decision {
	a := string(action)
	allowed := false
	for _, p := range policies {
		if p == nil {
			continue
		}
		for i := range p.statements {
			st := &p.statements[i]
			if !st.matches(a, resource) {
				continue
			}
			if !st.allow {
				return Deny
			}
			allowed = true
		}
	}
	if allowed {
		return Allow
	}
	return Implicit
}

// matches reports whether any action pattern and any resource pattern of the
// statement match the request.
func (st *statement) matches(action, resource string) bool {
	return anyMatch(st.actions, action, true) && anyMatch(st.resources, resource, false)
}

// anyMatch reports whether any pattern matches s.
func anyMatch(patterns []string, s string, fold bool) bool {
	for _, p := range patterns {
		if globMatch(p, s, fold) {
			return true
		}
	}
	return false
}
