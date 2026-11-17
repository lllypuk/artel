package app_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/app"
	"github.com/lllypuk/artel/internal/domain"
)

func TestLimitsArchivedObjectsAndLostResponse(t *testing.T) {
	f := setup(t)
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	command := app.Command{Action: app.CreateProject, Name: ptr(strings.Repeat("я", 120)), RootPath: &alias, Description: ptr(strings.Repeat("x", 16*1024))}
	first, err := f.a.Execute(t.Context(), key, "POST", "/projects", command)
	if err != nil {
		t.Fatal(err)
	}
	p := decode[domain.Project](t, first)
	if p.RootPath != root {
		t.Fatal("root was not canonicalized")
	}
	task := decode[domain.Task](t, f.run(app.Command{Action: app.CreateTask, ProjectID: p.ID, Title: ptr(strings.Repeat("я", 200)), Description: ptr(strings.Repeat("x", 64*1024)), AcceptanceCriteria: ptr(strings.Repeat("x", 64*1024))}))
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatal("project directory was modified")
	}
	f.run(app.Command{Action: app.EditProject, ProjectID: p.ID, Archived: ptr(true), ExpectedVersion: 1})
	f.reject(app.Command{Action: app.EditTask, TaskID: task.ID, Title: ptr("blocked"), ExpectedVersion: 1}, "conflict")
	f.reject(app.Command{Action: app.Transition, TaskID: task.ID, State: domain.Cancelled, ExpectedVersion: 1}, "conflict")
	f.reject(app.Command{Action: app.ReplaceQueue, ProjectID: p.ID, TaskIDs: []string{task.ID}, ExpectedQueueVersion: 1}, "conflict")
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	// A lost create response must replay even after its original path disappears.
	before := f.snapshot()
	repeated, err := f.a.Execute(t.Context(), key, "POST", "/projects", command)
	if err != nil || !reflect.DeepEqual(first, repeated) || !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatalf("lost create response could not replay: %v", err)
	}
	f.reject(app.Command{Action: app.EditProject, ProjectID: p.ID, Archived: ptr(false), ExpectedVersion: 2}, "invalid_request")
}
