package api

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

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
