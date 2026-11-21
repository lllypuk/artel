// Package app implements the manual workflows inside store transactions.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/store"
)

const (
	CreateProject = "project.create"
	EditProject   = "project.edit"
	CreateTask    = "task.create"
	EditTask      = "task.edit"
	Transition    = "task.transition"
	ReplaceQueue  = "queue.replace"
)

// Command is a typed canonical payload; pointers distinguish omission from empty
// text. The HTTP adapter must reject unknown fields and select the action/IDs.
type Command struct {
	Action               string       `json:"action"`
	ProjectID            string       `json:"project_id,omitempty"`
	TaskID               string       `json:"task_id,omitempty"`
	Name                 *string      `json:"name,omitempty"`
	RootPath             *string      `json:"root_path,omitempty"`
	Description          *string      `json:"description,omitempty"`
	Title                *string      `json:"title,omitempty"`
	AcceptanceCriteria   *string      `json:"acceptance_criteria,omitempty"`
	Archived             *bool        `json:"archived,omitempty"`
	State                domain.State `json:"state,omitempty"`
	TaskIDs              []string     `json:"task_ids,omitempty"`
	ExpectedVersion      int64        `json:"expected_version,omitempty"`
	ExpectedQueueVersion int64        `json:"expected_queue_version,omitempty"`
	ExpectedResultID     *string      `json:"expected_result_id,omitempty"`
}

type Service struct{ store *store.Store }

func New(s *store.Store) *Service { return &Service{store: s} }

func (s *Service) Execute(ctx context.Context, key, method, path string, c Command) (store.Result, error) {
	id, err := uuid.Parse(key)
	if err != nil || id.String() != key {
		return store.Result{}, domain.Invalid("Idempotency-Key must be a canonical UUID")
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return store.Result{}, err
	}
	hash := sha256.Sum256(payload)
	return s.store.Apply(ctx, store.Mutation{Key: key, Method: method, Path: path, Hash: hex.EncodeToString(hash[:])}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		return execute(snap, c)
	})
}

func execute(snap *store.Snapshot, c Command) (store.Result, *store.Event, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	event := &store.Event{Action: c.Action, ProjectID: c.ProjectID, TaskID: c.TaskID, Source: "owner_manual", Timestamp: now, Changes: []store.Change{}}
	var value any
	var err error
	status := 200
	switch c.Action {
	case CreateProject:
		value, err = createProject(snap, c, now, event)
		status = 201
	case EditProject:
		value, err = editProject(snap, c, now, event)
	case CreateTask:
		value, err = createTask(snap, c, now, event)
		status = 201
	case EditTask, Transition:
		value, err = changeTask(snap, c, now, event)
	case ReplaceQueue:
		value, err = replaceQueue(snap, c, now, event)
	default:
		err = domain.Invalid("unknown action")
	}
	if err != nil {
		return store.Result{}, nil, err
	}
	data, err := json.Marshal(value)
	if len(event.Changes) == 0 {
		event = nil
	}
	return store.Result{Status: status, Body: data}, event, err
}

func canonicalRoot(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", domain.Invalid("root must be an absolute existing directory")
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", domain.Invalid("root directory unavailable")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", domain.Invalid("root directory unavailable")
	}
	return root, nil
}

func uniqueRoot(snap *store.Snapshot, p domain.Project) error {
	for _, other := range snap.Projects {
		if other.ID != p.ID && !other.Archived && other.RootPath == p.RootPath {
			return domain.Conflict("active project root already exists")
		}
	}
	return nil
}

func validateProject(p domain.Project) error {
	if err := domain.ValidateText(p.Name, 1, 120); err != nil {
		return err
	}
	return longText(p.Description, 16*1024)
}
func longText(s string, limit int) error {
	if !utf8.ValidString(s) || len(s) > limit {
		return domain.Invalid("text exceeds byte limit or is not UTF-8")
	}
	return nil
}
func validateTask(t domain.Task) error {
	if err := domain.ValidateText(t.Title, 1, 200); err != nil {
		return err
	}
	if err := longText(t.Description, 64*1024); err != nil {
		return err
	}
	return longText(t.AcceptanceCriteria, 64*1024)
}

func createProject(snap *store.Snapshot, c Command, now string, e *store.Event) (any, error) {
	if c.Name == nil || c.RootPath == nil {
		return nil, domain.Invalid("name and root_path are required")
	}
	root, err := canonicalRoot(*c.RootPath)
	if err != nil {
		return nil, err
	}
	p := domain.Project{ID: uuid.NewString(), Name: *c.Name, RootPath: root, Version: 1, QueueVersion: 1, CreatedAt: now, UpdatedAt: now}
	if c.Description != nil {
		p.Description = *c.Description
	}
	if err := validateProject(p); err != nil {
		return nil, err
	}
	if err := uniqueRoot(snap, p); err != nil {
		return nil, err
	}
	snap.Projects = append(snap.Projects, p)
	snap.Queues[p.ID] = []string{}
	e.ProjectID = p.ID
	addChange(e, "project", p.ID, nil, p)
	return p, nil
}

func project(snap *store.Snapshot, id string) (*domain.Project, error) {
	for i := range snap.Projects {
		if snap.Projects[i].ID == id {
			return &snap.Projects[i], nil
		}
	}
	return nil, &domain.Error{Code: "not_found", Message: "project not found"}
}
func task(snap *store.Snapshot, id string) (*domain.Task, error) {
	for i := range snap.Tasks {
		if snap.Tasks[i].ID == id {
			return &snap.Tasks[i], nil
		}
	}
	return nil, &domain.Error{Code: "not_found", Message: "task not found"}
}
func writable(p *domain.Project) error {
	if p.Archived {
		return domain.Conflict("project is archived")
	}
	return nil
}

func editProject(snap *store.Snapshot, c Command, now string, e *store.Event) (any, error) {
	p, err := project(snap, c.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := domain.CheckVersion(c.ExpectedVersion, p.Version); err != nil {
		return nil, err
	}
	before := *p
	// Only an explicit unarchive may change an archived project.
	if p.Archived && (c.Archived == nil || *c.Archived || c.Name != nil || c.RootPath != nil || c.Description != nil) {
		return nil, domain.Conflict("project is archived")
	}
	if c.Name != nil {
		p.Name = *c.Name
	}
	if c.Description != nil {
		p.Description = *c.Description
	}
	if c.RootPath != nil {
		p.RootPath, err = canonicalRoot(*c.RootPath)
		if err != nil {
			return nil, err
		}
	}
	if c.Archived != nil {
		p.Archived = *c.Archived
	}
	if err := validateProject(*p); err != nil {
		return nil, err
	}
	if err := checkArchive(snap, p, before.Archived); err != nil {
		return nil, err
	}
	if *p != before {
		p.Version++
		p.UpdatedAt = now
		addChange(e, "project", p.ID, before, *p)
	}
	return *p, nil
}

func checkArchive(snap *store.Snapshot, p *domain.Project, wasArchived bool) error {
	if p.Archived {
		for _, t := range snap.Tasks {
			if t.ProjectID == p.ID && (t.State == domain.Queued || t.State == domain.InProgress) {
				return domain.Conflict("project has queued or current tasks")
			}
		}
		return nil
	}
	if wasArchived {
		root, err := canonicalRoot(p.RootPath)
		if err != nil {
			return err
		}
		p.RootPath = root
	}
	return uniqueRoot(snap, *p)
}

func createTask(snap *store.Snapshot, c Command, now string, e *store.Event) (any, error) {
	p, err := project(snap, c.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := writable(p); err != nil {
		return nil, err
	}
	if c.Title == nil {
		return nil, domain.Invalid("title is required")
	}
	t := domain.Task{ID: uuid.NewString(), ProjectID: p.ID, Title: *c.Title, State: domain.Backlog, Version: 1, CreatedAt: now, UpdatedAt: now}
	if c.Description != nil {
		t.Description = *c.Description
	}
	if c.AcceptanceCriteria != nil {
		t.AcceptanceCriteria = *c.AcceptanceCriteria
	}
	if err := validateTask(t); err != nil {
		return nil, err
	}
	snap.Tasks = append(snap.Tasks, t)
	e.TaskID = t.ID
	addChange(e, "task", t.ID, nil, t)
	return t, nil
}

func changeTask(snap *store.Snapshot, c Command, now string, e *store.Event) (any, error) {
	t, err := task(snap, c.TaskID)
	if err != nil {
		return nil, err
	}
	p, err := project(snap, t.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := writable(p); err != nil {
		return nil, err
	}
	if c.Action == Transition {
		current, submitted := snap.CurrentResultID(t.ID)
		expected := ""
		if c.ExpectedResultID != nil {
			expected = *c.ExpectedResultID
		}
		if (submitted && (c.ExpectedResultID == nil || *c.ExpectedResultID != current)) || (!submitted && expected != "") {
			return nil, domain.Conflict("current task result changed")
		}
	}
	if err := domain.CheckVersion(c.ExpectedVersion, t.Version); err != nil {
		return nil, err
	}
	before := *t
	e.ProjectID = p.ID
	if c.Action == EditTask {
		if c.Title != nil {
			t.Title = *c.Title
		}
		if c.Description != nil {
			t.Description = *c.Description
		}
		if c.AcceptanceCriteria != nil {
			t.AcceptanceCriteria = *c.AcceptanceCriteria
		}
		if err := validateTask(*t); err != nil {
			return nil, err
		}
	} else {
		if err := transition(snap, p, t, c, now, e); err != nil {
			return nil, err
		}
	}
	if *t != before {
		t.Version++
		t.UpdatedAt = now
		addChange(e, "task", t.ID, before, *t)
	}
	return *t, nil
}

func transition(snap *store.Snapshot, p *domain.Project, t *domain.Task, c Command, now string, e *store.Event) error {
	// An already achieved state is a no-op, but still requires current versions.
	if t.State == domain.Queued {
		if err := domain.CheckVersion(c.ExpectedQueueVersion, p.QueueVersion); err != nil {
			return err
		}
	}
	if t.State == c.State {
		return nil
	}
	if !domain.CanTransition(t.State, c.State) {
		return domain.Conflict("invalid manual transition")
	}
	if c.State == domain.InProgress {
		queue := snap.Queues[p.ID]
		if len(queue) == 0 || queue[0] != t.ID {
			return domain.Conflict("only the queue head can start")
		}
		for _, other := range snap.Tasks {
			if other.ProjectID == p.ID && other.State == domain.InProgress {
				return domain.Conflict("project already has a current task")
			}
		}
	}
	if t.State == domain.Queued {
		oldQueue := slices.Clone(snap.Queues[p.ID])
		queue := snap.Queues[p.ID]
		index := slices.Index(queue, t.ID)
		if index < 0 {
			return domain.Conflict("queued task is missing from queue")
		}
		snap.Queues[p.ID] = append(queue[:index:index], queue[index+1:]...)
		bumpQueue(p, now, e, oldQueue, snap.Queues[p.ID])
	}
	t.State = c.State
	return nil
}

func replaceQueue(snap *store.Snapshot, c Command, now string, e *store.Event) (any, error) {
	p, err := project(snap, c.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := writable(p); err != nil {
		return nil, err
	}
	if err := domain.CheckVersion(c.ExpectedQueueVersion, p.QueueVersion); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, id := range c.TaskIDs {
		if seen[id] {
			return nil, domain.Invalid("duplicate queue task")
		}
		seen[id] = true
		t, err := task(snap, id)
		if err != nil {
			return nil, err
		}
		if t.ProjectID != p.ID || (t.State != domain.Backlog && t.State != domain.Queued) {
			return nil, domain.Conflict("queue contains an ineligible task")
		}
	}
	oldQueue := snap.Queues[p.ID]
	if !slices.Equal(oldQueue, c.TaskIDs) {
		for i := range snap.Tasks {
			t := &snap.Tasks[i]
			if t.ProjectID != p.ID {
				continue
			}
			before := *t
			if seen[t.ID] {
				t.State = domain.Queued
			} else if t.State == domain.Queued {
				t.State = domain.Backlog
			}
			if *t != before {
				t.Version++
				t.UpdatedAt = now
				addChange(e, "task", t.ID, before, *t)
			}
		}
		snap.Queues[p.ID] = append([]string{}, c.TaskIDs...)
		bumpQueue(p, now, e, oldQueue, snap.Queues[p.ID])
	}
	return struct {
		ProjectID    string   `json:"project_id"`
		QueueVersion int64    `json:"queue_version"`
		TaskIDs      []string `json:"task_ids"`
	}{p.ID, p.QueueVersion, snap.Queues[p.ID]}, nil
}

func bumpQueue(p *domain.Project, now string, e *store.Event, before, after []string) {
	old := *p
	p.QueueVersion++
	p.UpdatedAt = now
	addChange(e, "project", p.ID, old, *p)
	addChange(e, "queue", p.ID, map[string]any{"task_ids": before}, map[string]any{"task_ids": after})
}

// Audit only changed fields (including versions), never filesystem content.
func addChange(e *store.Event, object, id string, before, after any) {
	old := fields(before)
	next := fields(after)
	b, a := map[string]any{}, map[string]any{}
	for key, v := range next {
		if !reflect.DeepEqual(old[key], v) {
			b[key] = old[key]
			a[key] = v
		}
	}
	e.Changes = append(e.Changes, store.Change{Object: object, ID: id, Before: b, After: a})
}
func fields(v any) map[string]any {
	data, _ := json.Marshal(v)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}
