// Package observerapi implements the isolated observer surface and owner controls.
package observerapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lllypuk/artel/internal/store"
)

const MaxBody = 16384

func reply(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	status := 200
	if err != nil {
		code, capacity := "unavailable", ""
		var e *store.ObserverError
		if errors.As(err, &e) {
			code, capacity = e.Code, e.Capacity
		}
		status = 409
		switch code {
		case "invalid_request", "unsafe_host", "origin_forbidden":
			status = 400
		case "unauthorized":
			status = 401
		case "not_found":
			status = 404
		case "body_too_large":
			status = 413
		case "rate_limited":
			status = 429
		case "unavailable":
			status = 503
		}
		body := map[string]string{"error": code}
		if capacity != "" {
			body["capacity"] = capacity
		}
		value = body
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func failure(w http.ResponseWriter, code string) { reply(w, nil, &store.ObserverError{Code: code}) }

// Reject duplicate keys at every depth, null root, unknown and missing fields.
func decode(w http.ResponseWriter, r *http.Request, target any, allowed []string, required ...string) bool {
	return decodeLimit(w, r, target, MaxBody, allowed, required...)
}

func decodeLimit(w http.ResponseWriter, r *http.Request, target any, limit int64, allowed []string, required ...string) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		failure(w, "body_too_large")
		return false
	}
	var fields map[string]json.RawMessage
	if !utf8.Valid(data) || json.Unmarshal(data, &fields) != nil || fields == nil {
		failure(w, "invalid_request")
		return false
	}
	for name, value := range fields {
		// encoding/json also matches case-folded struct field names. Check the
		// exact operation contract before decoding so aliases cannot replace CAS.
		if !slices.Contains(allowed, name) {
			failure(w, "invalid_request")
			return false
		}
		if name == "terminal_hint" && !validTerminalKeys(value) {
			failure(w, "invalid_request")
			return false
		}
		if (name == "provider" || name == "model") && bytes.Equal(bytes.TrimSpace(value), []byte(`""`)) {
			failure(w, "invalid_request")
			return false
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) && name != "runtime_id" && name != "project_id" {
			failure(w, "invalid_request")
			return false
		}
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			failure(w, "invalid_request")
			return false
		}
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if unique(d) != nil {
		failure(w, "invalid_request")
		return false
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		failure(w, "invalid_request")
		return false
	}
	return true
}
func validTerminalKeys(value json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(value, &fields) != nil || len(fields) != 3 {
		return false
	}
	for _, name := range []string{"window_id", "session_id", "pane_id"} {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	return true
}
func unique(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return errors.New("duplicate key")
			}
			seen[s] = true
		}
		if err := unique(d); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

type limiter struct {
	sync.Mutex
	times map[string][]time.Time
}

func (l *limiter) allow(key string) bool {
	l.Lock()
	defer l.Unlock()
	now := time.Now()
	for k, v := range l.times {
		if len(v) == 0 || now.Sub(v[len(v)-1]) >= time.Second {
			delete(l.times, k)
		}
	}
	v := l.times[key]
	live := v[:0]
	for _, t := range v {
		if now.Sub(t) < time.Second {
			live = append(live, t)
		}
	}
	if len(live) >= 4 || len(l.times) >= 2048 && len(live) == 0 {
		return false
	}
	l.times[key] = append(live, now)
	return true
}

// New must only be served on the private Unix listener, never the owner listener.
func New(s *store.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { failure(w, "not_found") })
	rate := &limiter{times: map[string][]time.Time{}}
	mountDelivery(mux, s)
	mountDeliveryQuestions(mux, s)
	mux.HandleFunc("GET /observer/v1/context", func(w http.ResponseWriter, r *http.Request) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) != 1 || len(query["runtime_id"]) != 1 {
			failure(w, "invalid_request")
			return
		}
		v, e := s.ObserverContext(r.Context(), verifier(r), query.Get("runtime_id"))
		reply(w, v, e)
	})
	mux.HandleFunc("POST /observer/v1/register", func(w http.ResponseWriter, r *http.Request) {
		var body store.ObserverRegistration
		if !decode(w, r, &body, []string{"runtime_id", "pi_session_id", "reason", "pi_version", "bridge_version", "cwd", "provider", "model", "terminal_hint"}, "runtime_id", "pi_session_id", "reason", "pi_version", "bridge_version", "cwd") {
			return
		}
		v, e := s.RegisterObserver(r.Context(), verifier(r), body)
		reply(w, v, e)
	})
	mux.HandleFunc("PUT /observer/v1/report", func(w http.ResponseWriter, r *http.Request) {
		var body store.ObserverReport
		if !decode(w, r, &body, []string{"runtime_id", "generation", "report_seq", "activity", "tool_count", "lifecycle", "provider", "model"}, "runtime_id", "generation", "report_seq", "activity", "tool_count", "lifecycle") {
			return
		}
		c, e := s.ObserverContext(r.Context(), verifier(r), body.RuntimeID)
		if e != nil {
			reply(w, nil, e)
			return
		}
		if !rate.allow(c.ObserverID + "/" + body.RuntimeID) {
			failure(w, "rate_limited")
			return
		}
		v, e := s.ReportObserver(r.Context(), verifier(r), body)
		reply(w, v, e)
	})
	return protect(s, mux)
}

func protect(s *store.Store, mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "artel-observer" || r.URL.IsAbs() {
			failure(w, "unsafe_host")
			return
		}
		if _, ok := r.Header["Origin"]; ok {
			failure(w, "origin_forbidden")
			return
		}
		if isDeliveryPath(r.URL.Path) {
			var ok bool
			r, ok = authenticateDelivery(r.Context(), s, w, r)
			if !ok {
				return
			}
		} else if e := s.AuthenticateObserver(r.Context(), verifier(r)); e != nil {
			reply(w, nil, e)
			return
		}
		bodyLimit := int64(MaxBody)
		if r.URL.Path == "/delivery/v1/questions" {
			bodyLimit = maxQuestionBody
		}
		data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, bodyLimit))
		if e != nil {
			failure(w, "body_too_large")
			return
		}
		if r.Method == "GET" && len(data) != 0 {
			failure(w, "invalid_request")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		if r.Method != "GET" && r.Header.Get("Content-Type") != "application/json" || r.Method != "GET" && r.URL.RawQuery != "" {
			failure(w, "invalid_request")
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /observer/v1/context", "POST /observer/v1/register", "PUT /observer/v1/report",
			"GET /delivery/v1/context", "GET /delivery/v1/next",
			"POST /delivery/v1/attach", "POST /delivery/v1/ready", "POST /delivery/v1/spend",
			"POST /delivery/v1/verify", "POST /delivery/v1/receipt", "POST /delivery/v1/questions":
			mux.ServeHTTP(w, r)
		default:
			failure(w, "not_found")
		}
	})
}
func verifier(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return ""
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	b, err := hex.DecodeString(token)
	if err != nil || len(b) != 32 || strings.ToLower(token) != token {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MountOwner adds only owner operations; the caller must apply owner authentication.
func MountOwner(mux *http.ServeMux, s *store.Store) {
	mux.HandleFunc("GET /v1/observers", func(w http.ResponseWriter, r *http.Request) { v, e := s.Observers(r.Context()); reply(w, v, e) })
	for pattern, action := range map[string]string{"PUT /v1/observers/{observer_id}": "provision", "PATCH /v1/observers/{observer_id}/binding": "bind", "POST /v1/observers/{observer_id}/revoke": "revoke", "POST /v1/observers/{observer_id}/prune": "prune"} {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			m := store.ObserverOwnerMutation{ObserverID: r.PathValue("observer_id"), Action: action, Key: r.Header.Get("Idempotency-Key")}
			switch action {
			case "provision":
				var b struct {
					Label    string `json:"label"`
					Verifier string `json:"verifier"`
				}
				if !decode(w, r, &b, []string{"label", "verifier"}, "label", "verifier") {
					return
				}
				m.Label, m.Verifier = b.Label, b.Verifier
			case "bind":
				var b struct {
					ExpectedVersion *int64  `json:"expected_version"`
					RuntimeID       *string `json:"runtime_id"`
					ProjectID       *string `json:"project_id"`
				}
				if !decode(w, r, &b, []string{"expected_version", "runtime_id", "project_id"}, "expected_version", "runtime_id", "project_id") {
					return
				}
				if b.ExpectedVersion == nil || (b.RuntimeID == nil) != (b.ProjectID == nil) {
					failure(w, "invalid_request")
					return
				}
				m.ExpectedVersion = *b.ExpectedVersion
				if b.RuntimeID != nil {
					if *b.RuntimeID == "" || *b.ProjectID == "" {
						failure(w, "invalid_request")
						return
					}
					m.RuntimeID, m.ProjectID = *b.RuntimeID, *b.ProjectID
				}
			case "prune":
				var b struct {
					ExpectedVersion int64    `json:"expected_version"`
					RuntimeIDs      []string `json:"expected_runtime_ids"`
				}
				fields := []string{"expected_version", "expected_runtime_ids"}
				if !decode(w, r, &b, fields, fields...) {
					return
				}
				m.ExpectedVersion, m.ExpectedRuntimeIDs = b.ExpectedVersion, b.RuntimeIDs
			default:
				var b struct {
					ExpectedVersion *int64 `json:"expected_version"`
				}
				if !decode(w, r, &b, []string{"expected_version"}, "expected_version") {
					return
				}
				if b.ExpectedVersion == nil {
					failure(w, "invalid_request")
					return
				}
				m.ExpectedVersion = *b.ExpectedVersion
			}
			v, e := s.MutateObserver(r.Context(), m)
			reply(w, v, e)
		})
	}
}
