package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/app"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/observerapi"
	"github.com/lllypuk/artel/internal/store"
)

const MaxSnapshot = 32 * 1024 * 1024

type server struct {
	ctx   context.Context
	store *store.Store
	app   *app.Service
}

// New routes the domain API. The caller must wrap it with Protect.
func New(ctx context.Context, s *store.Store) http.Handler {
	a := &server{ctx: ctx, store: s, app: app.New(s)}
	mux := http.NewServeMux()
	observerapi.MountOwner(mux, s)
	MountDeliveryOwner(mux, s)
	mountOwnerQuestions(mux, s)
	mountTaskResults(mux, s)
	mux.Handle("GET /healthz", Handler())
	mux.HandleFunc("GET /v1/snapshot", a.snapshot)
	mux.HandleFunc("GET /v1/events", a.history)
	mux.HandleFunc("GET /v1/stream", a.stream)
	for pattern, action := range map[string]string{
		"POST /v1/projects":              app.CreateProject,
		"PATCH /v1/projects/{id}":        app.EditProject,
		"POST /v1/projects/{id}/tasks":   app.CreateTask,
		"PATCH /v1/tasks/{id}":           app.EditTask,
		"POST /v1/tasks/{id}/transition": app.Transition,
		"PUT /v1/projects/{id}/queue":    app.ReplaceQueue,
	} {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) { a.mutate(w, r, action) })
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { Error(w, 404, "not_found", "endpoint not found") })
	return mux
}

func publicError(w http.ResponseWriter, err error) {
	var e *domain.Error
	if !errors.As(err, &e) {
		Error(w, 500, "internal_error", "local API operation failed")
		return
	}
	status := 400
	switch e.Code {
	case "not_found":
		status = 404
	case "conflict", "reset_required":
		status = 409
	}
	data, _ := json.Marshal(e)
	send(w, status, data)
}
func send(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// #nosec G705 -- JSON-encoded bytes, application/json and nosniff; never HTML.
	_, _ = w.Write(data)
}
func (a *server) snapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := a.store.Snapshot(r.Context())
	if err != nil {
		publicError(w, err)
		return
	}
	data, err := json.Marshal(wireSnapshot(snap))
	if err != nil {
		publicError(w, err)
		return
	}
	sendSnapshot(w, data)
}
func sendSnapshot(w http.ResponseWriter, data []byte) {
	if len(data) > MaxSnapshot {
		Error(w, 413, "snapshot_too_large", "snapshot exceeds limit")
		return
	}
	send(w, 200, data)
}

// Decode the operation-specific object once, rejecting null, duplicate fields,
// trailing values, unknown fields and invalid UTF-8 before app validation.
func decodeCommand(w http.ResponseWriter, r *http.Request, action string) (app.Command, error) {
	c := app.Command{Action: action}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		return c, &bodyLimitError{}
	}
	if !utf8.Valid(data) {
		return c, domain.Invalid("JSON must be UTF-8")
	}
	fields, err := decodeObject(data)
	if err != nil {
		return c, err
	}
	for name, value := range fields {
		target, ok := commandFields(&c)[name]
		if !ok {
			return c, domain.Invalid("unknown JSON field")
		}
		if err = json.Unmarshal(value, target); err != nil {
			return c, domain.Invalid("invalid field type")
		}
	}
	return c, validateCommand(c, fields)
}

func decodeObject(data []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, domain.Invalid("JSON object required")
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			return nil, domain.Invalid("invalid JSON")
		}
		name, ok := tok.(string)
		if !ok {
			return nil, domain.Invalid("invalid JSON")
		}
		if _, ok = fields[name]; ok {
			return nil, domain.Invalid("duplicate JSON field")
		}
		var value json.RawMessage
		if err = dec.Decode(&value); err != nil || bytes.Equal(value, []byte("null")) {
			return nil, domain.Invalid("invalid or null field")
		}
		fields[name] = value
	}
	if _, err = dec.Token(); err != nil {
		return nil, domain.Invalid("invalid JSON")
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, domain.Invalid("trailing JSON")
	}
	return fields, nil
}

func commandFields(c *app.Command) map[string]any {
	allowed := map[string]any{}
	switch c.Action {
	case app.CreateProject:
		allowed = map[string]any{"name": &c.Name, "root_path": &c.RootPath, "description": &c.Description}
	case app.EditProject:
		allowed = map[string]any{"name": &c.Name, "root_path": &c.RootPath, "description": &c.Description, "archived": &c.Archived, "expected_version": &c.ExpectedVersion}
	case app.CreateTask:
		allowed = map[string]any{"title": &c.Title, "description": &c.Description, "acceptance_criteria": &c.AcceptanceCriteria}
	case app.EditTask:
		allowed = map[string]any{"title": &c.Title, "description": &c.Description, "acceptance_criteria": &c.AcceptanceCriteria, "expected_version": &c.ExpectedVersion}
	case app.Transition:
		allowed = map[string]any{"state": &c.State, "expected_version": &c.ExpectedVersion, "expected_queue_version": &c.ExpectedQueueVersion, "expected_result_id": &c.ExpectedResultID}
	case app.ReplaceQueue:
		allowed = map[string]any{"task_ids": &c.TaskIDs, "expected_queue_version": &c.ExpectedQueueVersion}
	}
	return allowed
}

type bodyLimitError struct{}

func (*bodyLimitError) Error() string { return "request exceeds limit" }

func validateCommand(c app.Command, fields map[string]json.RawMessage) error {
	if (c.Action == app.EditProject || c.Action == app.EditTask || c.Action == app.Transition) && c.ExpectedVersion < 1 {
		return domain.Invalid("expected_version required")
	}
	if _, ok := fields["expected_queue_version"]; ok && c.ExpectedQueueVersion < 1 {
		return domain.Invalid("invalid expected_queue_version")
	}
	if c.Action == app.ReplaceQueue && (c.ExpectedQueueVersion < 1 || c.TaskIDs == nil) {
		return domain.Invalid("task_ids and expected_queue_version required")
	}
	if c.Action == app.Transition {
		return validateTransitionCommand(c)
	}
	for _, id := range c.TaskIDs {
		if !validUUID(id) {
			return domain.Invalid("invalid task UUID")
		}
	}
	return nil
}
func validateTransitionCommand(c app.Command) error {
	if c.State != domain.Backlog && c.State != domain.InProgress && c.State != domain.Done && c.State != domain.Cancelled {
		return domain.Invalid("invalid target state")
	}
	if c.ExpectedResultID != nil && *c.ExpectedResultID != "" && !validUUID(*c.ExpectedResultID) {
		return domain.Invalid("invalid expected_result_id")
	}
	return nil
}

func validUUID(id string) bool { u, err := uuid.Parse(id); return err == nil && u.String() == id }
func (a *server) mutate(w http.ResponseWriter, r *http.Request, action string) {
	c, err := decodeCommand(w, r, action)
	if err != nil {
		var limit *bodyLimitError
		if errors.As(err, &limit) {
			Error(w, 413, "body_too_large", "request exceeds limit")
		} else {
			publicError(w, err)
		}
		return
	}
	if id := r.PathValue("id"); id != "" {
		if !validUUID(id) {
			publicError(w, domain.Invalid("invalid object UUID"))
			return
		}
		if action == app.EditTask || action == app.Transition {
			c.TaskID = id
		} else {
			c.ProjectID = id
		}
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 {
		publicError(w, domain.Invalid("one Idempotency-Key required"))
		return
	}
	result, err := a.app.Execute(r.Context(), r.Header.Get("Idempotency-Key"), r.Method, r.URL.Path, c)
	if err != nil {
		publicError(w, err)
		return
	}
	send(w, result.Status, result.Body)
}

type cursor struct {
	instance      string
	after         int64
	limit         int
	project, task string
}

func parseCursor(r *http.Request, stream bool) (cursor, error) {
	c := cursor{limit: 100}
	q := r.URL.Query()
	allowed := map[string]bool{"instance_id": true, "after": true}
	if !stream {
		allowed["limit"] = true
		allowed["project_id"] = true
		allowed["task_id"] = true
	}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 {
			return c, domain.Invalid("invalid query parameter")
		}
	}
	c.instance = q.Get("instance_id")
	c.project = q.Get("project_id")
	c.task = q.Get("task_id")
	if !validUUID(c.instance) || (c.project != "" && !validUUID(c.project)) || (c.task != "" && !validUUID(c.task)) {
		return c, domain.Invalid("invalid instance or filter UUID")
	}
	var err error
	c.after, err = strconv.ParseInt(q.Get("after"), 10, 64)
	if err != nil || c.after < 0 {
		return c, domain.Invalid("invalid event cursor")
	}
	if v := q.Get("limit"); v != "" {
		c.limit, err = strconv.Atoi(v)
		if err != nil || c.limit < 1 || c.limit > 500 {
			return c, domain.Invalid("invalid event limit")
		}
	}
	// Never accept credentials in query parameters; SSE uses Bearer headers.
	if strings.Contains(r.URL.RawQuery, ";") {
		return c, domain.Invalid("invalid query")
	}
	return c, nil
}
func (a *server) history(w http.ResponseWriter, r *http.Request) {
	c, err := parseCursor(r, false)
	if err != nil {
		publicError(w, err)
		return
	}
	page, err := a.store.History(r.Context(), c.instance, c.after, c.limit, c.project, c.task)
	if err != nil {
		publicError(w, err)
		return
	}
	data, err := json.Marshal(wireHistory(page))
	if err != nil {
		publicError(w, err)
		return
	}
	send(w, 200, data)
}
