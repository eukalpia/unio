package policy

import (
	"encoding/json"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
)

// Vocabularies for generated documents. Most patterns come from these lists,
// so that matches are frequent. Others are built from random bytes, so that
// unusual patterns are covered too.
var (
	genActions   = []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket", "s3:*", "s3:Get*", "*", "s3:?etObject", "S3:getobject", "s3:Delete*", "s3:List*"}
	genResources = []string{"arn:aws:s3:::b", "arn:aws:s3:::b/*", "arn:aws:s3:::b/a?.jpg", "arn:aws:s3:::B/*", "*", "arn:aws:s3:::*", "arn:aws:s3:::b/a/*"}
	reqActions   = []auth.Action{auth.ActionGetObject, auth.ActionPutObject, auth.ActionDeleteObject, auth.ActionListBucket, auth.ActionGetBucketLocation, auth.ActionAdmin}
	reqResources = []string{"arn:aws:s3:::b", "arn:aws:s3:::b/a.jpg", "arn:aws:s3:::b/a/x.jpg", "arn:aws:s3:::B/a.jpg", "*", "arn:aws:s3:::b/ab.jpg"}
)

// Characters that random patterns may use. Actions must contain ':' and use
// only action characters; resources must stay printable and contain no space.
const (
	randomActionChars   = "ab*?.:"
	randomResourceChars = "ab/*?."
)

// genStatement is a statement made from the fuzz input. The reference
// evaluator works on these values, not on the parsed policy, so a parser bug
// cannot hide from it.
type genStatement struct {
	allow     bool
	actions   []string
	resources []string
}

// cursor turns fuzz input into choices. Past the end it yields zeros, so every
// input describes some valid set of documents.
type cursor struct {
	data []byte
	pos  int
}

// next returns the next byte of the input, or 0 past its end.
func (c *cursor) next() int {
	c.pos++
	if c.pos > len(c.data) {
		return 0
	}
	return int(c.data[c.pos-1])
}

// pick returns a choice in [0, n).
func (c *cursor) pick(n int) int {
	return c.next() % n
}

// pattern returns a pattern from vocab, or a random one that starts with
// prefix and continues with bytes from chars.
func (c *cursor) pattern(vocab []string, prefix, chars string) string {
	if c.pick(4) != 0 {
		return vocab[c.pick(len(vocab))]
	}
	b := []byte(prefix)
	for range c.pick(6) {
		b = append(b, chars[c.pick(len(chars))])
	}
	return string(b)
}

// jsonStatement and jsonDocument are the wire form of a generated document.
// Action and Resource are strings or arrays of strings.
type jsonStatement struct {
	Effect   string `json:"Effect"`
	Action   any    `json:"Action"`
	Resource any    `json:"Resource"`
}

type jsonDocument struct {
	Version   string          `json:"Version"`
	Statement []jsonStatement `json:"Statement"`
}

// jsonList encodes values as a string when one value is given and asArray is
// not set, and as an array otherwise.
func jsonList(values []string, asArray bool) any {
	if len(values) == 1 && !asArray {
		return values[0]
	}
	return values
}

// FuzzEvaluateAll generates several policy documents from the fuzz input,
// parses them, and checks EvaluateAll against a reference evaluator for every
// request in a fixed set and for one request taken from the input. It also
// checks that the result does not depend on the order of the documents, and
// that a nil document changes nothing. Nothing may panic.
func FuzzEvaluateAll(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{3, 2, 1, 0, 7, 5, 9, 2, 255, 128, 64, 32, 16, 8, 4, 2, 1})
	f.Add([]byte("the quick brown fox jumps over the lazy dog 0123456789"))
	f.Fuzz(func(t *testing.T, data []byte) {
		c := &cursor{data: data}
		var docs []*Policy
		var ref [][]genStatement
		for range 1 + c.pick(4) {
			var stmts []genStatement
			var wire []jsonStatement
			for range 1 + c.pick(4) {
				st := genStatement{allow: c.pick(2) == 0}
				effect := "Deny"
				if st.allow {
					effect = "Allow"
				}
				for range 1 + c.pick(2) {
					st.actions = append(st.actions, c.pattern(genActions, "s3:", randomActionChars))
				}
				for range 1 + c.pick(2) {
					st.resources = append(st.resources, c.pattern(genResources, "arn:aws:s3:::", randomResourceChars))
				}
				stmts = append(stmts, st)
				wire = append(wire, jsonStatement{
					Effect:   effect,
					Action:   jsonList(st.actions, c.pick(2) == 0),
					Resource: jsonList(st.resources, c.pick(2) == 0),
				})
			}
			raw, err := json.Marshal(jsonDocument{Version: Version, Statement: wire})
			if err != nil {
				t.Fatalf("encode generated document: %v", err)
			}
			p, err := Parse(raw)
			if err != nil {
				t.Fatalf("generated document rejected: %v: %s", err, raw)
			}
			docs = append(docs, p)
			ref = append(ref, stmts)
		}

		requests := make([]struct {
			action   auth.Action
			resource string
		}, 0, len(reqActions)*len(reqResources)+1)
		for _, a := range reqActions {
			for _, r := range reqResources {
				requests = append(requests, struct {
					action   auth.Action
					resource string
				}{a, r})
			}
		}
		tail := data[min(c.pos, len(data)):]
		if len(tail) > 64 {
			tail = tail[:64]
		}
		requests = append(requests, struct {
			action   auth.Action
			resource string
		}{auth.Action(tail), string(tail)})

		reversed := make([]*Policy, len(docs))
		for i, p := range docs {
			reversed[len(docs)-1-i] = p
		}
		withNil := append([]*Policy{nil}, docs...)

		for _, req := range requests {
			want := refEvaluate(ref, string(req.action), req.resource)
			if got := EvaluateAll(docs, req.action, req.resource); got != want {
				t.Fatalf("EvaluateAll(%q, %q) = %v, reference %v; documents %d", req.action, req.resource, got, want, len(docs))
			}
			if got := EvaluateAll(reversed, req.action, req.resource); got != want {
				t.Fatalf("reversed order (%q, %q) = %v, reference %v", req.action, req.resource, got, want)
			}
			if got := EvaluateAll(withNil, req.action, req.resource); got != want {
				t.Fatalf("with a nil document (%q, %q) = %v, reference %v", req.action, req.resource, got, want)
			}
		}
	})
}

// refEvaluate is the reference for EvaluateAll. It scans every statement with
// no early exit, records whether any allow and any deny matched, and matches
// with the quadratic refGlob. It is written to be obviously right, not fast.
func refEvaluate(docs [][]genStatement, action, resource string) Decision {
	var anyAllow, anyDeny bool
	for _, stmts := range docs {
		for _, st := range stmts {
			if !refAnyMatch(st.actions, action, true) || !refAnyMatch(st.resources, resource, false) {
				continue
			}
			if st.allow {
				anyAllow = true
			} else {
				anyDeny = true
			}
		}
	}
	switch {
	case anyDeny:
		return Deny
	case anyAllow:
		return Allow
	default:
		return Implicit
	}
}

// refAnyMatch reports whether any pattern in patterns matches s, using refGlob.
func refAnyMatch(patterns []string, s string, fold bool) bool {
	for _, p := range patterns {
		if refGlob(p, s, fold) {
			return true
		}
	}
	return false
}
