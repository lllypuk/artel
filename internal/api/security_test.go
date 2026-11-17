package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurity(t *testing.T) {
	_, routes := fixture(t)
	const host = "127.0.0.1:12345"
	token := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, path, auth, host, origin string
		size, status                   int
	}{
		{"health", "/healthz", "", host, "", 0, 200},
		{"missing", "/v1/snapshot", "", host, "", 0, 401},
		{"wrong", "/v1/snapshot", "Bearer wrong", host, "", 0, 401},
		{"authenticated", "/v1/snapshot", "Bearer " + token, host, "", 0, 200},
		{"unknown protected", "/v1/unknown", "", host, "", 0, 401},
		{"origin", "/healthz", "", host, "https://evil.test", 0, 400},
		{"origin protected", "/v1/snapshot", "Bearer " + token, host, "null", 0, 400},
		{"host", "/healthz", "", "evil.test", "", 0, 400},
		{"wrong port", "/healthz", "", "127.0.0.1:1", "", 0, 400},
		{"oversize", "/v1/projects", "Bearer " + token, host, "", MaxBody + 1, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+host+tc.path, strings.NewReader(strings.Repeat("x", tc.size)))
			// Incoming origin-form requests, unlike proxy absolute-form requests.
			req.URL.Scheme = ""
			req.URL.Host = ""
			req.Host = tc.host
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			Protect(token, host, routes).ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "evil.test") {
				t.Fatal("sensitive diagnostic")
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS enabled")
			}
		})
	}
}

func TestEveryContractRouteRequiresOwner(t *testing.T) {
	_, routes := fixture(t)
	const host = "127.0.0.1:12345"
	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/snapshot"}, {"POST", "/v1/projects"}, {"PATCH", "/v1/projects/id"},
		{"POST", "/v1/projects/id/tasks"}, {"PATCH", "/v1/tasks/id"}, {"POST", "/v1/tasks/id/transition"},
		{"PUT", "/v1/projects/id/queue"}, {"GET", "/v1/events"}, {"GET", "/v1/stream"},
	} {
		for _, authenticated := range []bool{false, true} {
			req := httptest.NewRequest(route.method, route.path, nil)
			req.Host = host
			expected := 401
			if authenticated {
				req.Header.Set("Authorization", "Bearer owner")
				expected = 400
				if route.path == "/v1/snapshot" {
					expected = 200
				}
			}
			w := httptest.NewRecorder()
			Protect("owner", host, routes).ServeHTTP(w, req)
			if w.Code != expected {
				t.Errorf("%s %s: got %d expected %d", route.method, route.path, w.Code, expected)
			}
			checkContract(t, req, w)
		}
	}
}

func TestSecurityBodyAndDuplicateHeaders(t *testing.T) {
	handler := Protect("token", "127.0.0.1:1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body [4]byte
		if _, err := r.Body.Read(body[:]); err != nil || string(body[:]) != "test" {
			t.Error("body not preserved")
		}
		w.WriteHeader(204)
	}))
	req := httptest.NewRequest("POST", "/v1/projects", strings.NewReader("test"))
	req.Host = "127.0.0.1:1"
	req.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	req.Header.Add("Authorization", "Bearer token")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("duplicate auth accepted")
	}
	req = httptest.NewRequest("GET", "http://127.0.0.1:1/healthz", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("absolute request accepted")
	}
}
