package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/app"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/store"
)

func ptr[T any](v T) *T { return &v }

type fixture struct {
	t    *testing.T
	s    *store.Store
	a    *app.Service
	path string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artel.sqlite")
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return &fixture{t, s, app.New(s), path}
}
func (f *fixture) run(c app.Command) store.Result {
	f.t.Helper()
	r, err := f.a.Execute(f.t.Context(), uuid.NewString(), "POST", "/test", c)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}
func decode[T any](t *testing.T, r store.Result) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func (f *fixture) project() domain.Project {
	f.t.Helper()
	return decode[domain.Project](f.t, f.run(app.Command{Action: app.CreateProject, Name: ptr("Проект"), RootPath: ptr(f.t.TempDir())}))
}
func (f *fixture) task(p domain.Project) domain.Task {
	f.t.Helper()
	return decode[domain.Task](f.t, f.run(app.Command{Action: app.CreateTask, ProjectID: p.ID, Title: ptr("Задача")}))
}
func (f *fixture) snapshot() store.Snapshot {
	f.t.Helper()
	s, err := f.s.Snapshot(f.t.Context())
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}
func (f *fixture) getProject(id string) domain.Project {
	f.t.Helper()
	for _, p := range f.snapshot().Projects {
		if p.ID == id {
			return p
		}
	}
	f.t.Fatal("missing project")
	return domain.Project{}
}
func (f *fixture) reject(c app.Command, code string) {
	f.t.Helper()
	before := f.snapshot()
	key := uuid.NewString()
	_, err := f.a.Execute(f.t.Context(), key, "POST", "/test", c)
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != code {
		f.t.Fatalf("want %s, got %v", code, err)
	}
	after := f.snapshot()
	if !reflect.DeepEqual(before, after) {
		f.t.Fatal("failed command changed snapshot/events")
	}
	// A failed request cannot claim a key. Reuse it for a different valid payload.
	_, err = f.a.Execute(f.t.Context(), key, "POST", "/test", app.Command{Action: app.CreateProject, Name: ptr("receipt rollback probe"), RootPath: ptr(f.t.TempDir())})
	if err != nil {
		f.t.Fatalf("failed request left a receipt: %v", err)
	}
}

func TestManualLifecycleQueueAndAudit(t *testing.T) {
	f := setup(t)
	p := f.project()
	a, b, c := f.task(p), f.task(p), f.task(p)
	f.run(app.Command{Action: app.ReplaceQueue, ProjectID: p.ID, TaskIDs: []string{a.ID, b.ID}, ExpectedQueueVersion: 1})
	snap := f.snapshot()
	if snap.Projects[0].QueueVersion != 2 {
		t.Fatal("queue version")
	}
	f.reject(app.Command{Action: app.Transition, TaskID: b.ID, State: domain.InProgress, ExpectedVersion: 2, ExpectedQueueVersion: 2}, "conflict")
	f.reject(app.Command{Action: app.EditProject, ProjectID: p.ID, Archived: ptr(true), ExpectedVersion: 1}, "conflict")
	f.run(app.Command{Action: app.ReplaceQueue, ProjectID: p.ID, TaskIDs: []string{b.ID, a.ID}, ExpectedQueueVersion: 2})
	for _, task := range f.snapshot().Tasks {
		if task.ID == a.ID || task.ID == b.ID {
			if task.Version != 2 {
				t.Fatal("pure reorder changed task version")
			}
		}
	}
	started := decode[domain.Task](t, f.run(app.Command{Action: app.Transition, TaskID: b.ID, State: domain.InProgress, ExpectedVersion: 2, ExpectedQueueVersion: 3}))
	if started.Version != 3 || f.getProject(p.ID).QueueVersion != 4 {
		t.Fatal("start versions")
	}
	f.reject(app.Command{Action: app.Transition, TaskID: a.ID, State: domain.InProgress, ExpectedVersion: 2, ExpectedQueueVersion: 4}, "conflict")
	f.run(app.Command{Action: app.Transition, TaskID: b.ID, State: domain.Done, ExpectedVersion: 3})
	f.run(app.Command{Action: app.Transition, TaskID: b.ID, State: domain.Backlog, ExpectedVersion: 4})
	f.run(app.Command{Action: app.Transition, TaskID: a.ID, State: domain.Cancelled, ExpectedVersion: 2, ExpectedQueueVersion: 4})
	if len(f.snapshot().Queues[p.ID]) != 0 {
		t.Fatal("cancel left queue entry")
	}
	f.run(app.Command{Action: app.Transition, TaskID: a.ID, State: domain.Backlog, ExpectedVersion: 3})
	f.run(app.Command{Action: app.Transition, TaskID: c.ID, State: domain.Cancelled, ExpectedVersion: 1})
	events, err := f.s.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		if e.Seq != int64(i+1) || e.Source != "owner_manual" || e.ProjectID == "" || len(e.Changes) == 0 || e.Timestamp == "" {
			t.Fatalf("bad event: %+v", e)
		}
	}
	last := events[len(events)-1]
	change := last.Changes[0]
	if change.Before["state"] != "backlog" || change.After["state"] != "cancelled" || change.After["version"] != float64(2) {
		t.Fatalf("bad audit: %+v", change)
	}
	if _, ok := change.After["title"]; ok {
		t.Fatal("audit includes unchanged fields")
	}
}

func TestQueueMembershipAndInvalidRequests(t *testing.T) {
	f := setup(t)
	p := f.project()
	other := f.project()
	a, b := f.task(p), f.task(p)
	foreign := f.task(other)
	for _, ids := range [][]string{{a.ID, a.ID}, {foreign.ID}, {uuid.NewString()}} {
		code := "conflict"
		if len(ids) == 2 {
			code = "invalid_request"
		}
		if ids[0] != foreign.ID && len(ids) == 1 {
			code = "not_found"
		}
		f.reject(app.Command{Action: app.ReplaceQueue, ProjectID: p.ID, TaskIDs: ids, ExpectedQueueVersion: 1}, code)
	}
	f.run(app.Command{Action: app.ReplaceQueue, ProjectID: p.ID, TaskIDs: []string{a.ID, b.ID}, ExpectedQueueVersion: 1})
	f.run(app.Command{Action: app.ReplaceQueue, ProjectID: p.ID, TaskIDs: []string{b.ID}, ExpectedQueueVersion: 2})
	for _, task := range f.snapshot().Tasks {
		if task.ID == a.ID && (task.State != domain.Backlog || task.Version != 3) {
			t.Fatal("excluded task not returned to backlog")
		}
	}
	f.run(app.Command{Action: app.Transition, TaskID: b.ID, State: domain.InProgress, ExpectedVersion: 2, ExpectedQueueVersion: 3})
	f.reject(app.Command{Action: app.ReplaceQueue, ProjectID: p.ID, TaskIDs: []string{b.ID}, ExpectedQueueVersion: 4}, "conflict")
	f.run(app.Command{Action: app.Transition, TaskID: b.ID, State: domain.Backlog, ExpectedVersion: 3})
	f.reject(app.Command{Action: app.Transition, TaskID: b.ID, State: domain.Done, ExpectedVersion: 4}, "conflict")
	f.reject(app.Command{Action: app.Transition, TaskID: b.ID, State: domain.Queued, ExpectedVersion: 4}, "conflict")
}

func TestNoOpsAndIdempotency(t *testing.T) {
	f := setup(t)
	p := f.project()
	task := f.task(p)
	before := f.snapshot()
	for _, c := range []app.Command{
		{Action: app.EditProject, ProjectID: p.ID, Name: ptr(p.Name), ExpectedVersion: 1},
		{Action: app.EditTask, TaskID: task.ID, Title: ptr(task.Title), ExpectedVersion: 1},
		{Action: app.ReplaceQueue, ProjectID: p.ID, ExpectedQueueVersion: 1},
		{Action: app.Transition, TaskID: task.ID, State: domain.Backlog, ExpectedVersion: 1},
	} {
		f.run(c)
	}
	if !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("no-op changed state/event/version")
	}
	c := app.Command{Action: app.EditTask, TaskID: task.ID, Title: ptr("changed"), ExpectedVersion: 1}
	key := uuid.NewString()
	first, err := f.a.Execute(t.Context(), key, "PATCH", "/tasks/"+task.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	snap := f.snapshot()
	replay, err := f.a.Execute(t.Context(), key, "PATCH", "/tasks/"+task.ID, c)
	if err != nil || !reflect.DeepEqual(first, replay) || !reflect.DeepEqual(snap, f.snapshot()) {
		t.Fatalf("replay failed: %v", err)
	}
	for _, req := range []struct {
		method, path string
		c            app.Command
	}{
		{"POST", "/tasks/" + task.ID, c}, {"PATCH", "/different", c},
		{"PATCH", "/tasks/" + task.ID, app.Command{Action: app.EditTask, TaskID: task.ID, Title: ptr("different"), ExpectedVersion: 1}},
	} {
		_, err := f.a.Execute(t.Context(), key, req.method, req.path, req.c)
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != "conflict" {
			t.Fatalf("key reuse accepted: %v", err)
		}
	}
	f.reject(c, "conflict")
	_, err = f.a.Execute(t.Context(), "bad-key", "POST", "/test", c)
	if err == nil {
		t.Fatal("bad key")
	}
}

func TestProjectPathsArchiveAndValidation(t *testing.T) {
	f := setup(t)
	root := t.TempDir()
	p := decode[domain.Project](t, f.run(app.Command{Action: app.CreateProject, Name: ptr("one"), RootPath: &root}))
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	f.reject(app.Command{Action: app.CreateProject, Name: ptr("duplicate"), RootPath: &alias}, "conflict")
	f.run(app.Command{Action: app.EditProject, ProjectID: p.ID, Archived: ptr(true), ExpectedVersion: 1})
	f.reject(app.Command{Action: app.CreateTask, ProjectID: p.ID, Title: ptr("blocked")}, "conflict")
	f.reject(app.Command{Action: app.EditProject, ProjectID: p.ID, Name: ptr("blocked"), ExpectedVersion: 2}, "conflict")
	replacement := decode[domain.Project](t, f.run(app.Command{Action: app.CreateProject, Name: ptr("replacement"), RootPath: &root}))
	f.reject(app.Command{Action: app.EditProject, ProjectID: p.ID, Archived: ptr(false), ExpectedVersion: 2}, "conflict")
	f.run(app.Command{Action: app.EditProject, ProjectID: replacement.ID, Archived: ptr(true), ExpectedVersion: 1})
	f.run(app.Command{Action: app.EditProject, ProjectID: p.ID, Archived: ptr(false), ExpectedVersion: 2})
	task := f.task(p)
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	// Disappearing roots do not destroy objects or prevent text edits.
	f.run(app.Command{Action: app.EditTask, TaskID: task.ID, Description: ptr("still available"), AcceptanceCriteria: ptr("criteria"), ExpectedVersion: 1})
	f.run(app.Command{Action: app.EditProject, ProjectID: p.ID, Name: ptr("renamed"), ExpectedVersion: 3})
	for _, path := range []string{"relative", root, alias} {
		f.reject(app.Command{Action: app.CreateProject, Name: ptr("bad path"), RootPath: &path}, "invalid_request")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	f.reject(app.Command{Action: app.CreateProject, Name: ptr("file"), RootPath: &file}, "invalid_request")
	for _, name := range []string{"", strings.Repeat("я", 121), string([]byte{0xff})} {
		f.reject(app.Command{Action: app.CreateProject, Name: &name, RootPath: ptr(t.TempDir())}, "invalid_request")
	}
	f.reject(app.Command{Action: app.CreateProject, Name: ptr("description"), RootPath: ptr(t.TempDir()), Description: ptr(strings.Repeat("x", 16385))}, "invalid_request")
	f.reject(app.Command{Action: app.CreateTask, ProjectID: p.ID, Title: ptr(strings.Repeat("x", 201))}, "invalid_request")
	f.reject(app.Command{Action: app.EditTask, TaskID: task.ID, Description: ptr(strings.Repeat("я", 32769)), ExpectedVersion: 2}, "invalid_request")
	f.reject(app.Command{Action: app.EditTask, TaskID: task.ID, AcceptanceCriteria: ptr(strings.Repeat("x", 65537)), ExpectedVersion: 2}, "invalid_request")
	f.reject(app.Command{Action: app.CreateTask, ProjectID: uuid.NewString(), Title: ptr("missing")}, "not_found")
	f.reject(app.Command{Action: app.EditTask, TaskID: uuid.NewString(), ExpectedVersion: 1}, "not_found")
}

func raceCommands(t *testing.T, services []*app.Service, commands []app.Command) []error {
	t.Helper()
	start := make(chan struct{})
	errs := make([]error, len(commands))
	var wg sync.WaitGroup
	for i, c := range commands {
		wg.Go(func() {
			<-start
			_, errs[i] = services[i%len(services)].Execute(t.Context(), uuid.NewString(), "POST", "/race", c)
		})
	}
	close(start)
	wg.Wait()
	return errs
}
func successes(t *testing.T, errs []error, want int) {
	t.Helper()
	got := 0
	for _, err := range errs {
		if err == nil {
			got++
			continue
		}
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != "conflict" {
			t.Fatalf("unexpected concurrent failure: %v", err)
		}
	}
	if got != want {
		t.Fatalf("%d successes, want %d: %v", got, want, errs)
	}
}
func secondService(t *testing.T, f *fixture) *app.Service {
	t.Helper()
	s, err := store.Open(t.Context(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return app.New(s)
}
func TestConcurrentEditorsAndUnrelatedWrites(t *testing.T) {
	f := setup(t)
	p := f.project()
	a, b := f.task(p), f.task(p)
	services := []*app.Service{f.a, secondService(t, f)}
	successes(t, raceCommands(t, services, []app.Command{
		{Action: app.EditTask, TaskID: a.ID, Title: ptr("editor one"), ExpectedVersion: 1},
		{Action: app.EditTask, TaskID: a.ID, Title: ptr("editor two"), ExpectedVersion: 1},
	}), 1)
	successes(t, raceCommands(t, services, []app.Command{
		{Action: app.EditTask, TaskID: a.ID, Description: ptr("independent a"), ExpectedVersion: 2},
		{Action: app.EditTask, TaskID: b.ID, Description: ptr("independent b"), ExpectedVersion: 1},
	}), 2)
	snap := f.snapshot()
	if snap.EventSeq != 6 {
		t.Fatalf("events = %d", snap.EventSeq)
	}
	for _, task := range snap.Tasks {
		if !strings.HasPrefix(task.Description, "independent") {
			t.Fatal("unrelated edit lost")
		}
	}
}

func TestConcurrentReorderStartArchive(t *testing.T) {
	for _, kind := range []string{"reorder-start", "start-start", "archive-queue", "archive-start", "reorder-reorder"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			p := f.project()
			a, b := f.task(p), f.task(p)
			services := []*app.Service{f.a, secondService(t, f)}
			if kind != "archive-queue" {
				f.run(app.Command{Action: app.ReplaceQueue, ProjectID: p.ID, TaskIDs: []string{a.ID, b.ID}, ExpectedQueueVersion: 1})
			}
			start := app.Command{Action: app.Transition, TaskID: a.ID, State: domain.InProgress, ExpectedVersion: 2, ExpectedQueueVersion: 2}
			reorder := app.Command{Action: app.ReplaceQueue, ProjectID: p.ID, TaskIDs: []string{b.ID, a.ID}, ExpectedQueueVersion: 2}
			archive := app.Command{Action: app.EditProject, ProjectID: p.ID, Archived: ptr(true), ExpectedVersion: 1}
			commands := []app.Command{reorder, start}
			switch kind {
			case "start-start":
				commands = []app.Command{start, start}
			case "archive-queue":
				commands = []app.Command{archive, {Action: app.ReplaceQueue, ProjectID: p.ID, TaskIDs: []string{a.ID}, ExpectedQueueVersion: 1}}
			case "archive-start":
				commands = []app.Command{archive, start}
			case "reorder-reorder":
				commands = []app.Command{reorder, reorder}
			}
			successes(t, raceCommands(t, services, commands), 1)
			snap := f.snapshot()
			current := 0
			for _, task := range snap.Tasks {
				if task.State == domain.InProgress {
					current++
				}
				if snap.Projects[0].Archived && (task.State == domain.InProgress || task.State == domain.Queued) {
					t.Fatal("archive invariant")
				}
			}
			if current > 1 {
				t.Fatal("multiple current tasks")
			}
		})
	}
}

func TestConcurrentSameKeyAndReopen(t *testing.T) {
	f := setup(t)
	other := secondService(t, f)
	key := uuid.NewString()
	root := t.TempDir()
	c := app.Command{Action: app.CreateProject, Name: ptr("once"), RootPath: &root}
	var wg sync.WaitGroup
	results := make([]store.Result, 2)
	errs := make([]error, 2)
	for i, a := range []*app.Service{f.a, other} {
		wg.Go(func() { results[i], errs[i] = a.Execute(context.Background(), key, "POST", "/projects", c) })
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
		t.Fatalf("concurrent receipt: %v", errs)
	}
	before := f.snapshot()
	reopened, err := store.Open(t.Context(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	after, err := reopened.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || len(after.Projects) != 1 || after.EventSeq != 1 {
		t.Fatal("reopen lost data/identity")
	}
	result, err := app.New(reopened).Execute(t.Context(), key, "POST", "/projects", c)
	if err != nil || !reflect.DeepEqual(result, results[0]) {
		t.Fatalf("receipt lost: %v", err)
	}
}
