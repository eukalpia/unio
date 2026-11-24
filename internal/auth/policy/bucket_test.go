package policy

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
)

// lmsPublic is the policy the LMS puts on its admission bucket: a few
// public prefixes, everything else private.
const lmsPublic = `{"Version":"2012-10-17","Statement":[{"Sid":"PublicByDesign","Effect":"Allow",
"Principal":{"AWS":["*"]},"Action":["s3:GetObject"],
"Resource":["arn:aws:s3:::ttpu-admission/faculties/*","arn:aws:s3:::ttpu-admission/users/*/photo.*"]}]}`

func TestParseBucketAcceptsThePublicPrefixPolicy(t *testing.T) {
	p, err := ParseBucket([]byte(lmsPublic), "ttpu-admission")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		action auth.Action
		res    string
		want   Decision
	}{
		{auth.ActionGetObject, "arn:aws:s3:::ttpu-admission/faculties/cs/logo.png", Allow},
		{auth.ActionGetObject, "arn:aws:s3:::ttpu-admission/users/42/photo.jpg", Allow},
		{auth.ActionGetObject, "arn:aws:s3:::ttpu-admission/users/42/passport.jpg", Implicit},
		{auth.ActionGetObject, "arn:aws:s3:::ttpu-admission/faculties", Implicit},
		{auth.ActionPutObject, "arn:aws:s3:::ttpu-admission/faculties/x", Implicit},
		{auth.ActionListBucket, "arn:aws:s3:::ttpu-admission", Implicit},
	}
	for _, c := range cases {
		if got := p.Evaluate(c.action, c.res); got != c.want {
			t.Errorf("%s %s: got %d, want %d", c.action, c.res, got, c.want)
		}
	}
}

func TestParseBucketPrincipalForms(t *testing.T) {
	for _, principal := range []string{`"*"`, `{"AWS":"*"}`, `{"AWS":["*"]}`} {
		doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":` + principal +
			`,"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`
		if _, err := ParseBucket([]byte(doc), "b"); err != nil {
			t.Errorf("principal %s refused: %v", principal, err)
		}
	}
}

func TestParseBucketRefusesWhatItCannotHonour(t *testing.T) {
	stmt := func(fields string) string {
		return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` + fields + `}]}`
	}
	cases := map[string]string{
		"no principal":         stmt(`"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"`),
		"named principal":      stmt(`"Principal":{"AWS":"arn:aws:iam::1:user/x"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"`),
		"principal mixed list": stmt(`"Principal":{"AWS":["*","x"]},"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"`),
		"service principal":    stmt(`"Principal":{"Service":"*"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"`),
		"bare star resource":   stmt(`"Principal":"*","Action":"s3:GetObject","Resource":"*"`),
		"other bucket":         stmt(`"Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::other/*"`),
		"bucket name prefix":   stmt(`"Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b*"`),
		"wildcard bucket":      stmt(`"Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::*/x"`),
		"admin action":         stmt(`"Principal":"*","Action":"unio:Admin","Resource":"arn:aws:s3:::b"`),
		"condition":            stmt(`"Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*","Condition":{}`),
		"not principal":        stmt(`"NotPrincipal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"`),
	}
	for name, doc := range cases {
		if _, err := ParseBucket([]byte(doc), "b"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// An identity policy still refuses Principal.
	if _, err := Parse([]byte(stmt(`"Principal":"*","Action":"s3:GetObject","Resource":"*"`))); err == nil {
		t.Error("identity policy accepted a Principal")
	}
}

func TestParseBucketStarActionMeansS3Only(t *testing.T) {
	doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"*","Resource":"arn:aws:s3:::b/*"}]}`
	p, err := ParseBucket([]byte(doc), "b")
	if err != nil {
		t.Fatal(err)
	}
	if p.Evaluate(auth.ActionGetObject, "arn:aws:s3:::b/k") != Allow {
		t.Error("s3 action not allowed by *")
	}
	if p.Evaluate(auth.ActionAdmin, "arn:aws:s3:::b/k") != Implicit {
		t.Error("* in a bucket policy reached unio:Admin")
	}
	if !strings.Contains(string(auth.ActionAdmin), "unio:") {
		t.Fatal("test assumption broken")
	}
}

func TestParseBucketDenyWins(t *testing.T) {
	doc := `{"Version":"2012-10-17","Statement":[
{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"},
{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/secret/*"}]}`
	p, err := ParseBucket([]byte(doc), "b")
	if err != nil {
		t.Fatal(err)
	}
	if p.Evaluate(auth.ActionGetObject, "arn:aws:s3:::b/secret/x") != Deny {
		t.Error("deny did not win")
	}
}
