package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// adminCall is one request to a running node, with the session and CSRF
// token attached when they are given.
type adminCall struct {
	method, path string
	cookie, csrf string
	body         any
}

// adminResult is the status, the headers and the body of an answer.
type adminResult struct {
	status int
	header http.Header
	body   []byte
}

// doAdmin sends an admin call to base and returns the answer.
func doAdmin(t *testing.T, base string, c adminCall) adminResult {
	t.Helper()
	var rd io.Reader
	if c.body != nil {
		b, err := json.Marshal(c.body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(c.method, base+c.path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if c.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", "unio_session="+c.cookie)
	}
	if c.csrf != "" {
		req.Header.Set("X-Unio-CSRF", c.csrf)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", c.method, c.path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return adminResult{status: resp.StatusCode, header: resp.Header, body: b}
}

// sessionCookieOf returns the value of the session cookie in a response.
func sessionCookieOf(t *testing.T, h http.Header) string {
	t.Helper()
	r := &http.Response{Header: http.Header{"Set-Cookie": h.Values("Set-Cookie")}}
	for _, c := range r.Cookies() {
		if c.Name == "unio_session" {
			return c.Value
		}
	}
	t.Fatalf("no session cookie in %v", h.Values("Set-Cookie"))
	return ""
}

// TestAdminAPIOnRealNode runs the admin API against a one-node cluster in this
// process. It logs in, reads the cluster, creates a bucket, presigns a PUT,
// uploads through the presigned URL with a plain HTTP client, lists the
// bucket, and logs out. A second user without the admin right is refused.
func TestAdminAPIOnRealNode(t *testing.T) {
	n := newTestNode(t, "127.0.0.1", 4)
	r := serve(t, loadConfig(t, n, []*testNode{n}, nil))
	waitReady(t, r.node.S3Addr().String())
	base := "http://" + r.node.S3Addr().String()

	// Log in as the root account.
	login := doAdmin(t, base, adminCall{method: http.MethodPost, path: "/unio/admin/v1/session",
		body: map[string]string{"accessKey": testRootUser, "secretKey": testRootPassword}})
	if login.status != http.StatusOK {
		t.Fatalf("login: status %d body %s", login.status, login.body)
	}
	var session struct {
		AccessKey string `json:"accessKey"`
		ExpiresAt string `json:"expiresAt"`
		CSRF      string `json:"csrf"`
	}
	mustJSON(t, login.body, &session)
	if session.AccessKey != testRootUser || session.ExpiresAt == "" || session.CSRF == "" {
		t.Fatalf("session = %+v", session)
	}
	cookie := sessionCookieOf(t, login.header)
	if strings.Contains(string(login.body), testRootPassword) {
		t.Fatal("the login answer contains the root secret")
	}

	// Who am I, and what does the cluster look like.
	me := doAdmin(t, base, adminCall{method: http.MethodGet, path: "/unio/admin/v1/session", cookie: cookie})
	if me.status != http.StatusOK {
		t.Fatalf("session: status %d body %s", me.status, me.body)
	}
	cl := doAdmin(t, base, adminCall{method: http.MethodGet, path: "/unio/admin/v1/cluster", cookie: cookie})
	if cl.status != http.StatusOK {
		t.Fatalf("cluster: status %d body %s", cl.status, cl.body)
	}
	var cluster struct {
		ClusterID string `json:"clusterId"`
		Nodes     []struct {
			ID      string `json:"id"`
			Healthy bool   `json:"healthy"`
		} `json:"nodes"`
		Sets []struct {
			DataShards   int `json:"dataShards"`
			ParityShards int `json:"parityShards"`
			Drives       []struct {
				Online bool   `json:"online"`
				Total  uint64 `json:"total"`
			} `json:"drives"`
		} `json:"sets"`
		Capacity struct {
			Total uint64 `json:"total"`
		} `json:"capacity"`
	}
	mustJSON(t, cl.body, &cluster)
	if cluster.ClusterID == "" || len(cluster.Nodes) != 1 || !cluster.Nodes[0].Healthy {
		t.Fatalf("cluster = %s", cl.body)
	}
	if len(cluster.Sets) != 1 || len(cluster.Sets[0].Drives) != 4 {
		t.Fatalf("sets = %s", cl.body)
	}
	for _, d := range cluster.Sets[0].Drives {
		if !d.Online || d.Total == 0 {
			t.Errorf("drive %+v is not online with capacity", d)
		}
	}
	if cluster.Capacity.Total == 0 {
		t.Errorf("capacity = %+v", cluster.Capacity)
	}

	// A bucket, created through the API.
	mk := doAdmin(t, base, adminCall{method: http.MethodPost, path: "/unio/admin/v1/buckets",
		cookie: cookie, csrf: session.CSRF, body: map[string]string{"name": "admin-bucket"}})
	if mk.status != http.StatusCreated || string(mk.body) != `{"name":"admin-bucket"}` {
		t.Fatalf("create bucket: status %d body %s", mk.status, mk.body)
	}

	// A presigned PUT, then the upload through it with a plain HTTP client.
	const key = "docs/hello world.txt"
	const content = "hello from a presigned URL"
	presign := doAdmin(t, base, adminCall{method: http.MethodPost, path: "/unio/admin/v1/buckets/admin-bucket/presign",
		cookie: cookie, csrf: session.CSRF, body: map[string]any{"method": "PUT", "key": key, "expiresSeconds": 300}})
	if presign.status != http.StatusOK {
		t.Fatalf("presign: status %d body %s", presign.status, presign.body)
	}
	var signed struct {
		URL string `json:"url"`
	}
	mustJSON(t, presign.body, &signed)
	if !strings.HasPrefix(signed.URL, base+"/admin-bucket/") {
		t.Fatalf("presigned URL %s is not on the node", signed.URL)
	}
	req, err := http.NewRequest(http.MethodPut, signed.URL, strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	put, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, put.Body)
	_ = put.Body.Close()
	if put.StatusCode != http.StatusOK {
		t.Fatalf("upload through the presigned URL: status %d", put.StatusCode)
	}

	// The object is listed, with its size.
	list := doAdmin(t, base, adminCall{method: http.MethodGet,
		path: "/unio/admin/v1/buckets/admin-bucket/objects?prefix=" + url.QueryEscape("docs/"), cookie: cookie})
	if list.status != http.StatusOK {
		t.Fatalf("list: status %d body %s", list.status, list.body)
	}
	var listed struct {
		Objects []struct {
			Key  string `json:"key"`
			Size int64  `json:"size"`
		} `json:"objects"`
	}
	mustJSON(t, list.body, &listed)
	if len(listed.Objects) != 1 || listed.Objects[0].Key != key || listed.Objects[0].Size != int64(len(content)) {
		t.Fatalf("listing = %s", list.body)
	}

	// A presigned GET reads the same bytes back.
	get := doAdmin(t, base, adminCall{method: http.MethodPost, path: "/unio/admin/v1/buckets/admin-bucket/presign",
		cookie: cookie, csrf: session.CSRF, body: map[string]any{"method": "GET", "key": key, "expiresSeconds": 60}})
	mustJSON(t, get.body, &signed)
	dl, err := (&http.Client{}).Get(signed.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	if dl.StatusCode != http.StatusOK || string(got) != content {
		t.Fatalf("download through the presigned URL: status %d body %q", dl.StatusCode, got)
	}

	// A user with no admin right can log in, but cannot use the API.
	mkUser := doAdmin(t, base, adminCall{method: http.MethodPost, path: "/unio/admin/v1/users",
		cookie: cookie, csrf: session.CSRF, body: map[string]string{"name": "reader"}})
	if mkUser.status != http.StatusCreated {
		t.Fatalf("create user: status %d body %s", mkUser.status, mkUser.body)
	}
	mkKey := doAdmin(t, base, adminCall{method: http.MethodPost, path: "/unio/admin/v1/users/reader/keys",
		cookie: cookie, csrf: session.CSRF})
	if mkKey.status != http.StatusCreated {
		t.Fatalf("create key: status %d body %s", mkKey.status, mkKey.body)
	}
	if mkKey.header.Get("Cache-Control") != "no-store" {
		t.Errorf("the key answer is cacheable: %q", mkKey.header.Get("Cache-Control"))
	}
	var reader struct {
		AccessKeyID string `json:"accessKeyId"`
		SecretKey   string `json:"secretKey"`
	}
	mustJSON(t, mkKey.body, &reader)
	denied := doAdmin(t, base, adminCall{method: http.MethodPost, path: "/unio/admin/v1/session",
		body: map[string]string{"accessKey": reader.AccessKeyID, "secretKey": reader.SecretKey}})
	if denied.status != http.StatusForbidden || !strings.Contains(string(denied.body), "AccessDenied") {
		t.Fatalf("login of a user without the admin right: status %d body %s", denied.status, denied.body)
	}

	// Logout clears the cookie.
	out := doAdmin(t, base, adminCall{method: http.MethodDelete, path: "/unio/admin/v1/session", cookie: cookie})
	if out.status != http.StatusNoContent || !strings.Contains(strings.Join(out.header.Values("Set-Cookie"), ""), "Max-Age=0") {
		t.Fatalf("logout: status %d cookies %v", out.status, out.header.Values("Set-Cookie"))
	}

	r.stop(t)
}

// mustJSON decodes body into v, failing the test on error.
func mustJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}
