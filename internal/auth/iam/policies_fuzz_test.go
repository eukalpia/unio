package iam

import (
	"strings"
	"testing"
)

// FuzzValidPolicyName checks that every accepted managed policy name is within
// the length and character rule, and stays inside its own key segment, so that
// no name can address another record.
func FuzzValidPolicyName(f *testing.F) {
	f.Add("ReadOnly-2026")
	f.Add("a.b@c_d=e+f,g")
	f.Add("../managed/x")
	f.Add("a/b")
	f.Add("")
	f.Add(strings.Repeat("z", maxPolicyNameLen+1))
	f.Fuzz(func(t *testing.T, name string) {
		if !validPolicyName(name) {
			return
		}
		if len(name) == 0 || len(name) > maxPolicyNameLen {
			t.Fatalf("accepted name of length %d", len(name))
		}
		for i := 0; i < len(name); i++ {
			if !isPolicyNameChar(name[i]) {
				t.Fatalf("accepted name %q with byte %q", name, name[i])
			}
		}
		if n := strings.Count(managedPath(name), "/"); n != 2 {
			t.Fatalf("accepted name %q yields key %q with %d separators", name, managedPath(name), n)
		}
	})
}
