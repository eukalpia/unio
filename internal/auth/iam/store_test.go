package iam

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/authtest"
	"github.com/eukalpia/unio/internal/auth/sigv4"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestValidateRootCredentials(t *testing.T) {
	cases := []struct {
		name   string
		ak, sk string
		ok     bool
	}{
		{"good", testRootKey, testRootSecret, true},
		{"access key too short", "SHORTKEY1234", testRootSecret, true},
		{"access key 11 chars", "ABCDEFGHIJK", testRootSecret, false},
		{"access key with slash", "ABCDEFGHIJK/L", testRootSecret, false},
		{"secret too short", testRootKey, "abcdefghijklmnopqrstuvwxyz01234", false},
		{"secret with space", testRootKey, "rootsecret-0123456789-abcdefghijk lmnop", false},
		{"secret repeated char", testRootKey, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
		{"secret too long", testRootKey, string(make([]byte, 300)), false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateRootCredentials(c.ak, c.sk)
			if (err == nil) != c.ok {
				t.Fatalf("ValidateRootCredentials = %v, want ok=%v", err, c.ok)
			}
			if err != nil && !s3err.Is(err, s3err.InvalidArgument) {
				t.Fatalf("code = %s, want InvalidArgument", s3err.CodeOf(err))
			}
		})
	}
}

func TestNewRejectsBadConfiguration(t *testing.T) {
	kv := authtest.NewMemKV()
	clock := newClock()
	cases := map[string]func(c *Config){
		"nil kek":          func(c *Config) { c.KEK = nil },
		"short kek":        func(c *Config) { c.KEK = c.KEK[:16] },
		"weak root key":    func(c *Config) { c.RootAccessKey = "short" },
		"weak root secret": func(c *Config) { c.RootSecret = "tooshort" },
		"no root":          func(c *Config) { c.RootAccessKey, c.RootSecret = "", "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(clock)
			mutate(&cfg)
			if _, err := New(kv, cfg); err == nil {
				t.Fatal("bad configuration accepted")
			}
		})
	}
	if _, err := New(nil, testConfig(clock)); err == nil {
		t.Fatal("nil SystemKV accepted")
	}
}

func TestDeriveKEK(t *testing.T) {
	a := DeriveKEK([]byte("cluster one"))
	if len(a) != 32 {
		t.Fatalf("KEK length = %d, want 32", len(a))
	}
	if !bytes.Equal(a, DeriveKEK([]byte("cluster one"))) {
		t.Fatal("KEK derivation is not deterministic")
	}
	if bytes.Equal(a, DeriveKEK([]byte("cluster two"))) {
		t.Fatal("different cluster secrets gave the same KEK")
	}
	if DeriveKEK(nil) != nil {
		t.Fatal("empty cluster secret must not derive a key")
	}
}

func TestCreateUserLifecycle(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	info, err := s.CreateUser(ctx, "alice")
	if err != nil || info.Name != "alice" || !info.Enabled {
		t.Fatalf("create: %+v, %v", info, err)
	}
	if _, err := s.CreateUser(ctx, "alice"); !errors.Is(err, ErrUserExists) {
		t.Fatalf("duplicate create: err = %v, want ErrUserExists", err)
	}
	mustCreateUser(t, s, "bob.smith@example")
	users, err := s.ListUsers(ctx)
	if err != nil || len(users) != 2 || users[0].Name != "alice" || users[1].Name != "bob.smith@example" {
		t.Fatalf("list = %+v, %v", users, err)
	}
	if err := s.DeleteUser(ctx, "nobody"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("delete missing: err = %v", err)
	}
}

func TestInvalidUserNamesRejected(t *testing.T) {
	s, _, _ := newTestStore(t)
	long := make([]byte, maxUserNameLen+1)
	for i := range long {
		long[i] = 'a'
	}
	for _, name := range []string{"", "a/b", "-x", "a b", "..", "a\x00b", ".hidden", string(long), "iam/users/x"} {
		if _, err := s.CreateUser(context.Background(), name); !errors.Is(err, ErrInvalidUserName) {
			t.Fatalf("name %q: err = %v, want ErrInvalidUserName", name, err)
		}
	}
}

func TestGeneratedCredentialShapes(t *testing.T) {
	seen := make(map[string]bool)
	for range 500 {
		id, err := newAccessKeyID()
		if err != nil || len(id) != accessKeyIDLen {
			t.Fatalf("access key id %q, %v", id, err)
		}
		for i := 0; i < len(id); i++ {
			if !(('A' <= id[i] && id[i] <= 'Z') || ('0' <= id[i] && id[i] <= '9')) {
				t.Fatalf("access key id %q has a character outside A-Z0-9", id)
			}
		}
		if seen[id] {
			t.Fatalf("duplicate access key id %q in 500 draws", id)
		}
		seen[id] = true
	}
	sec, err := newSecret()
	if err != nil || len(sec) != secretLen {
		t.Fatalf("secret %q, %v", sec, err)
	}
	for i := 0; i < len(sec); i++ {
		if !isAlnum(sec[i]) && sec[i] != '+' && sec[i] != '/' {
			t.Fatalf("secret has unexpected character %q", sec[i])
		}
	}
}

func TestSecretsAreSealedAtRest(t *testing.T) {
	s, kv, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	key := mustCreateKey(t, s, "alice")

	raw, _, err := kv.Get(ctx, keyPath(key.ID))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(key.Secret)) {
		t.Fatal("plaintext secret found in the stored record")
	}
	rec, err := decodeKeyRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := open(s.aead, []byte("OTHERKEY0000000000AA"), rec.Secret); err == nil {
		t.Fatal("sealed secret opened under another access key id")
	}
	plain, err := open(s.aead, []byte(key.ID), rec.Secret)
	if err != nil || string(plain) != key.Secret {
		t.Fatalf("sealed secret does not open with its own id: %v", err)
	}
}

func TestSealUsesFreshNonces(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, _ := seal(s.aead, []byte("id"), []byte("same"))
	b, _ := seal(s.aead, []byte("id"), []byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext are identical")
	}
}

func TestOpenRejectsTamperedAndShortValues(t *testing.T) {
	s, _, _ := newTestStore(t)
	sealed, _ := seal(s.aead, []byte("id"), []byte("secret-value"))
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1
	if _, err := open(s.aead, []byte("id"), tampered); err == nil {
		t.Fatal("tampered ciphertext opened")
	}
	if _, err := open(s.aead, []byte("id"), []byte{1, 2}); err == nil {
		t.Fatal("short value opened")
	}
}

func TestAuthenticateWithStoredKey(t *testing.T) {
	s, _, clock := newTestStore(t)
	mustCreateUser(t, s, "alice")
	key := mustCreateKey(t, s, "alice")

	res, err := s.Authenticate(context.Background(), signRequest(t, key.ID, key.Secret, clock.Now()))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if res.Identity.User != "alice" || res.Identity.AccessKey != key.ID || res.Identity.Root {
		t.Fatalf("identity = %+v", res.Identity)
	}

	wrong := signRequest(t, key.ID, key.Secret+"x", clock.Now())
	if _, err := s.Authenticate(context.Background(), wrong); !s3err.Is(err, s3err.SignatureDoesNotMatch) {
		t.Fatalf("wrong secret: err = %v, want SignatureDoesNotMatch", err)
	}
}

func TestRootCredentialsAuthenticate(t *testing.T) {
	s, _, clock := newTestStore(t)
	res, err := s.Authenticate(context.Background(), signRequest(t, testRootKey, testRootSecret, clock.Now()))
	if err != nil {
		t.Fatalf("root rejected: %v", err)
	}
	if !res.Identity.Root {
		t.Fatalf("identity = %+v, want root", res.Identity)
	}
	if err := s.Authorize(context.Background(), res.Identity, auth.ActionAdmin, "", ""); err != nil {
		t.Fatalf("root must bypass policies: %v", err)
	}
}

func TestDeletedKeyStopsAuthenticatingImmediately(t *testing.T) {
	s, _, clock := newTestStore(t)
	mustCreateUser(t, s, "alice")
	key := mustCreateKey(t, s, "alice")
	req := signRequest(t, key.ID, key.Secret, clock.Now())
	if _, err := s.Authenticate(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccessKey(context.Background(), key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(context.Background(), req); !s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatalf("after delete: err = %v, want InvalidAccessKeyId", err)
	}
	if err := s.DeleteAccessKey(context.Background(), key.ID); !errors.Is(err, ErrAccessKeyNotFound) {
		t.Fatalf("second delete: err = %v", err)
	}
}

func TestDisabledUserIsDenied(t *testing.T) {
	s, _, clock := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	key := mustCreateKey(t, s, "alice")
	if err := s.SetUserPolicy(ctx, "alice", []byte(readAllowPolicy)); err != nil {
		t.Fatal(err)
	}
	req := signRequest(t, key.ID, key.Secret, clock.Now())
	if _, err := s.Authenticate(ctx, req); err != nil {
		t.Fatal(err)
	}

	if err := s.SetUserEnabled(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, req); !s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatalf("disabled user authenticated: err = %v", err)
	}
	id := auth.Identity{AccessKey: key.ID, User: "alice"}
	if err := s.Authorize(ctx, id, auth.ActionGetObject, "photos", "a.jpg"); !s3err.Is(err, s3err.AccessDenied) {
		t.Fatalf("disabled user authorized: err = %v", err)
	}

	if err := s.SetUserEnabled(ctx, "alice", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, req); err != nil {
		t.Fatalf("re-enabled user rejected: %v", err)
	}
}

func TestAuthorizePolicyDecisions(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	if err := s.SetUserPolicy(ctx, "alice", []byte(readAllowPolicy)); err != nil {
		t.Fatal(err)
	}
	alice := auth.Identity{AccessKey: "AK", User: "alice"}
	cases := []struct {
		name   string
		id     auth.Identity
		action auth.Action
		bucket string
		key    string
		allow  bool
	}{
		{"get object in bucket", alice, auth.ActionGetObject, "photos", "a.jpg", true},
		{"list bucket", alice, auth.ActionListBucket, "photos", "", true},
		{"put denied by default", alice, auth.ActionPutObject, "photos", "a.jpg", false},
		{"delete denied by explicit deny", alice, auth.ActionDeleteObject, "photos", "a.jpg", false},
		{"other bucket denied", alice, auth.ActionGetObject, "private", "a.jpg", false},
		{"service action denied", alice, auth.ActionListAllMyBuckets, "", "", false},
		{"anonymous denied", auth.Identity{Anonymous: true}, auth.ActionGetObject, "photos", "a.jpg", false},
		{"empty identity denied", auth.Identity{}, auth.ActionGetObject, "photos", "a.jpg", false},
		{"unknown user denied", auth.Identity{User: "mallory"}, auth.ActionGetObject, "photos", "a.jpg", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := s.Authorize(ctx, c.id, c.action, c.bucket, c.key)
			if c.allow != (err == nil) {
				t.Fatalf("allow=%v, err = %v", c.allow, err)
			}
			if err != nil && !s3err.Is(err, s3err.AccessDenied) {
				t.Fatalf("code = %s, want AccessDenied", s3err.CodeOf(err))
			}
		})
	}
}

func TestPolicyLifecycle(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	alice := auth.Identity{AccessKey: "AK", User: "alice"}

	if err := s.SetUserPolicy(ctx, "alice", []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*","Condition":{}}]}`)); !s3err.Is(err, s3err.MalformedPolicy) {
		t.Fatalf("policy with Condition: err = %v, want MalformedPolicy", err)
	}
	if err := s.SetUserPolicy(ctx, "alice", []byte(`{not json`)); !s3err.Is(err, s3err.MalformedPolicy) {
		t.Fatalf("malformed policy: err = %v, want MalformedPolicy", err)
	}
	if err := s.SetUserPolicy(ctx, "nobody", []byte(readAllowPolicy)); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("policy for missing user: err = %v", err)
	}

	if err := s.SetUserPolicy(ctx, "alice", []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Authorize(ctx, alice, auth.ActionPutObject, "photos", "a.jpg"); err != nil {
		t.Fatalf("replaced policy not in effect: %v", err)
	}
	if err := s.SetUserPolicy(ctx, "alice", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Authorize(ctx, alice, auth.ActionPutObject, "photos", "a.jpg"); !s3err.Is(err, s3err.AccessDenied) {
		t.Fatalf("detached policy still grants access: %v", err)
	}
}

func TestDeleteUserRemovesKeysAndPolicy(t *testing.T) {
	s, kv, clock := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "bob")
	key := mustCreateKey(t, s, "bob")
	if err := s.SetUserPolicy(ctx, "bob", []byte(readAllowPolicy)); err != nil {
		t.Fatal(err)
	}
	req := signRequest(t, key.ID, key.Secret, clock.Now())
	if _, err := s.Authenticate(ctx, req); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteUser(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, req); !s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatalf("key of deleted user authenticated: %v", err)
	}
	if _, err := s.ListAccessKeys(ctx, "bob"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("list keys of deleted user: %v", err)
	}
	for _, prefix := range []string{keysPrefix, policiesPrefix, usersPrefix} {
		left, err := kv.List(ctx, prefix)
		if err != nil || len(left) != 0 {
			t.Fatalf("records left under %s: %+v, %v", prefix, left, err)
		}
	}
}

func TestRecreatedUserDoesNotInheritOldAccess(t *testing.T) {
	s, kv, clock := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "carol")
	oldKey := mustCreateKey(t, s, "carol")
	if err := s.SetUserPolicy(ctx, "carol", []byte(readAllowPolicy)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, "carol"); err != nil {
		t.Fatal(err)
	}

	// Simulate a write that raced with the delete: the old key and policy
	// reappear in storage after the user is recreated.
	mustCreateUser(t, s, "carol")
	if _, _, err := kv.Get(ctx, keyPath(oldKey.ID)); !errors.Is(err, meta.ErrNotFound) {
		t.Fatalf("key record survived the delete: %v", err)
	}
	staleKey := keyRecord{V: recordVersion, User: "carol", UserID: "stale-id", Created: clock.Now(), Secret: mustSeal(t, s, oldKey)}
	raw, _ := encodeRecord(staleKey)
	if _, err := kv.Put(ctx, keyPath(oldKey.ID), raw, meta.AnyRevision); err != nil {
		t.Fatal(err)
	}
	stalePolicy, _ := encodeRecord(policyRecord{V: recordVersion, UserID: "stale-id", Document: readAllowPolicy})
	if _, err := kv.Put(ctx, policyPath("carol"), stalePolicy, meta.AnyRevision); err != nil {
		t.Fatal(err)
	}
	s.clearCaches()

	req := signRequest(t, oldKey.ID, oldKey.Secret, clock.Now())
	if _, err := s.Authenticate(ctx, req); !s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatalf("stale key of previous incarnation authenticated: %v", err)
	}
	id := auth.Identity{User: "carol"}
	if err := s.Authorize(ctx, id, auth.ActionGetObject, "photos", "a.jpg"); !s3err.Is(err, s3err.AccessDenied) {
		t.Fatalf("stale policy of previous incarnation applied: %v", err)
	}
}

// mustSeal seals the secret of key for a stale record in a test.
func mustSeal(t testing.TB, s *Store, key auth.AccessKey) []byte {
	t.Helper()
	sealed, err := seal(s.aead, []byte(key.ID), []byte(key.Secret))
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestCacheStalenessIsBoundedByTTL(t *testing.T) {
	s, kv, clock := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "dave")
	key := mustCreateKey(t, s, "dave")
	req := signRequest(t, key.ID, key.Secret, clock.Now())
	if _, err := s.Authenticate(ctx, req); err != nil {
		t.Fatal(err)
	}

	// Another node disables the user. This node does not see the write
	// directly, so the cached entry stays valid until it expires.
	raw, rev, _ := kv.Get(ctx, userPath("dave"))
	rec, _ := decodeUserRecord(raw)
	rec.Enabled = false
	off, _ := encodeRecord(rec)
	if _, err := kv.Put(ctx, userPath("dave"), off, rev); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, req); err != nil {
		t.Fatalf("cached entry should still be served within the TTL: %v", err)
	}
	clock.Advance(11 * time.Second)
	if _, err := s.Authenticate(ctx, req); !s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatalf("after the TTL the disabled user must be refused: %v", err)
	}
}

func TestCASRetriesOnConcurrentWriter(t *testing.T) {
	clock := newClock()
	kv := &faultyKV{SystemKV: authtest.NewMemKV(), prefix: policiesPrefix, failPuts: 3}
	s, err := New(kv, testConfig(clock))
	if err != nil {
		t.Fatal(err)
	}
	mustCreateUser(t, s, "erin")
	if err := s.SetUserPolicy(context.Background(), "erin", []byte(readAllowPolicy)); err != nil {
		t.Fatalf("policy write should succeed after retries: %v", err)
	}
	if kv.failPuts != 0 {
		t.Fatalf("expected all injected conflicts to be consumed, %d left", kv.failPuts)
	}
}

func TestCASGivesUpAfterBoundedRetries(t *testing.T) {
	clock := newClock()
	cfg := testConfig(clock)
	cfg.MaxRetries = 4
	kv := &faultyKV{SystemKV: authtest.NewMemKV(), prefix: policiesPrefix, failPuts: 1000}
	s, err := New(kv, cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustCreateUser(t, s, "frank")
	err = s.SetUserPolicy(context.Background(), "frank", []byte(readAllowPolicy))
	if !errors.Is(err, ErrTooManyRetries) {
		t.Fatalf("err = %v, want ErrTooManyRetries", err)
	}
	if kv.failPuts != 1000-4 {
		t.Fatalf("attempts = %d, want %d", 1000-kv.failPuts, 4)
	}
	if s3err.CodeOf(err) != s3err.ServiceUnavailable {
		t.Fatalf("code = %s, want ServiceUnavailable so clients retry", s3err.CodeOf(err))
	}
}

func TestCreateAccessKeyRetriesIDCollisions(t *testing.T) {
	clock := newClock()
	kv := &faultyKV{SystemKV: authtest.NewMemKV(), prefix: keysPrefix, failPuts: 2}
	s, err := New(kv, testConfig(clock))
	if err != nil {
		t.Fatal(err)
	}
	mustCreateUser(t, s, "gina")
	key, err := s.CreateAccessKey(context.Background(), "gina")
	if err != nil {
		t.Fatalf("collisions should be retried: %v", err)
	}
	if key.ID == testRootKey || len(key.Secret) != secretLen {
		t.Fatalf("key = %+v", key)
	}
}

func TestListAccessKeysOmitsSecrets(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "hal")
	mustCreateUser(t, s, "ivy")
	k1 := mustCreateKey(t, s, "hal")
	k2 := mustCreateKey(t, s, "hal")
	mustCreateKey(t, s, "ivy")
	keys, err := s.ListAccessKeys(ctx, "hal")
	if err != nil || len(keys) != 2 {
		t.Fatalf("list = %+v, %v", keys, err)
	}
	for _, k := range keys {
		if k.Secret != "" || k.User != "hal" || (k.ID != k1.ID && k.ID != k2.ID) {
			t.Fatalf("listed key = %+v", k)
		}
	}
}

func TestTamperedSecretIsAnInternalFailure(t *testing.T) {
	s, kv, clock := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "jack")
	key := mustCreateKey(t, s, "jack")
	raw, rev, _ := kv.Get(ctx, keyPath(key.ID))
	rec, _ := decodeKeyRecord(raw)
	rec.Secret[len(rec.Secret)-1] ^= 0x01
	bad, _ := encodeRecord(rec)
	if _, err := kv.Put(ctx, keyPath(key.ID), bad, rev); err != nil {
		t.Fatal(err)
	}
	s.clearCaches()
	_, err := s.Authenticate(ctx, signRequest(t, key.ID, key.Secret, clock.Now()))
	if err == nil || s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatalf("tampered secret: err = %v, want an internal failure", err)
	}
}

func TestWrongKEKCannotReadSecrets(t *testing.T) {
	s, kv, clock := newTestStore(t)
	mustCreateUser(t, s, "kate")
	key := mustCreateKey(t, s, "kate")
	cfg := testConfig(clock)
	cfg.KEK = DeriveKEK([]byte("a different cluster"))
	other, err := New(kv, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = other.Authenticate(context.Background(), signRequest(t, key.ID, key.Secret, clock.Now()))
	if err == nil {
		t.Fatal("secret opened with the wrong key-encryption key")
	}
	if errors.Is(err, sigv4.ErrUnknownAccessKey) {
		t.Fatal("decryption failure must not look like an unknown key")
	}
}

func TestUnsupportedRecordVersionIsRejected(t *testing.T) {
	s, kv, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "liam")
	raw, _, err := kv.Get(ctx, userPath("liam"))
	if err != nil {
		t.Fatal(err)
	}
	future := bytes.Replace(raw, []byte(`"v":1`), []byte(`"v":2`), 1)
	if bytes.Equal(future, raw) {
		t.Fatalf("test record has no version field to change: %s", raw)
	}
	if _, err := decodeUserRecord(future); !errors.Is(err, errRecordVersion) {
		t.Fatalf("version 2 record: err = %v, want errRecordVersion", err)
	}
}

func TestUnknownFieldsInRecordsAreRejected(t *testing.T) {
	if _, err := decodeUserRecord([]byte(`{"v":1,"id":"x","name":"n","enabled":true,"extra":1}`)); err == nil {
		t.Fatal("record with an unknown field accepted")
	}
}

func TestDeleteUserRepeatCleansLeftovers(t *testing.T) {
	s, kv, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "ned")
	// A key record left behind by an interrupted delete.
	orphan, _ := encodeRecord(keyRecord{V: recordVersion, User: "ned", UserID: "gone", Secret: []byte("x")})
	if _, err := kv.Put(ctx, keyPath("ORPHANKEY0000000000A"), orphan, meta.AnyRevision); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, "ned"); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := s.DeleteUser(ctx, "ned"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("repeat delete: err = %v, want ErrUserNotFound", err)
	}
	if left, _ := kv.List(ctx, keysPrefix); len(left) != 0 {
		t.Fatalf("leftover key records after repeat delete: %d", len(left))
	}
}

func TestManagementErrorCodes(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "olga")
	cases := []struct {
		name string
		err  error
		want s3err.Code
	}{
		{"duplicate user", func() error { _, err := s.CreateUser(ctx, "olga"); return err }(), s3err.EntityAlreadyExists},
		{"delete missing user", s.DeleteUser(ctx, "nobody"), s3err.NoSuchEntity},
		{"keys of missing user", func() error { _, err := s.ListAccessKeys(ctx, "nobody"); return err }(), s3err.NoSuchEntity},
		{"delete missing key", s.DeleteAccessKey(ctx, "MISSINGKEY0000000000A"), s3err.NoSuchEntity},
		{"disable missing user", s.SetUserEnabled(ctx, "nobody", false), s3err.NoSuchEntity},
		{"invalid name", func() error { _, err := s.CreateUser(ctx, "a/b"); return err }(), s3err.InvalidArgument},
	}
	for _, c := range cases {
		if got := s3err.CodeOf(c.err); got != c.want {
			t.Fatalf("%s: code = %s, want %s (err %v)", c.name, got, c.want, c.err)
		}
	}
}

// Compile-time check that the store satisfies the contract interface.
var _ auth.IAM = (*Store)(nil)
