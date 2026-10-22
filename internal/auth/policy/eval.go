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
// case-sensitively.
func (p *Policy) Evaluate(action auth.Action, resource string) Decision {
	if p == nil {
		return Implicit
	}
	a := string(action)
	allowed := false
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
