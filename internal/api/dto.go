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
type epicSummaryDTO struct {
	Backlog    int `json:"backlog"`
	Queued     int `json:"queued"`
	InProgress int `json:"in_progress"`
	Done       int `json:"done"`
	Cancelled  int `json:"cancelled"`
}

type taskDTO struct {
	domain.Task
	Kind             domain.Kind     `json:"kind"`
	ParentID         string          `json:"parent_id,omitempty"`
	ExpectedBehavior string          `json:"expected_behavior,omitempty"`
	ActualBehavior   string          `json:"actual_behavior,omitempty"`
	FixEvidence      string          `json:"fix_evidence,omitempty"`
	Summary          *epicSummaryDTO `json:"summary,omitempty"`
}

type snapshotDTO struct {
	InstanceID       string           `json:"instance_id"`
	EventSeq         int64            `json:"event_seq"`
	WorkTypesVersion int              `json:"work_types_version"`
	Projects         []domain.Project `json:"projects"`
	Tasks            []taskDTO        `json:"tasks"`
	Queues           []queueDTO       `json:"queues"`
}

func wireSnapshot(s store.Snapshot) snapshotDTO {
	out := snapshotDTO{InstanceID: s.InstanceID, EventSeq: s.EventSeq, WorkTypesVersion: 1,
		Projects: s.Projects, Tasks: []taskDTO{}, Queues: []queueDTO{}}
	for _, t := range s.Tasks {
		row := taskDTO{Task: t, Kind: t.Kind, ParentID: t.ParentID,
			ExpectedBehavior: t.ExpectedBehavior, ActualBehavior: t.ActualBehavior, FixEvidence: t.FixEvidence}
		if t.Kind == domain.KindEpic {
			summary := &epicSummaryDTO{}
			for _, child := range s.Tasks {
				if child.ParentID != t.ID {
					continue
				}
				switch child.State {
				case domain.Backlog:
					summary.Backlog++
				case domain.Queued:
					summary.Queued++
				case domain.InProgress:
					summary.InProgress++
				case domain.Done:
					summary.Done++
				case domain.Cancelled:
					summary.Cancelled++
				}
			}
			row.Summary = summary
		}
		out.Tasks = append(out.Tasks, row)
	}
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
