package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// The delivery owner surface is part of the pinned owner contract; responses
// in messages_test.go are validated against this document by checkContract.
func TestDeliveryOpenAPIContract(t *testing.T) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"/v1/tasks/{task_id}/messages",
		"/v1/tasks/{task_id}/delivery",
		"/v1/observers/{observer_id}/delivery",
		"/v1/observers/{observer_id}/delivery/revoke",
		"/v1/observers/{observer_id}/delivery/{delivery_binding_id}",
	}
	for _, path := range paths {
		if doc.Paths.Find(path) == nil {
			t.Fatalf("delivery path missing from owner contract: %s", path)
		}
	}
	for _, operation := range []*openapi3.Operation{
		doc.Paths.Find("/v1/tasks/{task_id}/messages").Post,
		doc.Paths.Find("/v1/observers/{observer_id}/delivery").Put,
		doc.Paths.Find("/v1/observers/{observer_id}/delivery/revoke").Post,
	} {
		parameters := operation.Parameters
		if len(parameters) != 1 || parameters[0].Value.Name != "Idempotency-Key" || !parameters[0].Value.Required {
			t.Fatalf("owner delivery mutation must require exactly one Idempotency-Key: %v", operation.OperationID)
		}
	}
	binding := doc.Components.Schemas["DeliveryBinding"].Value
	for _, field := range []string{"delivery_binding_id", "observer_id", "runtime_id", "project_id", "pi_session_id", "journal_id", "generation", "status", "created_at"} {
		if !strings.Contains(strings.Join(binding.Required, ","), field) {
			t.Fatalf("DeliveryBinding must require %s", field)
		}
	}
	if _, ok := binding.Properties["verifier"]; ok {
		t.Fatal("binding projection must never carry a verifier")
	}
	const id = "11111111-1111-4111-8111-111111111111"
	cases := []struct {
		name, schema, body string
		valid              bool
	}{
		{"enable full", "EnableDelivery", `{"expected_version":1,"delivery_binding_id":"` + id + `","journal_id":"` + id + `","verifier":"` + strings.Repeat("ab", 32) + `","runtime_id":"` + id + `","project_id":"` + id + `","pi_session_id":"` + id + `","instance_id":"` + id + `"}`, true},
		{"enable missing instance", "EnableDelivery", `{"expected_version":1,"delivery_binding_id":"` + id + `","journal_id":"` + id + `","verifier":"` + strings.Repeat("ab", 32) + `","runtime_id":"` + id + `","project_id":"` + id + `","pi_session_id":"` + id + `"}`, false},
		{"enable plaintext verifier", "EnableDelivery", `{"expected_version":1,"delivery_binding_id":"` + id + `","journal_id":"` + id + `","verifier":"plaintext","runtime_id":"` + id + `","project_id":"` + id + `","pi_session_id":"` + id + `","instance_id":"` + id + `"}`, false},
		{"append message", "AppendMessage", `{"text":"commission","delivery_binding_id":"` + id + `","instance_id":"` + id + `"}`, true},
		{"append empty text", "AppendMessage", `{"text":"","delivery_binding_id":"` + id + `","instance_id":"` + id + `"}`, false},
		{"message state", "DeliveryMessage", `{"id":"` + id + `","task_id":"` + id + `","delivery_binding_id":"` + id + `","text":"stored","status":"stored","blocked":false,"created_at":"2026-11-19T00:00:00Z"}`, true},
		{"no unknown state", "DeliveryMessage", `{"id":"` + id + `","task_id":"` + id + `","delivery_binding_id":"` + id + `","text":"stored","status":"delivered","blocked":false,"created_at":"2026-11-19T00:00:00Z"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.body), &value); err != nil {
				t.Fatal(err)
			}
			if valid := doc.Components.Schemas[tc.schema].Value.VisitJSON(value) == nil; valid != tc.valid {
				t.Fatalf("valid=%v", tc.valid)
			}
		})
	}
}
