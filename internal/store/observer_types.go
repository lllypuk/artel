package store

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const observerMaxCounter int64 = 9007199254740991

// ObserverError carries only a fixed code and optional quota, never caller data.
type ObserverError struct {
	Code     string
	Capacity string
}

func (e *ObserverError) Error() string { return e.Code }
func observerError(code string) error  { return &ObserverError{Code: code} }
func observerCapacity(quota string) error {
	return &ObserverError{Code: "capacity_exceeded", Capacity: quota}
}

type ObserverBinding struct {
	RuntimeID string `json:"runtime_id"`
	ProjectID string `json:"project_id"`
}
type ObserverTerminalHint struct {
	WindowID  string `json:"window_id"`
	SessionID string `json:"session_id"`
	PaneID    string `json:"pane_id"`
}
type ObserverRegistration struct {
	RuntimeID     string                `json:"runtime_id"`
	PiSessionID   string                `json:"pi_session_id"`
	Reason        string                `json:"reason"`
	PiVersion     string                `json:"pi_version"`
	BridgeVersion string                `json:"bridge_version"`
	Cwd           string                `json:"cwd"`
	Provider      string                `json:"provider,omitempty"`
	Model         string                `json:"model,omitempty"`
	TerminalHint  *ObserverTerminalHint `json:"terminal_hint,omitempty"`
}
type ObserverReport struct {
	RuntimeID  string `json:"runtime_id"`
	Generation int64  `json:"generation"`
	ReportSeq  int64  `json:"report_seq"`
	Activity   string `json:"activity"`
	ToolCount  int    `json:"tool_count"`
	Lifecycle  string `json:"lifecycle"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
}
type ObserverContext struct {
	DatabaseInstanceID string           `json:"database_instance_id"`
	ObserverID         string           `json:"observer_id"`
	RuntimeID          string           `json:"runtime_id"`
	Generation         int64            `json:"generation"`
	Disposition        string           `json:"disposition"`
	Binding            *ObserverBinding `json:"binding,omitempty"`
}
type ObserverReportResult struct {
	RuntimeID  string `json:"runtime_id"`
	Generation int64  `json:"generation"`
	ReportSeq  int64  `json:"report_seq"`
	Result     string `json:"result"`
}
type ObserverOwnerResult struct {
	DatabaseInstanceID string `json:"database_instance_id"`
	ObserverID         string `json:"observer_id"`
	Version            int64  `json:"version"`
	Generation         int64  `json:"generation"`
	PrunedCount        int    `json:"pruned_count,omitempty"`
}
type ObserverRuntimeProjection struct {
	Registration ObserverRegistration `json:"registration"`
	Disposition  string               `json:"disposition"`
	Generation   int64                `json:"generation"`
	Activity     string               `json:"activity"`
	Freshness    string               `json:"freshness"`
	ToolCount    int                  `json:"tool_count"`
	Lifecycle    string               `json:"lifecycle"`
	LastSeen     string               `json:"last_seen,omitempty"`
	Provider     string               `json:"provider,omitempty"`
	Model        string               `json:"model,omitempty"`
}
type ObserverProjection struct {
	ObserverID    string                      `json:"observer_id"`
	Label         string                      `json:"label"`
	Version       int64                       `json:"version"`
	Generation    int64                       `json:"generation"`
	Revoked       bool                        `json:"revoked"`
	IdentityCount int                         `json:"identity_count"`
	RetiredCount  int                         `json:"retired_count"`
	Binding       *ObserverBinding            `json:"binding,omitempty"`
	Runtimes      []ObserverRuntimeProjection `json:"runtimes"`
}
type ObserverRegistry struct {
	DatabaseInstanceID string               `json:"database_instance_id"`
	ServerTime         string               `json:"server_time"`
	Observers          []ObserverProjection `json:"observers"`
}

// These records are private to SQLite; projection never serializes verifier/hash.
type observerRecord struct {
	ID         string
	Label      string
	Version    int64
	Generation int64
	Revoked    bool
	Binding    *ObserverBinding
}
type observerRuntime struct {
	ObserverRuntimeProjection
	Seq  int64
	Hash string
}

func observerUUID(s string) bool { u, err := uuid.Parse(s); return err == nil && u.String() == s }
func observerText(s string, max int, optional bool) bool {
	if s == "" {
		return optional
	}
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && !strings.ContainsFunc(s, unicode.IsControl)
}
func observerVerifier(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && strings.ToLower(s) == s
}
func (r ObserverRegistration) validate() error {
	validReason := r.Reason == "startup" || r.Reason == "reload" || r.Reason == "new" || r.Reason == "resume" || r.Reason == "fork"
	if !observerUUID(r.RuntimeID) || !observerUUID(r.PiSessionID) || !validReason || !observerText(r.PiVersion, 256, false) || !observerText(r.BridgeVersion, 256, false) || !observerText(r.Cwd, 4096, false) || !observerText(r.Provider, 256, true) || !observerText(r.Model, 256, true) {
		return observerError("invalid_request")
	}
	if h := r.TerminalHint; h != nil && (!observerUUID(h.WindowID) || !observerUUID(h.SessionID) || !observerUUID(h.PaneID)) {
		return observerError("invalid_request")
	}
	return observerSize(r)
}
func (r ObserverReport) validate() error {
	activity := r.Activity == "unknown" || r.Activity == "idle" || r.Activity == "working" || r.Activity == "waiting_user"
	if !observerUUID(r.RuntimeID) || r.Generation < 0 || r.Generation > observerMaxCounter || r.ReportSeq < 1 || r.ReportSeq > observerMaxCounter || !activity || r.ToolCount < 0 || r.ToolCount > 65535 || (r.Lifecycle != "running" && r.Lifecycle != "stopped") || !observerText(r.Provider, 256, true) || !observerText(r.Model, 256, true) {
		return observerError("invalid_request")
	}
	return observerSize(r)
}
func observerSize(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > 16384 {
		return observerError("invalid_request")
	}
	return nil
}
func observerIs(err error, code string) bool {
	var e *ObserverError
	return errors.As(err, &e) && e.Code == code
}
