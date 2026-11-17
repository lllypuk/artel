package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestObserverOpenAPI(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "api", "observer-openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = doc.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	count := 0
	for path, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			count++
			want := "OwnerBearer"
			if strings.HasPrefix(path, "/observer/") {
				want = "ObserverBearer"
			}
			if op.Security == nil || len(*op.Security) != 1 || len((*op.Security)[0]) != 1 {
				t.Fatalf("ambiguous auth for %s", path)
			}
			if _, ok := (*op.Security)[0][want]; !ok {
				t.Fatalf("incorrect surface for %s", path)
			}
		}
	}
	if count != 8 {
		t.Fatalf("operations: %d", count)
	}
	const id = "11111111-1111-4111-8111-111111111111"
	cases := []struct {
		name, schema, body string
		valid              bool
	}{
		{"registration", "Registration", `{"runtime_id":"` + id + `","pi_session_id":"` + id + `","reason":"startup","pi_version":"1.5.2","bridge_version":"0.0.0","cwd":"/fictional"}`, true},
		{"report", "Report", `{"runtime_id":"` + id + `","generation":1,"report_seq":1,"activity":"idle","tool_count":0,"lifecycle":"running"}`, true},
		{"no task completion", "Activity", `"completed"`, false},
		{"no tool text", "Report", `{"runtime_id":"` + id + `","generation":1,"report_seq":1,"activity":"idle","tool_count":0,"lifecycle":"running","output":"private"}`, false},
		{"no zero seq", "Report", `{"runtime_id":"` + id + `","generation":1,"report_seq":0,"activity":"idle","tool_count":0,"lifecycle":"running"}`, false},
		{"duplicate", "ReportResult", `{"runtime_id":"` + id + `","generation":1,"report_seq":1,"result":"duplicate"}`, true},
		{"negative version", "CAS", `{"expected_version":-1}`, false},
		{"unbind", "BindingRequest", `{"expected_version":0,"runtime_id":null,"project_id":null}`, true},
		{"mixed binding", "BindingRequest", `{"expected_version":0,"runtime_id":"` + id + `","project_id":null}`, false},
		{"no plaintext credential", "ProvisionRequest", `{"label":"Fixture","verifier":"plaintext"}`, false},
		{"no arbitrary navigation", "TerminalHint", `{"window_id":"` + id + `","session_id":"` + id + `","pane_id":"` + id + `","command":"sh"}`, false},
		{"no prefix id", "UUID", `"1111"`, false},
		{"retired", "Error", `{"error":"runtime_retired"}`, true},
		{"capacity", "Error", `{"error":"capacity_exceeded","capacity":"runtime_identities"}`, true},
		{"no error echo", "Error", `{"error":"unauthorized","token":"private"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.body), &value); err != nil {
				t.Fatal(err)
			}
			err := doc.Components.Schemas[tc.schema].Value.VisitJSON(value)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}
