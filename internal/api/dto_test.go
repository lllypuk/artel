package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestWorkTypeWireProjectionAndEpicSummary(t *testing.T) {
	_, h := fixture(t)
	var empty snapshotDTO
	if err := json.Unmarshal(request(t, h, "GET", "/v1/snapshot", "", "", 200), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.WorkTypesVersion != 1 || empty.Tasks == nil || len(empty.Tasks) != 0 {
		t.Fatalf("empty v7 snapshot: %+v", empty)
	}
	projectBody := fmt.Sprintf(`{"name":%q,"root_path":%q}`, "Work types", t.TempDir())
	projectData := request(t, h, "POST", "/v1/projects", projectBody, uuid.NewString(), 201)
	var project struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(projectData, &project); err != nil {
		t.Fatal(err)
	}
	create := func(body string) string {
		t.Helper()
		data := request(t, h, "POST", "/v1/projects/"+project.ID+"/tasks", body, uuid.NewString(), 201)
		var task struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data, &task); err != nil {
			t.Fatal(err)
		}
		return task.ID
	}
	epicID := create(`{"kind":"epic","title":"Epic"}`)
	request(t, h, "POST", "/v1/tasks/"+epicID+"/transition", `{"state":"in_progress","expected_version":1}`, uuid.NewString(), 200)
	backlogID := create(fmt.Sprintf(`{"kind":"bug","title":"Backlog","parent_id":%q,"expected_behavior":"expected","actual_behavior":"actual","fix_evidence":"owner evidence"}`, epicID))
	queuedID := create(fmt.Sprintf(`{"title":"Queued","parent_id":%q}`, epicID))
	runningID := create(fmt.Sprintf(`{"kind":"bug","title":"Running","parent_id":%q}`, epicID))
	doneID := create(fmt.Sprintf(`{"title":"Done","parent_id":%q}`, epicID))
	cancelledID := create(fmt.Sprintf(`{"title":"Cancelled","parent_id":%q}`, epicID))
	queueBody := fmt.Sprintf(`{"task_ids":[%q,%q,%q,%q],"expected_queue_version":1}`, doneID, cancelledID, runningID, queuedID)
	request(t, h, "PUT", "/v1/projects/"+project.ID+"/queue", queueBody, uuid.NewString(), 200)
	transition := func(id, state string, version, queueVersion int64) {
		t.Helper()
		body := fmt.Sprintf(`{"state":%q,"expected_version":%d,"expected_queue_version":%d}`, state, version, queueVersion)
		request(t, h, "POST", "/v1/tasks/"+id+"/transition", body, uuid.NewString(), 200)
	}
	transition(doneID, "in_progress", 2, 2)
	transition(doneID, "done", 3, 3)
	transition(cancelledID, "in_progress", 2, 3)
	transition(cancelledID, "cancelled", 3, 4)
	transition(runningID, "in_progress", 2, 4)

	var snapshot snapshotDTO
	raw := request(t, h, "GET", "/v1/snapshot", "", "", 200)
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	assertWorkTypeProjection(t, snapshot, epicID, backlogID, queuedID, cancelledID)
	history := request(t, h, "GET", "/v1/events?instance_id="+snapshot.InstanceID+"&after=0", "", "", 200)
	if strings.Contains(string(history), "summary") {
		t.Fatal("projection-only summary leaked into history")
	}
}

func assertWorkTypeProjection(t *testing.T, snapshot snapshotDTO, epicID, backlogID, queuedID, cancelledID string) {
	t.Helper()
	if snapshot.WorkTypesVersion != 1 || len(snapshot.Tasks) != 6 {
		t.Fatalf("work-type snapshot: %+v", snapshot)
	}
	rows := make(map[string]taskDTO, len(snapshot.Tasks))
	for _, row := range snapshot.Tasks {
		rows[row.ID] = row
		if row.ID != epicID && row.Summary != nil {
			t.Fatalf("executable row has Epic summary: %+v", row)
		}
	}
	epic := rows[epicID]
	if epic.Kind != "epic" || epic.Summary == nil || *epic.Summary != (epicSummaryDTO{Backlog: 1, Queued: 1, InProgress: 1, Done: 1, Cancelled: 1}) {
		t.Fatalf("Epic summary: %+v", epic)
	}
	bug := rows[backlogID]
	if bug.Kind != "bug" || bug.ParentID != epicID || bug.ExpectedBehavior != "expected" || bug.ActualBehavior != "actual" || bug.FixEvidence != "owner evidence" {
		t.Fatalf("Bug wire fields: %+v", bug)
	}
	if rows[queuedID].Kind != "task" || rows[queuedID].State != "queued" || rows[cancelledID].State != "cancelled" {
		t.Fatalf("legacy Task/default or states lost: %+v", snapshot.Tasks)
	}
}

func TestWireSnapshotAndAuditDoNotLeakStorageShapes(t *testing.T) {
	s, h := fixture(t)
	p := eventProject(t, s)
	var snap snapshotDTO
	if err := json.Unmarshal(request(t, h, "GET", "/v1/snapshot", "", "", 200), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Queues) != 1 || snap.Queues[0].ProjectID != p || snap.Queues[0].QueueVersion != 1 || snap.Queues[0].TaskIDs == nil {
		t.Fatalf("queue DTO: %+v", snap.Queues)
	}
	request(t, h, "PATCH", "/v1/projects/"+p, `{"name":"Next name","expected_version":1}`, uuid.NewString(), 200)
	var page historyDTO
	if err := json.Unmarshal(request(t, h, "GET", "/v1/events?instance_id="+snap.InstanceID+"&after=1", "", "", 200), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 {
		t.Fatal("missing audit")
	}
	e := page.Events[0]
	key := "project:" + p
	if e.Before[key]["name"] != "Project" || e.After[key]["name"] != "Next name" || e.Before[key]["version"] != float64(1) || e.After[key]["version"] != float64(2) {
		t.Fatalf("wrong audit DTO: %+v", e)
	}
}
