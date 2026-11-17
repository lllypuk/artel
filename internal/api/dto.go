package api

import (
	"sort"

	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/store"
)

// Wire DTOs preserve the OpenAPI contract independently of storage layouts.
type queueDTO struct {
	ProjectID    string   `json:"project_id"`
	QueueVersion int64    `json:"queue_version"`
	TaskIDs      []string `json:"task_ids"`
}
type snapshotDTO struct {
	InstanceID string           `json:"instance_id"`
	EventSeq   int64            `json:"event_seq"`
	Projects   []domain.Project `json:"projects"`
	Tasks      []domain.Task    `json:"tasks"`
	Queues     []queueDTO       `json:"queues"`
}

func wireSnapshot(s store.Snapshot) snapshotDTO {
	out := snapshotDTO{InstanceID: s.InstanceID, EventSeq: s.EventSeq, Projects: s.Projects, Tasks: s.Tasks, Queues: []queueDTO{}}
	for _, p := range s.Projects {
		out.Queues = append(out.Queues, queueDTO{p.ID, p.QueueVersion, s.Queues[p.ID]})
	}
	sort.Slice(out.Queues, func(i, j int) bool { return out.Queues[i].ProjectID < out.Queues[j].ProjectID })
	return out
}

type eventDTO struct {
	Seq       int64                     `json:"seq"`
	Action    string                    `json:"action"`
	ProjectID string                    `json:"project_id"`
	TaskID    string                    `json:"task_id,omitempty"`
	Source    string                    `json:"source"`
	Timestamp string                    `json:"timestamp"`
	Before    map[string]map[string]any `json:"before"`
	After     map[string]map[string]any `json:"after"`
}
type historyDTO struct {
	InstanceID string     `json:"instance_id"`
	Events     []eventDTO `json:"events"`
	NextCursor int64      `json:"next_cursor"`
}

func wireHistory(p store.HistoryPage) historyDTO {
	out := historyDTO{InstanceID: p.InstanceID, NextCursor: p.NextCursor, Events: []eventDTO{}}
	for _, e := range p.Events {
		event := eventDTO{Seq: e.Seq, Action: e.Action, ProjectID: e.ProjectID, TaskID: e.TaskID, Source: e.Source, Timestamp: e.Timestamp, Before: map[string]map[string]any{}, After: map[string]map[string]any{}}
		for _, c := range e.Changes {
			key := c.Object + ":" + c.ID
			event.Before[key] = c.Before
			event.After[key] = c.After
		}
		out.Events = append(out.Events, event)
	}
	return out
}
