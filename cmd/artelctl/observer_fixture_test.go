package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lllypuk/artel/internal/local"
)

func assertCanonicalProvision(t *testing.T, keys []string, payloads []local.ObserverProvision, pending local.ObserverConfig, expected int) {
	t.Helper()
	if len(keys) != expected {
		t.Fatalf("PUT count %d", len(keys))
	}
	for i := range keys {
		if keys[i] != pending.ProvisioningKey || payloads[i] != pending.Payload {
			t.Fatal("resume changed canonical payload or key")
		}
	}
}

func lostProvisionHandler(t *testing.T, handler http.Handler, config, ownerToken string, drop *atomic.Bool, record func(string, local.ObserverProvision)) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+ownerToken {
			w.WriteHeader(401)
			return
		}
		if r.Method == "PUT" {
			c, err := local.ReadObserverConfig(config, false)
			if err != nil {
				t.Error("PUT before durable private artifacts", err)
				w.WriteHeader(500)
				return
			}
			record(r.Header.Get("Idempotency-Key"), c.Payload)
			if drop.Load() {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, r)
				if recorder.Code != 200 {
					t.Error("provision before response loss", recorder.Body.String())
				}
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Error("missing hijacker")
					return
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				return
			}
		}
		handler.ServeHTTP(w, r)
	})
}
