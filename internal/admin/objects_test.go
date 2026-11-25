package admin

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

// seedObjects writes the keys into bucket, each with a small body.
func seedObjects(t *testing.T, f *fixture, bucket string, keys ...string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.layer.MakeBucket(ctx, bucket, bucketOpts()); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if _, err := f.layer.PutObject(ctx, bucket, k, newBody([]byte("v:"+k)), putOpts()); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
}

// listing is the decoded body of an object listing.
type listing struct {
	Prefixes []string `json:"prefixes"`
	Objects  []struct {
		Key          string `json:"key"`
		Size         int64  `json:"size"`
		ETag         string `json:"etag"`
		LastModified string `json:"lastModified"`
	} `json:"objects"`
	NextToken *string `json:"nextToken"`
}

func TestListObjects(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)
	seedObjects(t, f, "photos", "a/1", "a/2", "b/1", "c")

	get := func(q url.Values) (listing, Response) {
		t.Helper()
		resp := f.serve(f.sigv4(creds, http.MethodGet, bucketsPath+"/photos/objects", q.Encode(), nil))
		var out listing
		if resp.Status == http.StatusOK {
			mustUnmarshal(t, resp.Body, &out)
		}
		return out, resp
	}

	out, resp := get(url.Values{})
	expectStatus(t, resp, http.StatusOK)
	expectNoStore(t, resp)
	if len(out.Objects) != 4 || out.Objects[0].Key != "a/1" || out.Objects[3].Key != "c" {
		t.Fatalf("all keys = %+v", out.Objects)
	}
	if out.NextToken != nil {
		t.Errorf("nextToken = %v on a complete listing, want null", *out.NextToken)
	}
	if out.Objects[0].ETag == "" || out.Objects[0].Size != int64(len("v:a/1")) || out.Objects[0].LastModified == "" {
		t.Errorf("object fields = %+v", out.Objects[0])
	}
	if out.Prefixes == nil {
		t.Error("prefixes is null, want an array")
	}

	out, _ = get(url.Values{"prefix": {"a/"}})
	if len(out.Objects) != 2 || out.Objects[0].Key != "a/1" {
		t.Errorf("prefix a/ = %+v", out.Objects)
	}

	out, _ = get(url.Values{"delimiter": {"/"}})
	if len(out.Prefixes) != 2 || out.Prefixes[0] != "a/" || out.Prefixes[1] != "b/" || len(out.Objects) != 1 || out.Objects[0].Key != "c" {
		t.Errorf("delimiter / gave prefixes %v and objects %+v", out.Prefixes, out.Objects)
	}

	// Page through the bucket two keys at a time.
	var keys []string
	token := ""
	for i := 0; i < 5; i++ {
		q := url.Values{"max": {"2"}}
		if token != "" {
			q.Set("token", token)
		}
		page, resp := get(q)
		expectStatus(t, resp, http.StatusOK)
		for _, o := range page.Objects {
			keys = append(keys, o.Key)
		}
		if page.NextToken == nil {
			break
		}
		token = *page.NextToken
	}
	if len(keys) != 4 || keys[0] != "a/1" || keys[3] != "c" {
		t.Errorf("paged keys = %v", keys)
	}

	// A listing of an empty bucket is an empty array, not null.
	if _, err := f.layer.MakeBucket(context.Background(), "empty", bucketOpts()); err != nil {
		t.Fatal(err)
	}
	resp = f.serve(f.sigv4(creds, http.MethodGet, bucketsPath+"/empty/objects", "", nil))
	expectStatus(t, resp, http.StatusOK)
	var raw map[string]any
	mustUnmarshal(t, resp.Body, &raw)
	if raw["objects"] == nil || raw["prefixes"] == nil {
		t.Errorf("empty listing = %s, want arrays", resp.Body)
	}
}

func TestListObjectsErrors(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)
	seedObjects(t, f, "photos", "a")
	path := bucketsPath + "/photos/objects"

	cases := []struct {
		name   string
		path   string
		query  string
		status int
		code   string
	}{
		{"missing bucket", bucketsPath + "/missing/objects", "", http.StatusNotFound, "NoSuchBucket"},
		{"invalid bucket", bucketsPath + "/Bad_Name/objects", "", http.StatusBadRequest, "InvalidBucketName"},
		{"max zero", path, "max=0", http.StatusBadRequest, "InvalidArgument"},
		{"max over 1000", path, "max=1001", http.StatusBadRequest, "InvalidArgument"},
		{"max not a number", path, "max=ten", http.StatusBadRequest, "InvalidArgument"},
		{"max signed", path, "max=-5", http.StatusBadRequest, "InvalidArgument"},
		{"repeated prefix", path, "prefix=a&prefix=b", http.StatusBadRequest, "InvalidRequest"},
		{"overlong prefix", path, "prefix=" + longString(2000), http.StatusBadRequest, "InvalidArgument"},
		{"overlong token", path, "token=" + longString(2000), http.StatusBadRequest, "InvalidArgument"},
		{"malformed query", path, "a=%zz", http.StatusBadRequest, "InvalidRequest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := f.serve(f.sigv4(creds, http.MethodGet, tc.path, tc.query, nil))
			expectError(t, resp, tc.status, tc.code)
		})
	}
}

func TestDeleteObject(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)
	seedObjects(t, f, "photos", "gone", "kept")
	path := bucketsPath + "/photos/objects"

	expectStatus(t, f.serve(f.sigv4(creds, http.MethodDelete, path, "key=gone", nil)), http.StatusNoContent)
	if _, err := f.layer.HeadObject(context.Background(), "photos", "gone", getOpts()); err == nil {
		t.Error("the object still exists after the delete")
	}
	if _, err := f.layer.HeadObject(context.Background(), "photos", "kept", getOpts()); err != nil {
		t.Errorf("another object was affected: %v", err)
	}

	// Deleting a key that is already gone succeeds, as on the S3 API.
	expectStatus(t, f.serve(f.sigv4(creds, http.MethodDelete, path, "key=gone", nil)), http.StatusNoContent)

	cases := []struct {
		name   string
		path   string
		query  string
		status int
		code   string
	}{
		{"no key parameter", path, "", http.StatusBadRequest, "InvalidArgument"},
		{"empty key", path, "key=", http.StatusBadRequest, "InvalidArgument"},
		{"key repeated", path, "key=a&key=b", http.StatusBadRequest, "InvalidRequest"},
		{"overlong key", path, "key=" + longString(2000), http.StatusBadRequest, "KeyTooLongError"},
		{"missing bucket", bucketsPath + "/missing/objects", "key=x", http.StatusNotFound, "NoSuchBucket"},
		{"invalid bucket", bucketsPath + "/a/objects", "key=x", http.StatusBadRequest, "InvalidBucketName"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectError(t, f.serve(f.sigv4(creds, http.MethodDelete, tc.path, tc.query, nil)), tc.status, tc.code)
		})
	}
}

// longString returns n copies of "a".
func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
