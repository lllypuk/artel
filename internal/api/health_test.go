package api

import (
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/healthz", 200}, {"POST", "/healthz", 405}, {"GET", "/v1/snapshot", 404},
	} {
		r := httptest.NewRecorder()
		Handler().ServeHTTP(r, httptest.NewRequest(tc.method, tc.path, nil))
		if r.Code != tc.status {
			t.Fatalf("%s %s: %d", tc.method, tc.path, r.Code)
		}
		if tc.status == 200 && (r.Body.String() != "{\"status\":\"ok\"}\n" || r.Header().Get("Content-Type") != "application/json") {
			t.Fatal("invalid liveness response")
		}
	}
}
