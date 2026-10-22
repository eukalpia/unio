package policy

import (
	"testing"

	"github.com/eukalpia/unio/internal/auth"
)

// FuzzParse feeds arbitrary bytes to the parser. It must never panic, and
// any accepted policy must evaluate without panicking.
func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`))
	f.Add([]byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":["s3:*"],"Resource":"*"}]}`))
	f.Add([]byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*","Condition":{}}]}`))
	f.Add([]byte(`{"Statement":"x"}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, doc []byte) {
		p, err := Parse(doc)
		if err != nil {
			return
		}
		if len(p.statements) == 0 {
			t.Fatalf("accepted a policy with no statements: %q", doc)
		}
		for _, st := range p.statements {
			if len(st.actions) == 0 || len(st.resources) == 0 {
				t.Fatalf("accepted a statement without actions or resources: %q", doc)
			}
		}
		_ = p.Evaluate(auth.ActionGetObject, "arn:aws:s3:::b/k")
	})
}

// FuzzGlobMatch differentially checks the iterative matcher against the
// quadratic reference, with and without case folding.
func FuzzGlobMatch(f *testing.F) {
	f.Add("s3:Get*", "s3:GetObject", true)
	f.Add("a*b?c", "aXXbyc", false)
	f.Add("*", "", false)
	f.Add("?*?", "a", false)
	f.Fuzz(func(t *testing.T, pat, s string, fold bool) {
		if len(pat) > 128 || len(s) > 128 {
			return
		}
		if got, want := globMatch(pat, s, fold), refGlob(pat, s, fold); got != want {
			t.Fatalf("globMatch(%q, %q, %v) = %v, reference %v", pat, s, fold, got, want)
		}
	})
}
