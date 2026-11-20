package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lllypuk/artel/internal/store"
)

// deliveryError maps store observer-style codes onto public owner API errors.
func deliveryError(w http.ResponseWriter, err error) {
	var e *store.ObserverError
	if !errors.As(err, &e) {
		Error(w, 500, "internal_error", "local API operation failed")
		return
	}
	status := 409
	switch e.Code {
	case "invalid_request":
		status = 400
	case "unauthorized":
		status = 401
	case "not_found":
		status = 404
	}
	Error(w, status, e.Code, "delivery operation rejected")
}

// MountDeliveryOwner adds owner message/assignment routes; Protect applies owner auth.
func MountDeliveryOwner(mux *http.ServeMux, s *store.Store) {
	mux.HandleFunc("POST /v1/tasks/{task_id}/messages", func(w http.ResponseWriter, r *http.Request) {
		appendTaskMessage(w, r, s)
	})
	mux.HandleFunc("GET /v1/tasks/{task_id}/messages", func(w http.ResponseWriter, r *http.Request) {
		listTaskMessages(w, r, s)
	})
	mux.HandleFunc("GET /v1/tasks/{task_id}/delivery", func(w http.ResponseWriter, r *http.Request) {
		taskDeliveryProjection(w, r, s)
	})
	mux.HandleFunc("PUT /v1/observers/{observer_id}/delivery", func(w http.ResponseWriter, r *http.Request) {
		enableDelivery(w, r, s)
	})
	mux.HandleFunc("POST /v1/observers/{observer_id}/delivery/revoke", func(w http.ResponseWriter, r *http.Request) {
		revokeDelivery(w, r, s)
	})
	mux.HandleFunc("GET /v1/observers/{observer_id}/delivery/{delivery_binding_id}", func(w http.ResponseWriter, r *http.Request) {
		deliveryBindingStatus(w, r, s)
	})
}

func appendTaskMessage(w http.ResponseWriter, r *http.Request, s *store.Store) {
	taskID := r.PathValue("task_id")
	if !validUUID(taskID) {
		Error(w, 400, "invalid_request", "invalid task UUID")
		return
	}
	var body struct {
		Text            string `json:"text"`
		DeliveryBinding string `json:"delivery_binding_id"`
		InstanceID      string `json:"instance_id"`
	}
	if !decodeDeliveryJSON(w, r, &body, []string{"text", "delivery_binding_id", "instance_id"}) {
		return
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 || !validUUID(r.Header.Get("Idempotency-Key")) {
		Error(w, 400, "invalid_request", "one Idempotency-Key required")
		return
	}
	result, err := s.AppendDeliveryMessage(r.Context(), taskID, store.DeliveryAppend{
		Key: r.Header.Get("Idempotency-Key"), Text: body.Text, BindingID: body.DeliveryBinding, InstanceID: body.InstanceID,
	})
	if err != nil {
		deliveryError(w, err)
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		Error(w, 500, "internal_error", "local API operation failed")
		return
	}
	send(w, 200, data)
}

func listTaskMessages(w http.ResponseWriter, r *http.Request, s *store.Store) {
	taskID := r.PathValue("task_id")
	if !validUUID(taskID) {
		Error(w, 400, "invalid_request", "invalid task UUID")
		return
	}
	q := r.URL.Query()
	after := int64(0)
	if v := q.Get("after"); v != "" {
		var err error
		after, err = strconv.ParseInt(v, 10, 64)
		if err != nil || after < 0 {
			Error(w, 400, "invalid_request", "invalid cursor")
			return
		}
	}
	limit := 0
	if v := q.Get("limit"); v != "" {
		var convErr error
		limit, convErr = strconv.Atoi(v)
		if convErr != nil || limit < 1 {
			Error(w, 400, "invalid_request", "invalid limit")
			return
		}
	}
	var ids []string
	if v := q.Get("ids"); v != "" {
		for _, id := range strings.Split(v, ",") {
			if !validUUID(id) {
				Error(w, 400, "invalid_request", "invalid message UUID")
				return
			}
			ids = append(ids, id)
		}
	}
	messages, next, err := s.ListDeliveryMessages(r.Context(), taskID, after, limit, ids)
	if err != nil {
		deliveryError(w, err)
		return
	}
	data, err := json.Marshal(map[string]any{"messages": messages, "next_after": next})
	if err != nil {
		Error(w, 500, "internal_error", "local API operation failed")
		return
	}
	send(w, 200, data)
}

func taskDeliveryProjection(w http.ResponseWriter, r *http.Request, s *store.Store) {
	taskID := r.PathValue("task_id")
	if !validUUID(taskID) {
		Error(w, 400, "invalid_request", "invalid task UUID")
		return
	}
	projection, err := s.DeliveryTaskProjection(r.Context(), taskID)
	if err != nil {
		deliveryError(w, err)
		return
	}
	data, err := json.Marshal(projection)
	if err != nil {
		Error(w, 500, "internal_error", "local API operation failed")
		return
	}
	send(w, 200, data)
}

func enableDelivery(w http.ResponseWriter, r *http.Request, s *store.Store) {
	var body struct {
		ExpectedVersion *int64 `json:"expected_version"`
		BindingID       string `json:"delivery_binding_id"`
		JournalID       string `json:"journal_id"`
		Verifier        string `json:"verifier"`
		RuntimeID       string `json:"runtime_id"`
		ProjectID       string `json:"project_id"`
		PiSessionID     string `json:"pi_session_id"`
		InstanceID      string `json:"instance_id"`
	}
	if !decodeDeliveryJSON(w, r, &body, []string{"expected_version", "delivery_binding_id", "journal_id", "verifier", "runtime_id", "project_id", "pi_session_id", "instance_id"}) {
		return
	}
	if body.ExpectedVersion == nil {
		Error(w, 400, "invalid_request", "expected_version required")
		return
	}
	result, err := s.MutateDeliveryBinding(r.Context(), store.DeliveryOwnerMutation{
		ObserverID: r.PathValue("observer_id"), Key: r.Header.Get("Idempotency-Key"), Action: "enable",
		ExpectedVer: *body.ExpectedVersion, BindingID: body.BindingID, JournalID: body.JournalID,
		Verifier: body.Verifier, RuntimeID: body.RuntimeID, ProjectID: body.ProjectID,
		PiSessionID: body.PiSessionID, InstanceID: body.InstanceID,
	})
	if err != nil {
		deliveryError(w, err)
		return
	}
	data, _ := json.Marshal(result)
	send(w, 200, data)
}

func revokeDelivery(w http.ResponseWriter, r *http.Request, s *store.Store) {
	var body struct {
		BindingID string `json:"delivery_binding_id"`
	}
	if !decodeDeliveryJSON(w, r, &body, []string{"delivery_binding_id"}) {
		return
	}
	result, err := s.MutateDeliveryBinding(r.Context(), store.DeliveryOwnerMutation{
		ObserverID: r.PathValue("observer_id"), Key: r.Header.Get("Idempotency-Key"), Action: "revoke",
		BindingID: body.BindingID,
	})
	if err != nil {
		deliveryError(w, err)
		return
	}
	data, _ := json.Marshal(result)
	send(w, 200, data)
}

func deliveryBindingStatus(w http.ResponseWriter, r *http.Request, s *store.Store) {
	result, err := s.DeliveryBindingStatus(r.Context(), r.PathValue("observer_id"), r.PathValue("delivery_binding_id"))
	if err != nil {
		deliveryError(w, err)
		return
	}
	data, _ := json.Marshal(result)
	send(w, 200, data)
}

// storePublicErr builds a fixed-code error for pre-store validation failures.
func storePublicErr(code string) error { return &store.ObserverError{Code: code} }

func readBody(w http.ResponseWriter, r *http.Request) []byte {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		Error(w, 413, "body_too_large", "request body exceeds limit")
		return nil
	}
	return data
}

// decodeDeliveryJSON rejects unknown fields, nulls, invalid UTF-8 and trailing
// JSON like the observer decode, but reports through the owner API error shape.
func decodeDeliveryJSON(w http.ResponseWriter, r *http.Request, target any, allowed []string) bool {
	data := readBody(w, r)
	if data == nil {
		return false
	}
	if !utf8.Valid(data) {
		deliveryError(w, storePublicErr("invalid_request"))
		return false
	}
	fields, err := decodeObject(data)
	if err != nil {
		deliveryError(w, storePublicErr("invalid_request"))
		return false
	}
	for name := range fields {
		if !slices.Contains(allowed, name) {
			deliveryError(w, storePublicErr("invalid_request"))
			return false
		}
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		deliveryError(w, storePublicErr("invalid_request"))
		return false
	}
	return true
}
