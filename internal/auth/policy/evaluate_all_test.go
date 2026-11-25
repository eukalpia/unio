package policy

import (
	"testing"

	"github.com/eukalpia/unio/internal/auth"
)

// mustParse parses a document that the test expects to be valid.
func mustParse(t *testing.T, doc string) *Policy {
	t.Helper()
	p, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse %s: %v", doc, err)
	}
	return p
}

// TestEvaluateAll covers the union rule over several documents: an inline and
// managed policies together, each of which may allow or deny.
func TestEvaluateAll(t *testing.T) {
	allowRead := mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],"Resource":["arn:aws:s3:::photos","arn:aws:s3:::photos/*"]}]}`)
	allowAll := mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)
	denyDelete := mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"s3:DeleteObject","Resource":"*"}]}`)
	allowList := mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:ListBucket","Resource":"*"}]}`)
	allowPut := mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:PutObject","Resource":"*"}]}`)
	allowOtherBucket := mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::other/*"}]}`)
	denyPhotos := mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"s3:GetObject","Resource":"arn:aws:s3:::photos/*"}]}`)

	const photo = "arn:aws:s3:::photos/a.jpg"
	cases := []struct {
		name     string
		policies []*Policy
		action   auth.Action
		resource string
		want     Decision
	}{
		{"allow in one document", []*Policy{allowList, allowRead}, auth.ActionGetObject, photo, Allow},
		{"allow in the second of two documents", []*Policy{allowPut, allowRead}, auth.ActionGetObject, photo, Allow},
		{"deny in one document overrides allow in another", []*Policy{allowAll, denyDelete}, auth.ActionDeleteObject, photo, Deny},
		{"deny overrides allow in either order", []*Policy{denyDelete, allowAll}, auth.ActionDeleteObject, photo, Deny},
		{"other actions still allowed beside a deny", []*Policy{allowAll, denyDelete}, auth.ActionGetObject, photo, Allow},
		{"inline deny overrides managed allow", []*Policy{denyPhotos, allowRead}, auth.ActionGetObject, photo, Deny},
		{"managed deny overrides inline allow", []*Policy{allowRead, denyPhotos}, auth.ActionGetObject, photo, Deny},
		{"no matching statement is implicit", []*Policy{allowList, allowPut}, auth.ActionGetObject, photo, Implicit},
		{"allow for another resource does not apply", []*Policy{allowOtherBucket}, auth.ActionGetObject, photo, Implicit},
		{"empty set is implicit", nil, auth.ActionGetObject, photo, Implicit},
		{"nil documents contribute nothing", []*Policy{nil, allowRead, nil}, auth.ActionGetObject, photo, Allow},
		{"only nil documents is implicit", []*Policy{nil}, auth.ActionGetObject, photo, Implicit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EvaluateAll(c.policies, c.action, c.resource); got != c.want {
				t.Fatalf("EvaluateAll = %v, want %v", got, c.want)
			}
		})
	}
}

// TestEvaluateAllIsOrderIndependent checks that the union does not depend on
// the order of the documents, for every ordering of a small set.
func TestEvaluateAllIsOrderIndependent(t *testing.T) {
	docs := []*Policy{
		mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`),
		mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"s3:Put*","Resource":"*"}]}`),
		mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:PutObject","Resource":"*"}]}`),
	}
	orders := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, action := range []auth.Action{auth.ActionPutObject, auth.ActionGetObject} {
		want := EvaluateAll(docs, action, "arn:aws:s3:::b/k")
		for _, order := range orders {
			set := []*Policy{docs[order[0]], docs[order[1]], docs[order[2]]}
			if got := EvaluateAll(set, action, "arn:aws:s3:::b/k"); got != want {
				t.Fatalf("order %v: %v, want %v", order, got, want)
			}
		}
	}
}
