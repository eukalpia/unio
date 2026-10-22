package policy

import (
	"errors"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestParseAccepts(t *testing.T) {
	cases := map[string]string{
		"minimal allow":           `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`,
		"lists and wildcards":     `{"Version":"2012-10-17","Statement":[{"Sid":"x","Effect":"Deny","Action":["s3:Put*","s3:Delete?bject"],"Resource":["arn:aws:s3:::b","arn:aws:s3:::b/*"]}]}`,
		"id and whitespace":       "{\n  \"Id\": \"p1\",\n  \"Version\": \"2012-10-17\",\n  \"Statement\": [\n    { \"Effect\": \"Allow\", \"Action\": \"s3:*\", \"Resource\": \"*\" }\n  ]\n}\n",
		"service level resource":  `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":"*"}]}`,
		"unicode sid":             `{"Version":"2012-10-17","Statement":[{"Sid":"héllo","Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`,
		"single-item action list": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["*"]}]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(doc)); err != nil {
				t.Fatalf("valid policy rejected: %v", err)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	const ok = `"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::b"`
	cases := []struct {
		name string
		doc  string
		want s3err.Code
	}{
		{"not json", `{`, s3err.MalformedPolicy},
		{"trailing garbage", `{"Version":"2012-10-17","Statement":[{` + ok + `}]} x`, s3err.MalformedPolicy},
		{"array at top", `[]`, s3err.MalformedPolicy},
		{"missing version", `{"Statement":[{` + ok + `}]}`, s3err.MalformedPolicy},
		{"wrong version", `{"Version":"2008-10-17","Statement":[{` + ok + `}]}`, s3err.MalformedPolicy},
		{"numeric version", `{"Version":2012,"Statement":[{` + ok + `}]}`, s3err.MalformedPolicy},
		{"unknown top-level key", `{"Version":"2012-10-17","Extra":1,"Statement":[{` + ok + `}]}`, s3err.MalformedPolicy},
		{"duplicate top-level key", `{"Version":"2012-10-17","Version":"2012-10-17","Statement":[{` + ok + `}]}`, s3err.MalformedPolicy},
		{"missing statement", `{"Version":"2012-10-17"}`, s3err.MalformedPolicy},
		{"empty statement", `{"Version":"2012-10-17","Statement":[]}`, s3err.MalformedPolicy},
		{"statement is object", `{"Version":"2012-10-17","Statement":{` + ok + `}}`, s3err.MalformedPolicy},
		{"statement entry not object", `{"Version":"2012-10-17","Statement":["x"]}`, s3err.MalformedPolicy},
		{"unknown statement key", `{"Version":"2012-10-17","Statement":[{` + ok + `,"Foo":1}]}`, s3err.MalformedPolicy},
		{"duplicate effect", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Effect":"Deny","Action":"s3:GetObject","Resource":"*"}]}`, s3err.MalformedPolicy},
		{"lower-case effect", `{"Version":"2012-10-17","Statement":[{"Effect":"allow","Action":"s3:GetObject","Resource":"*"}]}`, s3err.MalformedPolicy},
		{"missing effect", `{"Version":"2012-10-17","Statement":[{"Action":"s3:GetObject","Resource":"*"}]}`, s3err.MalformedPolicy},
		{"missing action", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Resource":"*"}]}`, s3err.MalformedPolicy},
		{"empty action list", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":[],"Resource":"*"}]}`, s3err.MalformedPolicy},
		{"non-string action", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":[1],"Resource":"*"}]}`, s3err.MalformedPolicy},
		{"action without service", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"GetObject","Resource":"*"}]}`, s3err.MalformedPolicy},
		{"action with space", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:Get Object","Resource":"*"}]}`, s3err.MalformedPolicy},
		{"resource outside s3", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:iam:::role/x"}]}`, s3err.MalformedPolicy},
		{"resource with space", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::b /k"}]}`, s3err.MalformedPolicy},
		{"missing resource", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject"}]}`, s3err.MalformedPolicy},
		{"principal", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*"}]}`, s3err.MalformedPolicy},
		{"condition refused", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*","Condition":{"IpAddress":{"aws:SourceIp":"10.0.0.0/8"}}}]}`, s3err.MalformedPolicy},
		{"not action refused", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","NotAction":"s3:DeleteObject","Resource":"*"}]}`, s3err.MalformedPolicy},
		{"not resource refused", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","NotResource":"*"}]}`, s3err.MalformedPolicy},
		{"sid too long", `{"Version":"2012-10-17","Statement":[{"Sid":"` + strings.Repeat("a", 300) + `",` + ok + `}]}`, s3err.MalformedPolicy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.doc))
			if err == nil {
				t.Fatal("invalid policy accepted")
			}
			if !s3err.Is(err, c.want) {
				t.Fatalf("err = %v, want code %s", err, c.want)
			}
			var pe *Error
			if !errors.As(err, &pe) {
				t.Fatalf("err %T is not a *policy.Error", err)
			}
		})
	}
}

func TestParseRejectsOversizedDocument(t *testing.T) {
	pad := strings.Repeat(" ", MaxDocumentSize)
	doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}` + pad
	if _, err := Parse([]byte(doc)); !s3err.Is(err, s3err.MalformedPolicy) {
		t.Fatalf("oversized document: err = %v", err)
	}
}

func TestParseTooManyStatements(t *testing.T) {
	stmt := `{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}`
	doc := `{"Version":"2012-10-17","Statement":[` + strings.Repeat(stmt+",", maxStatements) + stmt + `]}`
	if _, err := Parse([]byte(doc)); !s3err.Is(err, s3err.MalformedPolicy) {
		t.Fatalf("statement limit not enforced: %v", err)
	}
}

func TestEvaluate(t *testing.T) {
	const bucket = "arn:aws:s3:::photos"
	cases := []struct {
		name   string
		doc    string
		action auth.Action
		res    string
		want   Decision
	}{
		{
			"allow matching object",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::photos/*"}]}`,
			auth.ActionGetObject, bucket + "/2026/a.jpg", Allow,
		},
		{
			"default deny without a match",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::other/*"}]}`,
			auth.ActionGetObject, bucket + "/a.jpg", Implicit,
		},
		{
			"explicit deny beats allow in any order",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"},{"Effect":"Deny","Action":"s3:DeleteObject","Resource":"arn:aws:s3:::photos/*"}]}`,
			auth.ActionDeleteObject, bucket + "/a.jpg", Deny,
		},
		{
			"deny first also wins",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"s3:DeleteObject","Resource":"*"},{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`,
			auth.ActionDeleteObject, bucket + "/a.jpg", Deny,
		},
		{
			"action wildcard prefix",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:Get*","Resource":"*"}]}`,
			auth.ActionGetObject, bucket, Allow,
		},
		{
			"action wildcard does not cross service",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`,
			auth.ActionAdmin, "*", Implicit,
		},
		{
			"star action matches admin",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`,
			auth.ActionAdmin, "*", Allow,
		},
		{
			"action names are case-insensitive",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"S3:getobject","Resource":"*"}]}`,
			auth.ActionGetObject, bucket, Allow,
		},
		{
			"resources are case-sensitive",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::Photos/*"}]}`,
			auth.ActionGetObject, bucket + "/a.jpg", Implicit,
		},
		{
			"bucket ARN does not cover objects",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:ListBucket","Resource":"arn:aws:s3:::photos"}]}`,
			auth.ActionListBucket, bucket + "/a.jpg", Implicit,
		},
		{
			"question mark matches one byte",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::photos/a?.jpg"}]}`,
			auth.ActionGetObject, bucket + "/ab.jpg", Allow,
		},
		{
			"question mark does not match zero bytes",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::photos/a?.jpg"}]}`,
			auth.ActionGetObject, bucket + "/a.jpg", Implicit,
		},
		{
			"service-level action needs star resource",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:ListAllMyBuckets","Resource":"arn:aws:s3:::*"}]}`,
			auth.ActionListAllMyBuckets, "*", Implicit,
		},
		{
			"deny applies to listed action only",
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:PutObject"],"Resource":"*"},{"Effect":"Deny","Action":["s3:PutObject"],"Resource":"*"}]}`,
			auth.ActionGetObject, bucket + "/a", Allow,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Parse([]byte(c.doc))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := p.Evaluate(c.action, c.res); got != c.want {
				t.Fatalf("Evaluate(%s, %s) = %v, want %v", c.action, c.res, got, c.want)
			}
		})
	}
}

func TestEvaluateNilPolicyDenies(t *testing.T) {
	var p *Policy
	if got := p.Evaluate(auth.ActionGetObject, "*"); got != Implicit {
		t.Fatalf("nil policy = %v, want Implicit", got)
	}
}
