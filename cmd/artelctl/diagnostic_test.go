package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lllypuk/artel/internal/local"
)

func TestDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "owner.token")
	token, err := local.InitToken(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/snapshot":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("missing credential")
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(501)
			_, _ = w.Write([]byte(token)) // Server error bodies must never be echoed.
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"status", "--json", "--endpoint", server.URL}, 0},
		{[]string{"snapshot", "--json", "--endpoint", server.URL, "--token-file", path}, 1},
		{[]string{"snapshot", "--json", "--endpoint", server.URL, "--token-file", filepath.Join(t.TempDir(), "missing")}, 1},
		{[]string{"status", "--json", "--endpoint", "http://example.com:7331"}, 2},
		{[]string{"status", "--json", "--endpoint", "http://127.0.0.1:7331/?secret"}, 2},
		{[]string{"status", "--json", "extra"}, 2},
	} {
		var out, diagnostic bytes.Buffer
		if code := run(tc.args, &out, &diagnostic); code != tc.code {
			t.Fatalf("got %d expected %d", code, tc.code)
		}
		if strings.Contains(out.String()+diagnostic.String(), token) || strings.Contains(diagnostic.String(), path) {
			t.Fatal("secret or path echoed")
		}
	}
}

func TestDiagnosticBadResponses(t *testing.T) {
	for _, mode := range []string{"json", "redirect", "large", "empty", "array"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				switch mode {
				case "empty":
					_, _ = w.Write([]byte(`{}`))
				case "array":
					_, _ = w.Write([]byte(`[]`))
				case "json":
					_, _ = w.Write([]byte("not JSON"))
				case "redirect":
					w.Header().Set("Location", "http://example.com")
					w.WriteHeader(302)
				case "large":
					_, _ = w.Write([]byte(strings.Repeat(" ", 32*1024*1024+1)))
				}
			}))
			defer server.Close()
			var out, diagnostic bytes.Buffer
			if run([]string{"status", "--json", "--endpoint", server.URL}, &out, &diagnostic) != 1 || out.Len() != 0 {
				t.Fatal("invalid response accepted")
			}
		})
	}
}
