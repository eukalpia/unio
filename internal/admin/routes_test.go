package admin

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMatchRoutes(t *testing.T) {
	cases := []struct {
		method, path string
		want         routeID
		params       []string
		status       int
		code         string
	}{
		{"POST", "/unio/admin/v1/session", routeLogin, nil, 0, ""},
		{"GET", "/unio/admin/v1/session", routeSessionGet, nil, 0, ""},
		{"DELETE", "/unio/admin/v1/session", routeLogout, nil, 0, ""},
		{"GET", "/unio/admin/v1/cluster", routeCluster, nil, 0, ""},
		{"GET", "/unio/admin/v1/metrics/summary", routeMetricsSummary, nil, 0, ""},
		{"GET", "/unio/admin/v1/buckets", routeBucketsList, nil, 0, ""},
		{"POST", "/unio/admin/v1/buckets", routeBucketsCreate, nil, 0, ""},
		{"DELETE", "/unio/admin/v1/buckets/photos", routeBucketDelete, []string{"photos"}, 0, ""},
		{"GET", "/unio/admin/v1/buckets/photos/objects", routeObjectsList, []string{"photos"}, 0, ""},
		{"DELETE", "/unio/admin/v1/buckets/photos/objects", routeObjectDelete, []string{"photos"}, 0, ""},
		{"POST", "/unio/admin/v1/buckets/photos/presign", routePresign, []string{"photos"}, 0, ""},
		{"GET", "/unio/admin/v1/users", routeUsersList, nil, 0, ""},
		{"POST", "/unio/admin/v1/users", routeUsersCreate, nil, 0, ""},
		{"DELETE", "/unio/admin/v1/users/alice", routeUserDelete, []string{"alice"}, 0, ""},
		{"POST", "/unio/admin/v1/users/alice/enable", routeUserEnable, []string{"alice"}, 0, ""},
		{"POST", "/unio/admin/v1/users/alice/disable", routeUserDisable, []string{"alice"}, 0, ""},
		{"POST", "/unio/admin/v1/users/alice/keys", routeKeyCreate, []string{"alice"}, 0, ""},
		{"DELETE", "/unio/admin/v1/users/alice/keys/AKIA0001", routeKeyDelete, []string{"alice", "AKIA0001"}, 0, ""},
		{"POST", "/unio/admin/v1/users/alice/policies", routePolicyAttach, []string{"alice"}, 0, ""},
		{"DELETE", "/unio/admin/v1/users/alice/policies/p", routePolicyDetach, []string{"alice", "p"}, 0, ""},
		{"GET", "/unio/admin/v1/policies", routePoliciesList, nil, 0, ""},
		{"PUT", "/unio/admin/v1/policies/p", routePolicyPut, []string{"p"}, 0, ""},
		{"DELETE", "/unio/admin/v1/policies/p", routePolicyDelete, []string{"p"}, 0, ""},

		{"PUT", "/unio/admin/v1/cluster", routeNone, nil, 405, "MethodNotAllowed"},
		{"GET", "/unio/admin/v1/nope", routeNone, nil, 404, "NoSuchEntity"},
		{"GET", "/unio/admin/v1/", routeNone, nil, 404, "NoSuchEntity"},
		{"GET", "/unio/admin/v1", routeNone, nil, 404, "NoSuchEntity"},
		{"GET", "/unio/admin/v1/cluster/", routeNone, nil, 404, "NoSuchEntity"},
		{"GET", "/unio/admin/v1/buckets//objects", routeNone, nil, 404, "NoSuchEntity"},
		{"GET", "/unio/admin/v1/buckets/a/b/c/d", routeNone, nil, 404, "NoSuchEntity"},
		{"GET", "/unio/admin/v2/cluster", routeNone, nil, 404, "NoSuchEntity"},
		{"GET", "/cluster", routeNone, nil, 404, "NoSuchEntity"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			id, params, err := match(tc.method, tc.path)
			if tc.status != 0 {
				if err == nil {
					t.Fatalf("matched route %d, want error %d %s", id, tc.status, tc.code)
				}
				expectErrorValue(t, err, tc.status, tc.code)
				return
			}
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if id != tc.want {
				t.Errorf("route = %d, want %d", id, tc.want)
			}
			if strings.Join(params, "/") != strings.Join(tc.params, "/") || len(params) != len(tc.params) {
				t.Errorf("params = %q, want %q", params, tc.params)
			}
		})
	}
}

// expectErrorValue checks a route error against a status and code.
func expectErrorValue(t *testing.T, err error, status int, code string) {
	t.Helper()
	gotStatus, gotCode, _ := describe(err)
	if gotStatus != status || string(gotCode) != code {
		t.Fatalf("error = %d %s, want %d %s", gotStatus, gotCode, status, code)
	}
}

// TestBodyLimit checks the 64 KiB body limit: a body of exactly the limit is
// read, and one byte more is 413 before it is decoded.
func TestBodyLimit(t *testing.T) {
	ok := bytes.Repeat([]byte("a"), MaxBodyBytes)
	b, err := readBody(bytes.NewReader(ok))
	if err != nil || len(b) != MaxBodyBytes {
		t.Fatalf("a body of the limit: len=%d err=%v", len(b), err)
	}
	_, err = readBody(bytes.NewReader(append(ok, 'a')))
	expectErrorValue(t, err, http.StatusRequestEntityTooLarge, "EntityTooLarge")
}

// failingReader fails every read with a plain error.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestBodyReadFailureIsBadRequest(t *testing.T) {
	_, err := readBody(failingReader{})
	expectErrorValue(t, err, http.StatusBadRequest, "InvalidRequest")
}

// TestDecodeJSONStrictness covers the rules of every JSON body: one value, no
// unknown field, and nothing after the value.
func TestDecodeJSONStrictness(t *testing.T) {
	type body struct {
		Name string `json:"name"`
	}
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"valid", `{"name":"x"}`, true},
		{"surrounding space", "  {\"name\":\"x\"}\n", true},
		{"null", `null`, true},
		{"unknown field", `{"name":"x","role":"admin"}`, false},
		{"two values", `{"name":"x"}{"name":"y"}`, false},
		{"trailing garbage", `{"name":"x"} x`, false},
		{"wrong type", `{"name":5}`, false},
		{"empty", ``, false},
		{"array", `["x"]`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b body
			err := decodeJSON([]byte(tc.in), &b)
			if tc.ok && err != nil {
				t.Errorf("rejected: %v", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("accepted %q as %+v", tc.in, b)
			}
			if err != nil && strings.Contains(err.Error(), "role") {
				t.Errorf("the error names the field: %v", err)
			}
		})
	}
}

// TestDecodeBodyNeedsJSONContentType checks the media type rule of a body.
func TestDecodeBodyNeedsJSONContentType(t *testing.T) {
	type body struct {
		Name string `json:"name"`
	}
	f := newFixture(t)
	mk := func(ctype string) *call {
		req := f.request(http.MethodPost, BasePath+"/buckets", nil)
		req.Header.Set("Content-Type", ctype)
		return &call{req: req, body: []byte(`{"name":"photos"}`)}
	}
	for _, ct := range []string{"application/json", "application/json; charset=utf-8"} {
		var b body
		if err := decodeBody(mk(ct), &b); err != nil {
			t.Errorf("Content-Type %q: %v", ct, err)
		}
	}
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "application/jsonx", "application/json;;;"} {
		var b body
		err := decodeBody(mk(ct), &b)
		expectErrorValue(t, err, http.StatusUnsupportedMediaType, "InvalidRequest")
	}
	var b body
	err := decodeBody(&call{req: f.request(http.MethodPost, BasePath+"/x", nil)}, &b)
	expectErrorValue(t, err, http.StatusBadRequest, "InvalidRequest")
}

// TestUnknownRoutesAreNotFoundForEveryMethod checks that the router answers
// before any authentication, so a path that names nothing is 404 for everyone.
func TestUnknownRoutesAreNotFoundForEveryMethod(t *testing.T) {
	f := newFixture(t)
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		expectError(t, f.serve(f.request(m, BasePath+"/nothing", nil)), http.StatusNotFound, "NoSuchEntity")
	}
}
