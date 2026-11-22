package api

import (
	"bytes"
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
)

var contractOnce sync.Once
var contractDoc *openapi3.T
var contractErr error

func contract(t *testing.T) *openapi3.T {
	t.Helper()
	contractOnce.Do(func() {
		loader := openapi3.NewLoader()
		loader.IsExternalRefsAllowed = true
		contractDoc, contractErr = loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
		if contractErr == nil {
			contractErr = contractDoc.Validate(context.Background())
		}
	})
	if contractErr != nil {
		t.Fatal(contractErr)
	}
	return contractDoc
}

func TestOpenAPIValid(t *testing.T) {
	_, h := fixture(t)
	request(t, h, "GET", "/healthz", "", "", 200)
	doc := contract(t)
	count := 0
	for _, p := range doc.Paths.Map() {
		count += len(p.Operations())
	}
	if count != 31 {
		t.Fatalf("operations: %d", count)
	}
}

func TestWorkTypeOpenAPISchema(t *testing.T) {
	doc := contract(t)
	snapshot := doc.Components.Schemas["Snapshot"].Value
	if !slices.Contains(snapshot.Required, "work_types_version") || snapshot.Properties["work_types_version"] == nil {
		t.Fatal("snapshot does not advertise work_types_version")
	}
	task := doc.Components.Schemas["Task"].Value
	if !slices.Contains(task.Required, "kind") || task.Properties["summary"] == nil || task.Properties["parent_id"] == nil || task.Properties["fix_evidence"] == nil {
		t.Fatal("wire Task lacks work-type projection fields")
	}
	if task.Discriminator == nil || task.Discriminator.PropertyName != "kind" || len(task.OneOf) != 3 {
		t.Fatal("wire Task is not discriminated into Epic, Task and Bug shapes")
	}
	workRow := func(kind, state string) map[string]any {
		return map[string]any{
			"id": "11111111-1111-4111-8111-111111111111", "project_id": "22222222-2222-4222-8222-222222222222",
			"kind": kind, "title": "Work", "description": "", "acceptance_criteria": "", "state": state,
			"version": float64(1), "created_at": "2026-11-21T12:00:00Z", "updated_at": "2026-11-21T12:00:00Z",
		}
	}
	summary := map[string]any{"backlog": float64(0), "queued": float64(0), "in_progress": float64(0), "done": float64(0), "cancelled": float64(0)}
	validEpic := workRow("epic", "open")
	validEpic["summary"] = summary
	for _, test := range []struct {
		name  string
		row   map[string]any
		valid bool
	}{
		{name: "Epic", row: validEpic, valid: true},
		{name: "Epic executable state", row: func() map[string]any { row := maps.Clone(validEpic); row["state"] = "backlog"; return row }()},
		{name: "Epic missing summary", row: func() map[string]any { row := maps.Clone(validEpic); delete(row, "summary"); return row }()},
		{name: "Epic parent", row: func() map[string]any {
			row := maps.Clone(validEpic)
			row["parent_id"] = "33333333-3333-4333-8333-333333333333"
			return row
		}()},
		{name: "Task", row: workRow("task", "backlog"), valid: true},
		{name: "Task Bug field", row: func() map[string]any {
			row := workRow("task", "backlog")
			row["fix_evidence"] = "not applicable"
			return row
		}()},
		{name: "Bug", row: func() map[string]any {
			row := workRow("bug", "done")
			row["fix_evidence"] = "owner-written"
			return row
		}(), valid: true},
		{name: "Bug summary", row: func() map[string]any { row := workRow("bug", "done"); row["summary"] = summary; return row }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := task.VisitJSON(test.row)
			if (err == nil) != test.valid {
				t.Fatalf("schema validity = %v, error = %v", err == nil, err)
			}
		})
	}
	legacy := doc.Components.Schemas["LegacyTask"].Value
	if _, exists := legacy.Properties["kind"]; exists {
		t.Fatal("legacy mutation response gained a required work-type field")
	}
	create := doc.Components.Schemas["CreateTask"].Value
	if slices.Contains(create.Required, "kind") || create.Properties["kind"] == nil || create.Properties["parent_id"] == nil {
		t.Fatal("CreateTask broke omitted-kind compatibility or parent support")
	}
	edit := doc.Components.Schemas["EditTask"].Value
	if _, exists := edit.Properties["kind"]; exists || edit.Properties["parent_id"] == nil {
		t.Fatal("EditTask must reject kind and accept parent_id")
	}
	createResponse := doc.Paths.Find("/v1/projects/{id}/tasks").Post.Responses.Value("201").Value.Content.Get("application/json").Schema
	if createResponse.Ref != "#/components/schemas/LegacyTask" {
		t.Fatalf("create response schema = %q, want LegacyTask", createResponse.Ref)
	}
}

// Every request through the API test helper validates its response against the
// actual operation, including errors. Successful requests validate input too.
func checkContract(t *testing.T, r *http.Request, w *httptest.ResponseRecorder) {
	t.Helper()
	r = r.Clone(r.Context())
	r.URL.Scheme = "http"
	r.URL.Host = "127.0.0.1:7331"
	r.Host = r.URL.Host
	router, err := legacy.NewRouter(contract(t))
	if err != nil {
		t.Fatal(err)
	}
	route, params, err := router.FindRoute(r)
	if err != nil {
		t.Fatal(err)
	}
	input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	if w.Code < 300 {
		if err = openapi3filter.ValidateRequest(t.Context(), input); err != nil {
			t.Fatalf("request contract: %v", err)
		}
	}
	output := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: w.Code, Header: w.Header(), Options: &openapi3filter.Options{IncludeResponseStatus: true}}
	output.SetBodyBytes(bytes.Clone(w.Body.Bytes()))
	if err = openapi3filter.ValidateResponse(t.Context(), output); err != nil {
		t.Fatalf("response contract: %v\n%s", err, w.Body)
	}
}
