package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/store"
)

func mountTaskResults(mux *http.ServeMux, s *store.Store) {
	mux.HandleFunc("POST /v1/tasks/{task_id}/results", func(w http.ResponseWriter, r *http.Request) {
		submitTaskResult(w, r, s)
	})
	mux.HandleFunc("GET /v1/tasks/{task_id}/results", func(w http.ResponseWriter, r *http.Request) {
		listTaskResults(w, r, s)
	})
	mux.HandleFunc("GET /v1/tasks/{task_id}/results/current", func(w http.ResponseWriter, r *http.Request) {
		currentTaskResult(w, r, s)
	})
	mux.HandleFunc("GET /v1/tasks/{task_id}/results/current/reviews", func(w http.ResponseWriter, r *http.Request) {
		currentTaskReviews(w, r, s)
	})
	mux.HandleFunc("POST /v1/tasks/{task_id}/results/{result_id}/reviews", func(w http.ResponseWriter, r *http.Request) {
		attachTaskReview(w, r, s)
	})
}

func validateResultNestedObjects(fields map[string]json.RawMessage) error {
	schemas := []struct {
		name     string
		allowed  []string
		required []string
	}{
		{name: "required_sources", allowed: []string{"name", "executor"}, required: []string{"name", "executor"}},
		{name: "sources", allowed: []string{"name", "executor", "required", "responded", "degraded", "findings", "pre_existing", "open_questions"},
			required: []string{"name", "executor", "required", "responded", "degraded", "findings", "pre_existing", "open_questions"}},
	}
	for _, schema := range schemas {
		raw, ok := fields[schema.name]
		if !ok {
			continue
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return domain.Invalid("invalid field type")
		}
		for _, value := range values {
			nested, err := decodeObject(value)
			if err != nil {
				return err
			}
			for name := range nested {
				if !slices.Contains(schema.allowed, name) {
					return domain.Invalid("unknown JSON field")
				}
			}
			for _, name := range schema.required {
				if _, ok := nested[name]; !ok {
					return domain.Invalid("required JSON field missing")
				}
			}
		}
	}
	return nil
}

// decodeResultJSON applies the owner API's strict JSON rules to normalized
// result payloads, including nulls, nested aliases and required object fields.
func decodeResultJSON(w http.ResponseWriter, r *http.Request, target any, allowed, required []string) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		Error(w, 413, "body_too_large", "request exceeds limit")
		return false
	}
	if !utf8.Valid(data) {
		publicError(w, domain.Invalid("JSON must be UTF-8"))
		return false
	}
	fields, err := decodeObject(data)
	if err != nil {
		publicError(w, err)
		return false
	}
	for name := range fields {
		if !slices.Contains(allowed, name) {
			publicError(w, domain.Invalid("unknown JSON field"))
			return false
		}
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			publicError(w, domain.Invalid("required JSON field missing"))
			return false
		}
	}
	if err := validateResultNestedObjects(fields); err != nil {
		publicError(w, err)
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		publicError(w, domain.Invalid("invalid field type"))
		return false
	}
	return true
}

func resultMutation(r *http.Request) (store.Mutation, bool) {
	if len(r.Header.Values("Idempotency-Key")) != 1 || !validUUID(r.Header.Get("Idempotency-Key")) {
		return store.Mutation{}, false
	}
	return store.Mutation{Key: r.Header.Get("Idempotency-Key"), Method: r.Method, Path: r.URL.Path}, true
}

func submitTaskResult(w http.ResponseWriter, r *http.Request, s *store.Store) {
	taskID := r.PathValue("task_id")
	mutation, ok := resultMutation(r)
	if !validUUID(taskID) || !ok {
		publicError(w, domain.Invalid("valid task and Idempotency-Key UUIDs required"))
		return
	}
	var body store.SubmitResultInput
	if !decodeResultJSON(w, r, &body,
		[]string{"repo", "base", "head", "summary", "expected_result_id"},
		[]string{"repo", "base", "head", "expected_result_id"}) {
		return
	}
	result, err := s.SubmitResult(r.Context(), taskID, mutation, body)
	if err != nil {
		publicError(w, err)
		return
	}
	send(w, result.Status, result.Body)
}

func attachTaskReview(w http.ResponseWriter, r *http.Request, s *store.Store) {
	taskID, resultID := r.PathValue("task_id"), r.PathValue("result_id")
	mutation, ok := resultMutation(r)
	if !validUUID(taskID) || !validUUID(resultID) || !ok {
		publicError(w, domain.Invalid("valid task, result and Idempotency-Key UUIDs required"))
		return
	}
	var body store.AttachReviewInput
	allowed := []string{"repo", "base", "head", "revmux_task", "run", "range_source", "report_ref", "complete",
		"required_sources", "sources", "incomplete_reasons", "findings", "pre_existing", "open_questions"}
	if !decodeResultJSON(w, r, &body, allowed, allowed) {
		return
	}
	result, err := s.AttachReview(r.Context(), taskID, resultID, mutation, body)
	if err != nil {
		publicError(w, err)
		return
	}
	send(w, result.Status, result.Body)
}

func parseResultHistoryQuery(r *http.Request) (int64, int, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, 0, domain.Invalid("invalid query")
	}
	for name, values := range query {
		if (name != "after" && name != "limit") || len(values) != 1 {
			return 0, 0, domain.Invalid("invalid query parameter")
		}
	}
	var after int64
	limit := 50
	if query.Has("after") {
		after, err = strconv.ParseInt(query.Get("after"), 10, 64)
		if err != nil || after < 0 {
			return 0, 0, domain.Invalid("invalid result cursor")
		}
	}
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 100 {
			return 0, 0, domain.Invalid("invalid result limit")
		}
	}
	return after, limit, nil
}

func currentTaskResult(w http.ResponseWriter, r *http.Request, s *store.Store) {
	taskID := r.PathValue("task_id")
	if !validUUID(taskID) || r.URL.RawQuery != "" {
		publicError(w, domain.Invalid("invalid current result query"))
		return
	}
	current, err := s.CurrentTaskResult(r.Context(), taskID)
	if err != nil {
		publicError(w, err)
		return
	}
	data, err := json.Marshal(current)
	if err != nil {
		publicError(w, err)
		return
	}
	send(w, 200, data)
}

func parseCurrentReviewQuery(r *http.Request) (string, int, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", 0, domain.Invalid("invalid query")
	}
	for name, values := range query {
		if (name != "after" && name != "limit") || len(values) != 1 {
			return "", 0, domain.Invalid("invalid query parameter")
		}
	}
	after := query.Get("after")
	if after != "" && !validUUID(after) {
		return "", 0, domain.Invalid("invalid review cursor")
	}
	limit := 20
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 50 {
			return "", 0, domain.Invalid("invalid review limit")
		}
	}
	return after, limit, nil
}

func currentTaskReviews(w http.ResponseWriter, r *http.Request, s *store.Store) {
	taskID := r.PathValue("task_id")
	after, limit, err := parseCurrentReviewQuery(r)
	if !validUUID(taskID) || err != nil {
		if err == nil {
			err = domain.Invalid("invalid task UUID")
		}
		publicError(w, err)
		return
	}
	page, err := s.CurrentTaskReviews(r.Context(), taskID, after, limit)
	if err != nil {
		publicError(w, err)
		return
	}
	data, err := json.Marshal(page)
	if err != nil {
		publicError(w, err)
		return
	}
	send(w, 200, data)
}

func listTaskResults(w http.ResponseWriter, r *http.Request, s *store.Store) {
	taskID := r.PathValue("task_id")
	after, limit, err := parseResultHistoryQuery(r)
	if !validUUID(taskID) || err != nil {
		if err == nil {
			err = domain.Invalid("invalid task UUID")
		}
		publicError(w, err)
		return
	}
	page, err := s.TaskResultHistory(r.Context(), taskID, after, limit)
	if err != nil {
		publicError(w, err)
		return
	}
	data, err := json.Marshal(page)
	if err != nil {
		publicError(w, err)
		return
	}
	send(w, 200, data)
}
