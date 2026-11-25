package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eukalpia/unio/internal/console"
)

// The admin listener serves the console and nothing outside /unio/admin/ and
// /unio/console/: no S3 path, no metrics, no health.
func TestAdminHandlerServesOnlyAdminAndConsole(t *testing.T) {
	con, err := console.New()
	if err != nil {
		t.Fatal(err)
	}
	h := adminHandler(nil, con)
	for path, want := range map[string]int{
		"/unio/console/":    http.StatusOK,
		"/unio/metrics":     http.StatusNotFound,
		"/unio/health/live": http.StatusNotFound,
		"/bucket/key":       http.StatusNotFound,
		"/":                 http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}
