package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/lllypuk/artel/internal/store"
)

const maxQuestionBody = 32768

func mountOwnerQuestions(mux *http.ServeMux, s *store.Store) {
	mux.HandleFunc("GET /v1/tasks/{task_id}/questions", func(w http.ResponseWriter, r *http.Request) {
		listTaskQuestions(w, r, s)
	})
	mux.HandleFunc("POST /v1/tasks/{task_id}/questions/{question_id}/answer", func(w http.ResponseWriter, r *http.Request) {
		answerTaskQuestion(w, r, s)
	})
}

type questionListQuery struct {
	instance string
	after    int64
	limit    int
	ids      []string
}

func parseQuestionIDs(value string, present bool) ([]string, bool) {
	if !present {
		return nil, true
	}
	ids := strings.Split(value, ",")
	if len(ids) == 0 || len(ids) > 5 {
		return nil, false
	}
	for _, id := range ids {
		if !validUUID(id) {
			return nil, false
		}
	}
	return ids, true
}

func parseQuestionListQuery(r *http.Request) (questionListQuery, bool) {
	var parsed questionListQuery
	query := r.URL.Query()
	for name, values := range query {
		if (name != "instance_id" && name != "after" && name != "limit" && name != "ids") || len(values) != 1 {
			return parsed, false
		}
	}
	parsed.instance = query.Get("instance_id")
	if !validUUID(parsed.instance) || (query.Has("ids") && (query.Has("after") || query.Has("limit"))) {
		return parsed, false
	}
	var err error
	if query.Has("after") {
		parsed.after, err = strconv.ParseInt(query.Get("after"), 10, 64)
		if err != nil || parsed.after < 0 {
			return parsed, false
		}
	}
	if query.Has("limit") {
		parsed.limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || parsed.limit < 1 || parsed.limit > 5 {
			return parsed, false
		}
	}
	ids, ok := parseQuestionIDs(query.Get("ids"), query.Has("ids"))
	parsed.ids = ids
	return parsed, ok
}

func listTaskQuestions(w http.ResponseWriter, r *http.Request, s *store.Store) {
	taskID := r.PathValue("task_id")
	parsed, ok := parseQuestionListQuery(r)
	if !validUUID(taskID) || !ok {
		Error(w, 400, "invalid_request", "invalid question query")
		return
	}
	questions, next, err := s.ListOwnerQuestions(r.Context(), taskID, parsed.instance, parsed.after, parsed.limit, parsed.ids)
	if err != nil {
		deliveryError(w, err)
		return
	}
	data, err := json.Marshal(map[string]any{"questions": questions, "next_after": next})
	if err != nil || len(data) > 320*1024 {
		Error(w, 500, "internal_error", "local API operation failed")
		return
	}
	send(w, 200, data)
}

func answerTaskQuestion(w http.ResponseWriter, r *http.Request, s *store.Store) {
	taskID, questionID := r.PathValue("task_id"), r.PathValue("question_id")
	if !validUUID(taskID) || !validUUID(questionID) || len(r.Header.Values("Idempotency-Key")) != 1 || !validUUID(r.Header.Get("Idempotency-Key")) {
		Error(w, 400, "invalid_request", "valid task, question and Idempotency-Key UUIDs required")
		return
	}
	var body struct {
		InstanceID string `json:"instance_id"`
		Text       string `json:"text"`
	}
	if !decodeDeliveryJSONLimit(w, r, &body, maxQuestionBody, []string{"instance_id", "text"}) {
		return
	}
	result, err := s.AnswerOwnerQuestion(r.Context(), taskID, questionID, store.OwnerQuestionAnswer{
		Key: r.Header.Get("Idempotency-Key"), InstanceID: body.InstanceID, Text: body.Text,
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
