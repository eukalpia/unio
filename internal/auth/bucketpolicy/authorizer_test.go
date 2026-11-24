package bucketpolicy

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/policy"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

type fakeIdentity map[string]policy.Decision // user -> decision for every request

func (f fakeIdentity) Decide(_ context.Context, id auth.Identity, _ auth.Action, _, _ string) (policy.Decision, error) {
	if id.Root {
		return policy.Allow, nil
	}
	if id.Anonymous {
		return policy.Implicit, nil
	}
	d, ok := f[id.User]
	if !ok {
		return policy.Deny, nil // unknown user
	}
	return d, nil
}

type fakeBuckets struct {
	policies map[string]string // bucket -> stored document; missing bucket = NoSuchBucket
	reads    atomic.Int64
}

func (f *fakeBuckets) GetBucketInfo(_ context.Context, bucket string) (object.BucketInfo, error) {
	f.reads.Add(1)
	doc, ok := f.policies[bucket]
	if !ok {
		return object.BucketInfo{}, s3err.Errorf(s3err.NoSuchBucket, "no bucket")
	}
	info := object.BucketInfo{Name: bucket}
	if doc != "" {
		info.Policy = []byte(doc)
	}
	return info, nil
}

const (
	publicRead = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/public/*"}]}`
	denySecret = `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Principal":"*","Action":"s3:*","Resource":"arn:aws:s3:::locked/secret/*"}]}`
)

func newTest() (*Authorizer, *fakeBuckets) {
	b := &fakeBuckets{policies: map[string]string{"pub": publicRead, "locked": denySecret, "plain": "", "broken": `{"not":"a policy"}`}}
	id := fakeIdentity{"alice": policy.Allow, "bob": policy.Implicit, "eve": policy.Deny}
	return New(id, b, time.Hour, nil), b
}

func TestAuthorizeRule(t *testing.T) {
	a, _ := newTest()
	anon := auth.Identity{Anonymous: true}
	user := func(u string) auth.Identity { return auth.Identity{User: u} }
	cases := []struct {
		name        string
		id          auth.Identity
		action      auth.Action
		bucket, key string
		allow       bool
	}{
		{"root anywhere", auth.Identity{Root: true}, auth.ActionDeleteObject, "locked", "secret/x", true},
		{"anonymous public prefix", anon, auth.ActionGetObject, "pub", "public/a.png", true},
		{"anonymous outside prefix", anon, auth.ActionGetObject, "pub", "private/a.png", false},
		{"anonymous other action", anon, auth.ActionPutObject, "pub", "public/a.png", false},
		{"anonymous list", anon, auth.ActionListBucket, "pub", "", false},
		{"anonymous no policy", anon, auth.ActionGetObject, "plain", "x", false},
		{"anonymous missing bucket", anon, auth.ActionGetObject, "nope", "x", false},
		{"anonymous broken policy", anon, auth.ActionGetObject, "broken", "x", false},
		{"anonymous service level", anon, auth.ActionListAllMyBuckets, "", "", false},
		{"user allowed by identity", user("alice"), auth.ActionPutObject, "plain", "x", true},
		{"user allowed by bucket", user("bob"), auth.ActionGetObject, "pub", "public/a", true},
		{"user implicit everywhere", user("bob"), auth.ActionGetObject, "plain", "x", false},
		{"bucket deny beats identity allow", user("alice"), auth.ActionGetObject, "locked", "secret/x", false},
		{"identity deny beats bucket allow", user("eve"), auth.ActionGetObject, "pub", "public/a", false},
		{"unknown user despite bucket allow", user("mallory"), auth.ActionGetObject, "pub", "public/a", false},
		{"bucket deny scoped to prefix", user("alice"), auth.ActionGetObject, "locked", "open/x", true},
	}
	for _, c := range cases {
		err := a.Authorize(t.Context(), c.id, c.action, c.bucket, c.key)
		if (err == nil) != c.allow {
			t.Errorf("%s: err=%v, want allow=%v", c.name, err, c.allow)
		}
		if err != nil && s3err.CodeOf(err) != s3err.AccessDenied {
			t.Errorf("%s: code %s, want AccessDenied", c.name, s3err.CodeOf(err))
		}
	}
}

func TestPolicyIsCachedAndInvalidated(t *testing.T) {
	a, b := newTest()
	anon := auth.Identity{Anonymous: true}
	for range 5 {
		_ = a.Authorize(t.Context(), anon, auth.ActionGetObject, "pub", "public/a")
	}
	if n := b.reads.Load(); n != 1 {
		t.Fatalf("%d metadata reads for one bucket within the TTL, want 1", n)
	}
	// Revoke the public read on "another node": the cache still answers.
	b.policies["pub"] = ""
	if err := a.Authorize(t.Context(), anon, auth.ActionGetObject, "pub", "public/a"); err != nil {
		t.Fatal("cached policy should still apply before the TTL")
	}
	a.Invalidate("pub")
	if err := a.Authorize(t.Context(), anon, auth.ActionGetObject, "pub", "public/a"); err == nil {
		t.Fatal("revoked policy still allowed after Invalidate")
	}
}

func TestPolicyExpires(t *testing.T) {
	a, b := newTest()
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	a.ttl = time.Second
	anon := auth.Identity{Anonymous: true}
	if err := a.Authorize(t.Context(), anon, auth.ActionGetObject, "pub", "public/a"); err != nil {
		t.Fatal(err)
	}
	b.policies["pub"] = ""
	now = now.Add(1100 * time.Millisecond)
	if err := a.Authorize(t.Context(), anon, auth.ActionGetObject, "pub", "public/a"); err == nil {
		t.Fatal("revocation not seen after the TTL")
	}
}
