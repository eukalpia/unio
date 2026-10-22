package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// bucketsPath is the collection of buckets.
const bucketsPath = BasePath + "/buckets"

func TestListBuckets(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	token, _ := f.login(key, secret)

	resp := f.serve(with(f.get(bucketsPath), withCookie(token)))
	expectStatus(t, resp, http.StatusOK)
	expectNoStore(t, resp)
	if got := string(resp.Body); got != "[]" {
		t.Fatalf("empty layer lists %s, want []", got)
	}

	if _, err := f.layer.MakeBucket(context.Background(), "photos", bucketOpts()); err != nil {
		t.Fatal(err)
	}
	resp = f.serve(with(f.get(bucketsPath), withCookie(token)))
	expectStatus(t, resp, http.StatusOK)
	var out []struct {
		Name       string `json:"name"`
		Created    string `json:"created"`
		Versioning string `json:"versioning"`
	}
	mustUnmarshal(t, resp.Body, &out)
	if len(out) != 1 || out[0].Name != "photos" || out[0].Versioning != "Unversioned" {
		t.Fatalf("buckets = %+v", out)
	}
	if out[0].Created == "" || !strings.HasSuffix(out[0].Created, "Z") {
		t.Errorf("created = %q, want an RFC 3339 UTC time", out[0].Created)
	}
}

func TestCreateBucket(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	token, csrf := f.login(key, secret)

	post := func(body any, mods ...func(*Request)) Response {
		t.Helper()
		return f.serve(with(f.request(http.MethodPost, bucketsPath, body), append([]func(*Request){withCookie(token), withCSRF(csrf)}, mods...)...))
	}

	resp := post(map[string]string{"name": "photos"})
	expectStatus(t, resp, http.StatusCreated)
	if string(resp.Body) != `{"name":"photos"}` {
		t.Errorf("body = %s", resp.Body)
	}
	info, err := f.layer.GetBucketInfo(context.Background(), "photos")
	if err != nil {
		t.Fatal(err)
	}
	if info.Owner != "alice" {
		t.Errorf("owner = %q, want the session user", info.Owner)
	}

	errs := []struct {
		name   string
		resp   Response
		status int
		code   string
	}{
		{"duplicate by the same owner", post(map[string]string{"name": "photos"}), http.StatusConflict, "BucketAlreadyOwnedByYou"},
		{"uppercase", post(map[string]string{"name": "Photos"}), http.StatusBadRequest, "InvalidBucketName"},
		{"too short", post(map[string]string{"name": "ab"}), http.StatusBadRequest, "InvalidBucketName"},
		{"reserved unio", post(map[string]string{"name": "unio"}), http.StatusBadRequest, "InvalidBucketName"},
		{"ip address", post(map[string]string{"name": "10.0.0.1"}), http.StatusBadRequest, "InvalidBucketName"},
		{"empty name", post(map[string]string{"name": ""}), http.StatusBadRequest, "InvalidBucketName"},
		{"missing name", post(map[string]string{}), http.StatusBadRequest, "InvalidBucketName"},
		{"unknown field", post(`{"name":"ok-bucket","region":"x"}`), http.StatusBadRequest, "InvalidRequest"},
		{"not json", post(`{name`), http.StatusBadRequest, "InvalidRequest"},
		{"no body", post(nil), http.StatusBadRequest, "InvalidRequest"},
		{"text body", post(`{"name":"ok-bucket"}`, func(r *Request) { r.Header.Set("Content-Type", "text/plain") }), http.StatusUnsupportedMediaType, "InvalidRequest"},
	}
	for _, tc := range errs {
		t.Run(tc.name, func(t *testing.T) {
			expectError(t, tc.resp, tc.status, tc.code)
		})
	}
}

func TestCreateBucketAsRootIsOwnedByUnio(t *testing.T) {
	f := newFixture(t)
	resp := f.serve(f.sigv4(rootCreds(), http.MethodPost, bucketsPath, "", []byte(`{"name":"rootbucket"}`)))
	expectStatus(t, resp, http.StatusCreated)
	info, err := f.layer.GetBucketInfo(context.Background(), "rootbucket")
	if err != nil {
		t.Fatal(err)
	}
	if info.Owner != "unio" {
		t.Errorf("owner = %q, want unio", info.Owner)
	}
}

func TestDeleteBucket(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)
	ctx := context.Background()
	if _, err := f.layer.MakeBucket(ctx, "empty-one", bucketOpts()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.layer.MakeBucket(ctx, "full-one", bucketOpts()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.layer.PutObject(ctx, "full-one", "k", newBody([]byte("data")), putOpts()); err != nil {
		t.Fatal(err)
	}

	expectStatus(t, f.serve(f.sigv4(creds, http.MethodDelete, bucketsPath+"/empty-one", "", nil)), http.StatusNoContent)
	if _, err := f.layer.GetBucketInfo(ctx, "empty-one"); err == nil {
		t.Error("the bucket still exists after the delete")
	}
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, bucketsPath+"/full-one", "", nil)), http.StatusConflict, "BucketNotEmpty")
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, bucketsPath+"/missing-one", "", nil)), http.StatusNotFound, "NoSuchBucket")
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, bucketsPath+"/Bad_Name", "", nil)), http.StatusBadRequest, "InvalidBucketName")
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, bucketsPath+"/unio", "", nil)), http.StatusBadRequest, "InvalidBucketName")
}

func TestBucketsCollectionMethods(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)
	expectError(t, f.serve(f.sigv4(creds, http.MethodPut, bucketsPath, "", nil)), http.StatusMethodNotAllowed, "MethodNotAllowed")
	expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, bucketsPath, "", nil)), http.StatusMethodNotAllowed, "MethodNotAllowed")
}
