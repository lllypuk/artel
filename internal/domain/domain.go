// Package domain defines the manual panel's data and invariants, without I/O.
package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type State string

const (
	Backlog    State = "backlog"
	Queued     State = "queued"
	InProgress State = "in_progress"
	Done       State = "done"
	Cancelled  State = "cancelled"
)

type Project struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RootPath     string `json:"root_path"`
	Description  string `json:"description"`
	Version      int64  `json:"version"`
	QueueVersion int64  `json:"queue_version"`
	Archived     bool   `json:"archived"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type Task struct {
	ID                 string `json:"id"`
	ProjectID          string `json:"project_id"`
	Title              string `json:"title"`
	Description        string `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	State              State  `json:"state"`
	Version            int64  `json:"version"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// Error is safe for transport: no SQL, credentials or filesystem paths.
type Error struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	CurrentVersion int64  `json:"current_version,omitempty"`
}

func (e *Error) Error() string      { return e.Message }
func Invalid(message string) error  { return &Error{Code: "invalid_request", Message: message} }
func Conflict(message string) error { return &Error{Code: "conflict", Message: message} }
func CheckVersion(expected, actual int64) error {
	if expected < 1 {
		return Invalid("positive expected version required")
	}
	if expected != actual {
		return &Error{Code: "conflict", Message: "version conflict", CurrentVersion: actual}
	}
	return nil
}

// ValidateText counts Unicode characters; whitespace-only required fields are invalid.
func ValidateText(s string, min, max int) error {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max || (min > 0 && utf8.RuneCountInString(strings.TrimSpace(s)) < min) {
		return Invalid(fmt.Sprintf("text must contain %d to %d characters", min, max))
	}
	return nil
}

func CanTransition(from, to State) bool {
	switch from {
	case Backlog:
		return to == Cancelled
	case Queued:
		return to == InProgress || to == Cancelled
	case InProgress:
		return to == Done || to == Backlog || to == Cancelled
	case Done, Cancelled:
		return to == Backlog
	default:
		return false
	}
}
