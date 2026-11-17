package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

func observerHash(v any) string {
	data, _ := json.Marshal(v) // All callers use concrete, marshalable DTOs.
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func observerLoad(ctx context.Context, tx *sql.Tx, id string) (observerRecord, error) {
	var data string
	var o observerRecord
	if err := tx.QueryRowContext(ctx, "SELECT record FROM observers WHERE id=?", id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return o, observerError("not_found")
		}
		return o, err
	}
	err := json.Unmarshal([]byte(data), &o)
	return o, err
}
func observerSave(ctx context.Context, tx *sql.Tx, o observerRecord) error {
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE observers SET record=? WHERE id=?", string(data), o.ID)
	return err
}
func observerAuth(ctx context.Context, tx *sql.Tx, verifier string) (observerRecord, error) {
	var id string
	if !observerVerifier(verifier) {
		return observerRecord{}, observerError("unauthorized")
	}
	err := tx.QueryRowContext(ctx, "SELECT id FROM observers WHERE verifier=?", verifier).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return observerRecord{}, observerError("unauthorized")
	}
	if err != nil {
		return observerRecord{}, err
	}
	o, err := observerLoad(ctx, tx, id)
	if err == nil && o.Revoked {
		return o, observerError("unauthorized")
	}
	return o, err
}
func observerRuntimeLoad(ctx context.Context, tx *sql.Tx, id, runtime string) (observerRuntime, error) {
	var retired int
	var r observerRuntime
	err := tx.QueryRowContext(ctx, "SELECT retired FROM observer_identities WHERE observer_id=? AND runtime_id=?", id, runtime).Scan(&retired)
	if errors.Is(err, sql.ErrNoRows) {
		return r, observerError("not_found")
	}
	if err != nil {
		return r, err
	}
	if retired != 0 {
		return r, observerError("runtime_retired")
	}
	var data string
	if err := tx.QueryRowContext(ctx, "SELECT record FROM observer_runtimes WHERE observer_id=? AND runtime_id=?", id, runtime).Scan(&data); err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(data), &r)
	return r, err
}
func observerRuntimeSave(ctx context.Context, tx *sql.Tx, id string, r observerRuntime) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO observer_runtimes(observer_id,runtime_id,record) VALUES(?,?,?) ON CONFLICT(observer_id,runtime_id) DO UPDATE SET record=excluded.record", id, r.Registration.RuntimeID, string(data))
	return err
}
func observerDatabase(ctx context.Context, tx *sql.Tx) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, "SELECT instance_id FROM metadata WHERE id=1").Scan(&id)
	return id, err
}
func observerOwnContext(ctx context.Context, tx *sql.Tx, o observerRecord, runtime string) (ObserverContext, error) {
	c := ObserverContext{ObserverID: o.ID, RuntimeID: runtime, Disposition: "candidate"}
	var err error
	c.DatabaseInstanceID, err = observerDatabase(ctx, tx)
	if err != nil {
		return c, err
	}
	r, err := observerRuntimeLoad(ctx, tx, o.ID, runtime)
	if observerIs(err, "not_found") {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	c.Generation = r.Generation
	c.Disposition = r.Disposition
	if o.Binding != nil && o.Binding.RuntimeID == runtime {
		c.Binding = o.Binding
	}
	return c, nil
}

// ObserverContext authenticates inside the same immediate transaction as lookup.
// The verifier is SHA-256 of the bearer, computed by the observer-only middleware.
func (s *Store) ObserverContext(ctx context.Context, verifier, runtime string) (ObserverContext, error) {
	if !observerUUID(runtime) {
		return ObserverContext{}, observerError("invalid_request")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ObserverContext{}, err
	}
	defer func() { _ = tx.Rollback() }()
	o, err := observerAuth(ctx, tx, verifier)
	if err != nil {
		return ObserverContext{}, err
	}
	c, err := observerOwnContext(ctx, tx, o, runtime)
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}

func (s *Store) RegisterObserver(ctx context.Context, verifier string, registration ObserverRegistration) (ObserverContext, error) {
	if err := registration.validate(); err != nil {
		return ObserverContext{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ObserverContext{}, err
	}
	defer func() { _ = tx.Rollback() }()
	o, err := observerAuth(ctx, tx, verifier)
	if err != nil {
		return ObserverContext{}, err
	}
	r, err := observerRuntimeLoad(ctx, tx, o.ID, registration.RuntimeID)
	switch {
	case err == nil:
		if observerHash(r.Registration) != observerHash(registration) {
			return ObserverContext{}, observerError("registration_conflict")
		}
	case observerIs(err, "not_found"):
		var payloads, identities int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM observer_runtimes WHERE observer_id=?", o.ID).Scan(&payloads); err != nil {
			return ObserverContext{}, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM observer_identities WHERE observer_id=?", o.ID).Scan(&identities); err != nil {
			return ObserverContext{}, err
		}
		if identities >= 1024 {
			return ObserverContext{}, observerCapacity("runtime_identities")
		}
		if payloads >= 8 {
			return ObserverContext{}, observerCapacity("runtime_payloads")
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO observer_identities(observer_id,runtime_id) VALUES(?,?)", o.ID, registration.RuntimeID); err != nil {
			return ObserverContext{}, err
		}
		r = observerRuntime{ObserverRuntimeProjection: ObserverRuntimeProjection{Registration: registration, Disposition: "candidate", Activity: "unknown", Freshness: "unknown", Lifecycle: "running", Provider: registration.Provider, Model: registration.Model}}
		if err := observerRuntimeSave(ctx, tx, o.ID, r); err != nil {
			return ObserverContext{}, err
		}
	default:
		return ObserverContext{}, err
	}
	c, err := observerOwnContext(ctx, tx, o, registration.RuntimeID)
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}

func (s *Store) ReportObserver(ctx context.Context, verifier string, report ObserverReport) (ObserverReportResult, error) {
	result := ObserverReportResult{RuntimeID: report.RuntimeID, Generation: report.Generation, ReportSeq: report.ReportSeq, Result: "accepted"}
	if err := report.validate(); err != nil {
		return result, err
	}
	// Commit and the process-local monotonic timestamp form one projection boundary.
	s.observerMu.Lock()
	defer s.observerMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	o, err := observerAuth(ctx, tx, verifier)
	if err != nil {
		return result, err
	}
	r, err := observerRuntimeLoad(ctx, tx, o.ID, report.RuntimeID)
	if err != nil {
		return result, err
	}
	if r.Disposition == "superseded" {
		return result, observerError("runtime_superseded")
	}
	if r.Generation != report.Generation {
		return result, observerError("generation_conflict")
	}
	hash := observerHash(report)
	if report.ReportSeq <= r.Seq {
		if report.ReportSeq != r.Seq || r.Hash != hash {
			return result, observerError("sequence_conflict")
		}
		result.Result = "duplicate"
		return result, tx.Commit()
	}
	now := s.observerClock()
	r.Seq = report.ReportSeq
	r.Hash = hash
	r.Activity = report.Activity
	r.ToolCount = report.ToolCount
	r.Lifecycle = report.Lifecycle
	r.Provider = report.Provider
	r.Model = report.Model
	r.LastSeen = now.UTC().Format(time.RFC3339Nano)
	if err := observerRuntimeSave(ctx, tx, o.ID, r); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	s.observerSeen[o.ID+"/"+report.RuntimeID] = now
	return result, nil
}

// ObserverOwnerMutation is owner-only. Action is provision/bind/revoke/prune;
// Bind uses empty runtime+project for unbind. Receipts are in a separate namespace
// from domain receipts and scoped to the target observer principal.
type ObserverOwnerMutation struct {
	ObserverID      string
	Key             string
	Action          string
	ExpectedVersion int64
	Label           string
	Verifier        string
	RuntimeID       string
	ProjectID       string
}

func (m ObserverOwnerMutation) validate() error {
	if !observerUUID(m.ObserverID) || !observerUUID(m.Key) || m.ExpectedVersion < 0 || m.ExpectedVersion > observerMaxCounter {
		return observerError("invalid_request")
	}
	switch m.Action {
	case "provision":
		if !observerText(m.Label, 128, false) || !observerVerifier(m.Verifier) {
			return observerError("invalid_request")
		}
	case "bind":
		if (m.RuntimeID != "" || m.ProjectID != "") && (!observerUUID(m.RuntimeID) || !observerUUID(m.ProjectID)) {
			return observerError("invalid_request")
		}
	case "revoke", "prune":
	default:
		return observerError("invalid_request")
	}
	return nil
}
func (s *Store) MutateObserver(ctx context.Context, m ObserverOwnerMutation) (ObserverOwnerResult, error) {
	var result ObserverOwnerResult
	if err := m.validate(); err != nil {
		return result, err
	}
	s.observerMu.Lock()
	defer s.observerMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	var hash, data string
	err = tx.QueryRowContext(ctx, "SELECT request_hash,result FROM observer_receipts WHERE principal=? AND key=?", m.ObserverID, m.Key).Scan(&hash, &data)
	if err == nil {
		if hash != observerHash(m) {
			return result, observerError("idempotency_conflict")
		}
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			return result, err
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	o, oldGeneration, pruned, err := observerOwnerApply(ctx, tx, m)
	if err != nil {
		return result, err
	}
	result.PrunedCount = pruned
	result.DatabaseInstanceID, err = observerDatabase(ctx, tx)
	if err != nil {
		return result, err
	}
	result.ObserverID = o.ID
	result.Version = o.Version
	result.Generation = o.Generation
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO observer_receipts(principal,key,request_hash,result) VALUES(?,?,?,?)", o.ID, m.Key, observerHash(m), string(encoded)); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO observer_audit(observer_id,action,timestamp) VALUES(?,?,?)", o.ID, m.Action, s.observerClock().UTC().Format(time.RFC3339Nano)); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM observer_audit WHERE observer_id=? AND seq NOT IN (SELECT seq FROM observer_audit WHERE observer_id=? ORDER BY seq DESC LIMIT 256)", o.ID, o.ID); err != nil {
		return result, err
	}
	live := map[string]bool{}
	if m.Action == "prune" {
		runtimes, err := observerRuntimes(ctx, tx, o.ID)
		if err != nil {
			return result, err
		}
		for _, r := range runtimes {
			live[o.ID+"/"+r.Registration.RuntimeID] = true
		}
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	// Invalidate only the new generation, revoked credentials and removed payloads.
	// An unrelated candidate's accepted report retains its own freshness.
	s.observerInvalidate(o, oldGeneration, m.Action, live)
	return result, nil
}

func (s *Store) observerInvalidate(o observerRecord, oldGeneration int64, action string, live map[string]bool) {
	for key := range s.observerSeen {
		if len(key) <= len(o.ID) || key[:len(o.ID)+1] != o.ID+"/" {
			continue
		}
		newBinding := o.Generation != oldGeneration && o.Binding != nil && key == o.ID+"/"+o.Binding.RuntimeID
		if o.Revoked || newBinding || (action == "prune" && !live[key]) {
			delete(s.observerSeen, key)
		}
	}
}

func observerProvision(ctx context.Context, tx *sql.Tx, m ObserverOwnerMutation) (observerRecord, error) {
	o, err := observerLoad(ctx, tx, m.ObserverID)
	if err == nil {
		return o, observerError("idempotency_conflict")
	}
	if !observerIs(err, "not_found") {
		return o, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM observers").Scan(&count); err != nil {
		return o, err
	}
	if count >= 256 {
		return o, observerCapacity("observers")
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM observers WHERE verifier=?", m.Verifier).Scan(&count); err != nil {
		return o, err
	}
	if count != 0 {
		return o, observerError("idempotency_conflict")
	}
	o = observerRecord{ID: m.ObserverID, Label: m.Label, Version: 1}
	data, err := json.Marshal(o)
	if err != nil {
		return o, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO observers(id,verifier,record) VALUES(?,?,?)", o.ID, m.Verifier, string(data))
	return o, err
}
func observerOwnerApply(ctx context.Context, tx *sql.Tx, m ObserverOwnerMutation) (observerRecord, int64, int, error) {
	if m.Action == "provision" {
		o, err := observerProvision(ctx, tx, m)
		return o, 0, 0, err
	}
	o, err := observerLoad(ctx, tx, m.ObserverID)
	if err != nil {
		return o, 0, 0, err
	}
	oldGeneration := o.Generation
	if o.Version != m.ExpectedVersion {
		return o, 0, 0, observerError("version_conflict")
	}
	if o.Version == observerMaxCounter || o.Generation == observerMaxCounter {
		return o, 0, 0, observerError("capacity_exceeded")
	}
	pruned := 0
	switch m.Action {
	case "bind":
		if o.Revoked {
			return o, 0, 0, observerError("unauthorized")
		}
		err = observerBind(ctx, tx, &o, m)
	case "revoke":
		o.Revoked = true
		o.Generation++
	case "prune":
		pruned, err = observerPrune(ctx, tx, o)
	}
	if err != nil {
		return o, 0, 0, err
	}
	o.Version++
	return o, oldGeneration, pruned, observerSave(ctx, tx, o)
}

func observerBind(ctx context.Context, tx *sql.Tx, o *observerRecord, m ObserverOwnerMutation) error {
	var next observerRuntime
	if m.RuntimeID != "" {
		var archived int
		err := tx.QueryRowContext(ctx, "SELECT archived FROM projects WHERE id=?", m.ProjectID).Scan(&archived)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && archived != 0) {
			return observerError("project_unavailable")
		}
		if err != nil {
			return err
		}
		next, err = observerRuntimeLoad(ctx, tx, o.ID, m.RuntimeID)
		if err != nil {
			return err
		}
		if next.Disposition == "superseded" {
			return observerError("runtime_superseded")
		}
	}
	if o.Binding == nil && m.RuntimeID == "" {
		return nil
	}
	if o.Binding != nil && o.Binding.RuntimeID == m.RuntimeID && o.Binding.ProjectID == m.ProjectID {
		return nil
	}
	if o.Binding != nil && o.Binding.RuntimeID != m.RuntimeID {
		previous, err := observerRuntimeLoad(ctx, tx, o.ID, o.Binding.RuntimeID)
		if err != nil {
			return err
		}
		previous.Disposition = "superseded"
		if err := observerRuntimeSave(ctx, tx, o.ID, previous); err != nil {
			return err
		}
	}
	o.Generation++
	o.Binding = nil
	if m.RuntimeID != "" {
		o.Binding = &ObserverBinding{RuntimeID: m.RuntimeID, ProjectID: m.ProjectID}
		next.Disposition = "bound"
		next.Generation = o.Generation
		next.Seq = 0
		next.Hash = ""
		next.LastSeen = ""
		next.Activity = "unknown"
		next.ToolCount = 0
		next.Lifecycle = "running"
		if err := observerRuntimeSave(ctx, tx, o.ID, next); err != nil {
			return err
		}
	}
	return nil
}
func observerPrune(ctx context.Context, tx *sql.Tx, o observerRecord) (int, error) {
	runtimes, err := observerRuntimes(ctx, tx, o.ID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, r := range runtimes {
		id := r.Registration.RuntimeID
		if o.Binding != nil && o.Binding.RuntimeID == id {
			continue
		}
		if r.Disposition != "candidate" && r.Disposition != "superseded" && r.Lifecycle != "stopped" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM observer_runtimes WHERE observer_id=? AND runtime_id=?", o.ID, id); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE observer_identities SET retired=1 WHERE observer_id=? AND runtime_id=?", o.ID, id); err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}

func (s *Store) Observers(ctx context.Context) (ObserverRegistry, error) {
	s.observerMu.Lock()
	defer s.observerMu.Unlock()
	registry := ObserverRegistry{Observers: []ObserverProjection{}, ServerTime: s.observerClock().UTC().Format(time.RFC3339Nano)}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry, err
	}
	defer func() { _ = tx.Rollback() }()
	registry.DatabaseInstanceID, err = observerDatabase(ctx, tx)
	if err != nil {
		return registry, err
	}
	var records []observerRecord
	if err := readRecords(ctx, tx, "SELECT record FROM observers ORDER BY id", &records); err != nil {
		return registry, err
	}
	for _, o := range records {
		p := ObserverProjection{ObserverID: o.ID, Label: o.Label, Version: o.Version, Generation: o.Generation, Revoked: o.Revoked, Binding: o.Binding, Runtimes: []ObserverRuntimeProjection{}}
		if err := tx.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(retired),0) FROM observer_identities WHERE observer_id=?", o.ID).Scan(&p.IdentityCount, &p.RetiredCount); err != nil {
			return registry, err
		}
		runtimes, err := observerRuntimes(ctx, tx, o.ID)
		if err != nil {
			return registry, err
		}
		for _, r := range runtimes {
			r.Freshness = "unknown"
			if r.LastSeen != "" {
				r.Freshness = "stale"
			}
			if seen, ok := s.observerSeen[o.ID+"/"+r.Registration.RuntimeID]; ok && s.observerClock().Sub(seen) < 20*time.Second {
				r.Freshness = "fresh"
			}
			if r.Lifecycle == "stopped" {
				r.Freshness = "stopped"
			}
			if o.Revoked {
				r.Disposition = "revoked"
				if r.Freshness == "fresh" {
					r.Freshness = "stale"
				}
			}
			p.Runtimes = append(p.Runtimes, r.ObserverRuntimeProjection)
		}
		registry.Observers = append(registry.Observers, p)
	}
	return registry, tx.Commit()
}
func observerRuntimes(ctx context.Context, tx *sql.Tx, id string) ([]observerRuntime, error) {
	rows, err := tx.QueryContext(ctx, "SELECT record FROM observer_runtimes WHERE observer_id=? ORDER BY runtime_id", id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	runtimes := []observerRuntime{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var r observerRuntime
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return nil, err
		}
		runtimes = append(runtimes, r)
	}
	return runtimes, rows.Err()
}
