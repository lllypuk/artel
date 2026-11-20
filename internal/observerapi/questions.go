package observerapi

import (
	"net/http"

	"github.com/lllypuk/artel/internal/store"
)

const maxQuestionBody = 32768

func mountDeliveryQuestions(mux *http.ServeMux, s *store.Store) {
	mux.HandleFunc("POST /delivery/v1/questions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			InstanceID string `json:"instance_id"`
			TaskID     string `json:"task_id"`
			WriterID   string `json:"writer_id"`
			Text       string `json:"text"`
		}
		if len(r.Header.Values("Idempotency-Key")) != 1 {
			failure(w, "invalid_request")
			return
		}
		if !decodeLimit(w, r, &body, maxQuestionBody, []string{"instance_id", "task_id", "writer_id", "text"},
			"instance_id", "task_id", "writer_id", "text") {
			return
		}
		result, err := s.CreateOwnerQuestion(r.Context(), deliveryBinding(r), store.OwnerQuestionCreate{
			Key: r.Header.Get("Idempotency-Key"), InstanceID: body.InstanceID,
			TaskID: body.TaskID, WriterID: body.WriterID, Text: body.Text,
		})
		reply(w, result, err)
	})
}
