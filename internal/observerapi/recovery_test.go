package observerapi_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/app"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/store"
)

func TestOwnerBindLostResponseAndGeneration(t *testing.T) {
	f := newFixture(t)
	r := reg()
	name, root := "Fictional", t.TempDir()
	result, e := app.New(f.s).Execute(t.Context(), uuid.NewString(), "POST", "/v1/projects", app.Command{Action: app.CreateProject, Name: &name, RootPath: &root})
	if e != nil {
		t.Fatal(e)
	}
	var project domain.Project
	if e = json.Unmarshal(result.Body, &project); e != nil {
		t.Fatal(e)
	}
	before, e := f.s.Snapshot(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	f.request(t, true, "POST", "/observer/v1/register", f.token, "", r, 200)
	key := uuid.NewString()
	body := map[string]any{"expected_version": 1, "runtime_id": r.RuntimeID, "project_id": project.ID}
	first := f.request(t, false, "PATCH", "/v1/observers/"+f.id+"/binding", f.ownerToken, key, body, 200)
	retry := f.request(t, false, "PATCH", "/v1/observers/"+f.id+"/binding", f.ownerToken, key, body, 200)
	if !bytes.Equal(first, retry) {
		t.Fatal("lost binding response not recoverable")
	}
	f.request(t, false, "PATCH", "/v1/observers/"+f.id+"/binding", f.ownerToken, uuid.NewString(), body, 409)
	report := store.ObserverReport{RuntimeID: r.RuntimeID, ReportSeq: 1, Activity: "working", Lifecycle: "running"}
	f.request(t, true, "PUT", "/observer/v1/report", f.token, "", report, 409)
	report.Generation = 1
	f.request(t, true, "PUT", "/observer/v1/report", f.token, "", report, 200)
	f.request(t, false, "PATCH", "/v1/observers/"+f.id+"/binding", f.ownerToken, uuid.NewString(), `{"expected_version":2,"runtime_id":null,"project_id":null}`, 200)
	f.request(t, true, "PUT", "/observer/v1/report", f.token, "", report, 409)
	f.request(t, false, "POST", "/v1/observers/"+f.id+"/prune", f.ownerToken, uuid.NewString(), `{"expected_version":3}`, 200)
	f.request(t, true, "POST", "/observer/v1/register", f.token, "", r, 409)
	f.request(t, true, "GET", "/observer/v1/context?runtime_id="+r.RuntimeID, f.token, "", nil, 409)
	after, e := f.s.Snapshot(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if !bytes.Equal(b, a) {
		t.Fatal("observer changed domain snapshot")
	}
}
