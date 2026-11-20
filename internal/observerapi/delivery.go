package observerapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/lllypuk/artel/internal/store"
)

// MaxDeliveryBody bounds the bridge JSON response for /delivery/v1/next:
// 8 KiB of text can escape to at most 6 bytes per byte plus bounded metadata.
const MaxDeliveryBody = 65536

type deliveryContextKey struct{}

// deliveryBinding returns the authenticated binding ID pinned by the transport layer.
func deliveryBinding(r *http.Request) string {
	v, _ := r.Context().Value(deliveryContextKey{}).(string)
	return v
}

func mountDelivery(mux *http.ServeMux, s *store.Store) {
	mux.HandleFunc("GET /delivery/v1/context", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.DeliveryContext(r.Context(), deliveryBinding(r))
		reply(w, v, e)
	})
	mux.HandleFunc("POST /delivery/v1/attach", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			WriterID         string `json:"writer_id"`
			ExpectedPrevious string `json:"expected_previous"`
			JournalID        string `json:"journal_id"`
			RuntimeID        string `json:"runtime_id"`
			PiSessionID      string `json:"pi_session_id"`
		}
		if !decode(w, r, &body, []string{"writer_id", "expected_previous", "journal_id", "runtime_id", "pi_session_id"}, "writer_id", "journal_id", "runtime_id", "pi_session_id") {
			return
		}
		e := s.DeliveryAttach(r.Context(), deliveryBinding(r), body.WriterID, body.ExpectedPrevious, body.JournalID, body.RuntimeID, body.PiSessionID)
		reply(w, map[string]string{"status": "attached"}, e)
	})
	mux.HandleFunc("POST /delivery/v1/ready", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			WriterID string `json:"writer_id"`
		}
		if !decode(w, r, &body, []string{"writer_id"}, "writer_id") {
			return
		}
		e := s.DeliveryReady(r.Context(), deliveryBinding(r), body.WriterID)
		reply(w, map[string]string{"status": "ready"}, e)
	})
	mux.HandleFunc("GET /delivery/v1/next", func(w http.ResponseWriter, r *http.Request) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) != 1 || len(query["writer_id"]) != 1 {
			failure(w, "invalid_request")
			return
		}
		message, receiptKey, e := s.DeliveryNext(r.Context(), deliveryBinding(r), query.Get("writer_id"))
		if e != nil {
			reply(w, nil, e)
			return
		}
		if message.ID == "" {
			reply(w, map[string]any{"message": nil}, nil)
			return
		}
		data, err := json.Marshal(map[string]any{"message": message, "receipt_key": receiptKey})
		if err != nil || len(data) > MaxDeliveryBody {
			failure(w, "unavailable")
			return
		}
		reply(w, json.RawMessage(data), nil)
	})
	mux.HandleFunc("POST /delivery/v1/spend", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			WriterID  string `json:"writer_id"`
			MessageID string `json:"message_id"`
		}
		if !decode(w, r, &body, []string{"writer_id", "message_id"}, "writer_id", "message_id") {
			return
		}
		v, e := s.DeliverySpend(r.Context(), deliveryBinding(r), body.WriterID, body.MessageID)
		reply(w, v, e)
	})
	mux.HandleFunc("POST /delivery/v1/verify", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			WriterID  string `json:"writer_id"`
			AttemptID string `json:"attempt_id"`
		}
		if !decode(w, r, &body, []string{"writer_id", "attempt_id"}, "writer_id", "attempt_id") {
			return
		}
		v, e := s.DeliveryVerify(r.Context(), deliveryBinding(r), body.WriterID, body.AttemptID)
		reply(w, v, e)
	})
	mux.HandleFunc("POST /delivery/v1/receipt", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key         string         `json:"key"`
			MessageID   string         `json:"message_id"`
			AttemptID   string         `json:"attempt_id"`
			Type        string         `json:"type"`
			MessageType string         `json:"message_type"`
			PiSessionID string         `json:"pi_session_id"`
			Details     map[string]any `json:"details"`
		}
		if !decode(w, r, &body, []string{"key", "message_id", "attempt_id", "type", "message_type", "pi_session_id", "details"},
			"key", "message_id", "attempt_id", "type", "message_type", "pi_session_id", "details") {
			return
		}
		v, e := s.DeliveryReceipt(r.Context(), deliveryBinding(r), body.Key, struct {
			MessageID, AttemptID, Type, MessageType, PiSessionID string
			Details                                              map[string]any
		}{body.MessageID, body.AttemptID, body.Type, body.MessageType, body.PiSessionID, body.Details})
		reply(w, v, e)
	})
}

// authenticateDelivery resolves the delivery-only verifier to its binding ID.
func authenticateDelivery(ctx context.Context, s *store.Store, w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	tx, err := s.AuthenticateDeliveryBinding(ctx, verifier(r))
	if err != nil {
		reply(w, nil, err)
		return nil, false
	}
	return r.WithContext(context.WithValue(r.Context(), deliveryContextKey{}, tx)), true
}

func isDeliveryPath(path string) bool { return strings.HasPrefix(path, "/delivery/v1/") }
