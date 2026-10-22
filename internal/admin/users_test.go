package admin

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

// usersPath is the collection of users.
const usersPath = BasePath + "/users"

func TestListUsers(t *testing.T) {
	f := newFixture(t)
	akey, asecret := f.createUser("alice", fullPolicy)
	f.createUser("bob", "")
	if err := f.iam.SetUserEnabled(t.Context(), "bob", false); err != nil {
		t.Fatal(err)
	}
	creds := credsOf(akey, asecret)

	resp := f.serve(f.sigv4(creds, http.MethodGet, usersPath, "", nil))
	expectStatus(t, resp, http.StatusOK)
	expectNoStore(t, resp)
	var out []struct {
		Name       string   `json:"name"`
		Enabled    bool     `json:"enabled"`
		Policies   []string `json:"policies"`
		AccessKeys []struct {
			ID      string `json:"id"`
			Created string `json:"created"`
		} `json:"accessKeys"`
	}
	mustUnmarshal(t, resp.Body, &out)
	if len(out) != 2 || out[0].Name != "alice" || out[1].Name != "bob" {
		t.Fatalf("users = %+v, want alice and bob sorted", out)
	}
	if !out[0].Enabled || out[1].Enabled {
		t.Errorf("enabled flags = %v, %v", out[0].Enabled, out[1].Enabled)
	}
	if out[0].Policies == nil || len(out[0].Policies) != 0 {
		t.Errorf("policies = %v, want an empty array", out[0].Policies)
	}
	if len(out[0].AccessKeys) != 1 || out[0].AccessKeys[0].ID != akey || out[0].AccessKeys[0].Created == "" {
		t.Errorf("alice's keys = %+v", out[0].AccessKeys)
	}
	if out[1].AccessKeys == nil {
		t.Error("a user without keys has null accessKeys, want []")
	}
	if strings.Contains(string(resp.Body), asecret) || strings.Contains(string(resp.Body), "secretKey") {
		t.Error("the listing carries a secret")
	}
	if strings.Contains(string(resp.Body), testRoot) {
		t.Error("the root account is listed as a user")
	}
}

func TestCreateUser(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("admin", fullPolicy)
	creds := credsOf(key, secret)

	resp := f.serve(f.sigv4(creds, http.MethodPost, usersPath, "", []byte(`{"name":"carol"}`)))
	expectStatus(t, resp, http.StatusCreated)
	if string(resp.Body) != `{"name":"carol"}` {
		t.Errorf("body = %s", resp.Body)
	}

	errs := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"duplicate", `{"name":"carol"}`, http.StatusConflict, "EntityAlreadyExists"},
		{"invalid name", `{"name":"bad name"}`, http.StatusBadRequest, "InvalidArgument"},
		{"empty name", `{"name":""}`, http.StatusBadRequest, "InvalidArgument"},
		{"starts with a dot", `{"name":".hidden"}`, http.StatusBadRequest, "InvalidArgument"},
		{"too long", `{"name":"` + longString(65) + `"}`, http.StatusBadRequest, "InvalidArgument"},
		{"root account name", `{"name":"` + testRoot + `"}`, http.StatusConflict, "RootUserProtected"},
		{"unknown field", `{"name":"dave","admin":true}`, http.StatusBadRequest, "InvalidRequest"},
		{"not json", `{"name"`, http.StatusBadRequest, "InvalidRequest"},
	}
	for _, tc := range errs {
		t.Run(tc.name, func(t *testing.T) {
			expectError(t, f.serve(f.sigv4(creds, http.MethodPost, usersPath, "", []byte(tc.body))), tc.status, tc.code)
		})
	}
}

func TestDeleteUser(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("admin", fullPolicy)
	f.createUser("victim", s3OnlyPolicy)
	creds := credsOf(key, secret)

	expectStatus(t, f.serve(f.sigv4(creds, http.MethodDelete, usersPath+"/victim", "", nil)), http.StatusNoContent)
	if users, _ := f.iam.ListUsers(t.Context()); len(users) != 1 {
		t.Errorf("users after delete = %+v", users)
	}
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, usersPath+"/victim", "", nil)), http.StatusNotFound, "NoSuchEntity")
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, usersPath+"/bad%20name", "", nil)), http.StatusBadRequest, "InvalidArgument")
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, usersPath+"/"+testRoot, "", nil)), http.StatusConflict, "RootUserProtected")
}

func TestEnableAndDisableUser(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("admin", fullPolicy)
	bkey, bsecret := f.createUser("bob", fullPolicy)
	creds := credsOf(key, secret)

	expectStatus(t, f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/bob/disable", "", nil)), http.StatusNoContent)
	expectError(t, f.serve(f.sigv4(credsOf(bkey, bsecret), http.MethodGet, BasePath+"/cluster", "", nil)), http.StatusForbidden, "AccessDenied")

	expectStatus(t, f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/bob/enable", "", nil)), http.StatusNoContent)
	expectStatus(t, f.serve(f.sigv4(credsOf(bkey, bsecret), http.MethodGet, BasePath+"/cluster", "", nil)), http.StatusOK)

	expectError(t, f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/ghost/disable", "", nil)), http.StatusNotFound, "NoSuchEntity")
	expectError(t, f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/ghost/enable", "", nil)), http.StatusNotFound, "NoSuchEntity")
	expectError(t, f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/"+testRoot+"/disable", "", nil)), http.StatusConflict, "RootUserProtected")
	expectError(t, f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/"+testRoot+"/enable", "", nil)), http.StatusConflict, "RootUserProtected")
}

func TestCreateKey(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("admin", fullPolicy)
	f.createUser("bob", s3OnlyPolicy)
	creds := credsOf(key, secret)

	resp := f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/bob/keys", "", nil))
	expectStatus(t, resp, http.StatusCreated)
	expectNoStore(t, resp)
	var out struct {
		AccessKeyID string `json:"accessKeyId"`
		SecretKey   string `json:"secretKey"`
	}
	mustUnmarshal(t, resp.Body, &out)
	if out.AccessKeyID == "" || out.SecretKey == "" {
		t.Fatalf("body = %s", resp.Body)
	}

	// The new key authenticates; bob has no admin right, so it is refused by
	// authorization rather than by the signature.
	expectError(t, f.serve(f.sigv4(credsOf(out.AccessKeyID, out.SecretKey), http.MethodGet, BasePath+"/cluster", "", nil)), http.StatusForbidden, "AccessDenied")

	// Errors.
	expectError(t, f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/ghost/keys", "", nil)), http.StatusNotFound, "NoSuchEntity")
	expectError(t, f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/"+testRoot+"/keys", "", nil)), http.StatusConflict, "RootUserProtected")
	expectError(t, f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/bad%20name/keys", "", nil)), http.StatusBadRequest, "InvalidArgument")
}

// TestSecretsAreNeverLogged creates a key and a session with logging at the
// lowest level, and checks that no secret reaches the log.
func TestSecretsAreNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f := newFixture(t, func(c *Config) { c.Logger = logger })
	key, secret := f.createUser("admin", fullPolicy)
	f.createUser("bob", "")
	creds := credsOf(key, secret)

	resp := f.serve(f.sigv4(creds, http.MethodPost, usersPath+"/bob/keys", "", nil))
	expectStatus(t, resp, http.StatusCreated)
	var created struct {
		SecretKey string `json:"secretKey"`
	}
	mustUnmarshal(t, resp.Body, &created)

	// Refusals and logins are logged too; none of them may carry a secret.
	f.serve(f.request(http.MethodPost, BasePath+"/session", map[string]string{"accessKey": key, "secretKey": "wrong-" + secret}))
	f.login(key, secret)
	f.serve(f.sigv4(credsOf(key, "wrong-"+secret), http.MethodGet, BasePath+"/cluster", "", nil))

	logged := buf.String()
	if logged == "" {
		t.Fatal("nothing was logged; the test is not checking anything")
	}
	for _, s := range []string{secret, created.SecretKey, testRootSecret} {
		if strings.Contains(logged, s) {
			t.Fatalf("a secret appears in the log")
		}
	}
	if strings.Contains(logged, "secretKey") {
		t.Error("the log names the secretKey field")
	}
}

func TestKeyDeletion(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("admin", fullPolicy)
	bkey, bsecret := f.createUser("bob", s3OnlyPolicy)
	creds := credsOf(key, secret)

	expectStatus(t, f.serve(f.sigv4(creds, http.MethodDelete, usersPath+"/bob/keys/"+bkey, "", nil)), http.StatusNoContent)
	// The deleted key no longer authenticates.
	expectError(t, f.serve(f.sigv4(credsOf(bkey, bsecret), http.MethodGet, BasePath+"/cluster", "", nil)), http.StatusForbidden, "AccessDenied")

	// A key that belongs to another user is reported as missing, and is kept.
	akey, _ := f.createUser("carol", s3OnlyPolicy)
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, usersPath+"/bob/keys/"+akey, "", nil)), http.StatusNotFound, "NoSuchEntity")
	if keys, _ := f.iam.ListAccessKeys(t.Context(), "carol"); len(keys) != 1 {
		t.Errorf("carol's key was removed through bob: %+v", keys)
	}
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, usersPath+"/bob/keys/NOSUCHKEY", "", nil)), http.StatusNotFound, "NoSuchEntity")
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, usersPath+"/ghost/keys/"+akey, "", nil)), http.StatusNotFound, "NoSuchEntity")
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, usersPath+"/"+testRoot+"/keys/"+akey, "", nil)), http.StatusConflict, "RootUserProtected")
}

// TestNamedPoliciesAreNotImplemented checks the policy endpoints. The IAM store
// has no named policies, so each of them answers 501 until IAM supports them.
func TestNamedPoliciesAreNotImplemented(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("admin", fullPolicy)
	f.createUser("bob", "")
	creds := credsOf(key, secret)
	doc := `{"document":` + adminPolicy + `}`
	cases := []struct {
		method, path string
		body         string
	}{
		{http.MethodGet, BasePath + "/policies", ""},
		{http.MethodPut, BasePath + "/policies/readonly", doc},
		{http.MethodDelete, BasePath + "/policies/readonly", ""},
		{http.MethodPost, usersPath + "/bob/policies", `{"policy":"readonly"}`},
		{http.MethodDelete, usersPath + "/bob/policies/readonly", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp := f.serve(f.sigv4(creds, tc.method, tc.path, "", []byte(tc.body)))
			expectError(t, resp, http.StatusNotImplemented, string(s3err.NotImplemented))
		})
	}
	// Users still carry an empty policies array.
	resp := f.serve(f.sigv4(creds, http.MethodGet, usersPath, "", nil))
	var out []map[string]json.RawMessage
	mustUnmarshal(t, resp.Body, &out)
	if string(out[0]["policies"]) != "[]" {
		t.Errorf("policies = %s, want []", out[0]["policies"])
	}
}

func TestUserEndpointMethods(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("admin", fullPolicy)
	creds := credsOf(key, secret)
	expectError(t, f.serve(f.sigv4(creds, http.MethodPut, usersPath, "", nil)), http.StatusMethodNotAllowed, "MethodNotAllowed")
	expectError(t, f.serve(f.sigv4(creds, http.MethodGet, usersPath+"/admin", "", nil)), http.StatusMethodNotAllowed, "MethodNotAllowed")
	expectError(t, f.serve(f.sigv4(creds, http.MethodGet, usersPath+"/admin/enable", "", nil)), http.StatusMethodNotAllowed, "MethodNotAllowed")
}
