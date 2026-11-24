package policy

import (
	"strings"
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

// FuzzParseBucket checks the invariants that keep a bucket policy inside its
// bucket: whatever document is accepted, no statement may name a resource
// outside the bucket, an action outside s3:, or reach unio:Admin, and a
// request on another bucket is never allowed.
func FuzzParseBucket(f *testing.F) {
	f.Add([]byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/pub/*"}]}`))
	f.Add([]byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":"*","Resource":["arn:aws:s3:::b","arn:aws:s3:::b/*"]}]}`))
	f.Add([]byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Principal":"*","Action":"s3:*","Resource":"arn:aws:s3:::b/secret/*"}]}`))
	f.Add([]byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"unio:Admin","Resource":"*"}]}`))
	f.Fuzz(func(t *testing.T, doc []byte) {
		p, err := ParseBucket(doc, "b")
		if err != nil {
			return
		}
		for _, st := range p.statements {
			for _, r := range st.resources {
				if r != "arn:aws:s3:::b" && !strings.HasPrefix(r, "arn:aws:s3:::b/") {
					t.Fatalf("accepted resource %q outside the bucket: %q", r, doc)
				}
			}
			for _, a := range st.actions {
				if !strings.HasPrefix(strings.ToLower(a), "s3:") {
					t.Fatalf("accepted non-s3 action %q: %q", a, doc)
				}
			}
		}
		for _, res := range []string{"arn:aws:s3:::other/k", "arn:aws:s3:::bb/k", "arn:aws:s3:::other", "*"} {
			if p.Evaluate(auth.ActionGetObject, res) == Allow {
				t.Fatalf("allowed %s on %s: %q", auth.ActionGetObject, res, doc)
			}
		}
		if p.Evaluate(auth.ActionAdmin, "arn:aws:s3:::b") == Allow || p.Evaluate(auth.ActionAdmin, "*") == Allow {
			t.Fatalf("a bucket policy granted unio:Admin: %q", doc)
		}
	})
}
