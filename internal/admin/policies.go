package admin

// errNamedPoliciesUnsupported answers the named-policy endpoints. The IAM store
// keeps one policy document per user and no named policies, and it evaluates
// only that document. The catalogue of named policies and the attachment of
// several of them need IAM support first. Until then these endpoints answer
// 501 rather than store a second, unshared copy of the rights.
func errNamedPoliciesUnsupported() error {
	return errNotImplemented("Named policies are not supported by this node.")
}
