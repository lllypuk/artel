package api

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// MaxBody is the maximum JSON mutation request size.
const MaxBody = 256 * 1024

// Error writes only public, stable errors, never underlying IO/SQL details.
func Error(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message})
}

// Protect applies transport policy before routing, including to unknown endpoints.
func Protect(token, host string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.Host != host || len(r.Header.Values("Origin")) != 0 || r.URL.IsAbs() {
			Error(w, 400, "invalid_request", "non-local request rejected")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			auth := r.Header.Values("Authorization")
			if len(auth) != 1 || token == "" || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+token)) != 1 {
				Error(w, 401, "unauthorized", "owner credential required")
				return
			}
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
		if err != nil {
			Error(w, 413, "body_too_large", "request body exceeds limit")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}
