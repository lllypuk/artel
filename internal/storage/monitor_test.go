package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/storage"
)

// ─── InsertMonitorCheck / GetMonitorHistory ───────────────────────────────────

func TestInsertMonitorCheck(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	c := storage.MonitorCheck{
		SiteURL:    "https://example.com",
		SiteName:   "Example",
		CheckedAt:  time.Now().UTC(),
		StatusCode: 200,
		ResponseMS: 42,
		IsUp:       true,
	}
	if err := db.InsertMonitorCheck(ctx, c); err != nil {
		t.Fatalf("insert check: %v", err)
	}
}

func TestGetMonitorHistory_OrderDesc(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := range 3 {
		err := db.InsertMonitorCheck(ctx, storage.MonitorCheck{
			SiteURL:    "https://example.com",
			SiteName:   "Example",
			CheckedAt:  base.Add(time.Duration(i) * time.Minute),
			StatusCode: 200,
			IsUp:       true,
		})
		if err != nil {
			t.Fatalf("insert check %d: %v", i, err)
		}
	}

	history, err := db.GetMonitorHistory(ctx, "https://example.com", 10)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(history))
	}
	// Should be ordered DESC: newest first
	if !history[0].CheckedAt.After(history[1].CheckedAt) {
		t.Errorf("expected desc order: history[0]=%v history[1]=%v", history[0].CheckedAt, history[1].CheckedAt)
	}
}

func TestGetMonitorHistory_Limit(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := range 5 {
		_ = db.InsertMonitorCheck(ctx, storage.MonitorCheck{
			SiteURL:   "https://example.com",
			CheckedAt: base.Add(time.Duration(i) * time.Minute),
			IsUp:      true,
		})
	}

	history, err := db.GetMonitorHistory(ctx, "https://example.com", 3)
	if err != nil {
		t.Fatalf("get history with limit: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 (limit), got %d", len(history))
	}
}

func TestGetMonitorHistory_OtherURLIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_ = db.InsertMonitorCheck(ctx, storage.MonitorCheck{
		SiteURL:   "https://a.com",
		CheckedAt: time.Now().UTC(),
		IsUp:      true,
	})
	_ = db.InsertMonitorCheck(ctx, storage.MonitorCheck{
		SiteURL:   "https://b.com",
		CheckedAt: time.Now().UTC(),
		IsUp:      false,
	})

	history, err := db.GetMonitorHistory(ctx, "https://a.com", 10)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1, got %d", len(history))
	}
	if history[0].SiteURL != "https://a.com" {
		t.Errorf("wrong site_url: %q", history[0].SiteURL)
	}
}

func TestGetMonitorHistory_Empty(t *testing.T) {
	db := openTestDB(t)
	history, err := db.GetMonitorHistory(context.Background(), "https://unknown.com", 10)
	if err != nil {
		t.Fatalf("get history for unknown site: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("expected 0, got %d", len(history))
	}
}

// ─── UpsertMonitorState / GetMonitorState ─────────────────────────────────────

func TestGetMonitorState_NoState(t *testing.T) {
	db := openTestDB(t)
	st, err := db.GetMonitorState(context.Background(), "https://new.com")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if st != nil {
		t.Fatalf("expected nil for unknown site, got %+v", st)
	}
}

func TestUpsertAndGetMonitorState_Up(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)

	st := storage.MonitorState{
		SiteURL:        "https://example.com",
		SiteName:       "Example",
		IsUp:           true,
		LastCheckedAt:  now,
		LastStatusCode: 200,
		LastResponseMS: 50,
		DownSinceAt:    nil,
	}
	if err := db.UpsertMonitorState(ctx, st); err != nil {
		t.Fatalf("upsert state: %v", err)
	}

	got, err := db.GetMonitorState(ctx, "https://example.com")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if got == nil {
		t.Fatal("expected state, got nil")
	}
	if !got.IsUp {
		t.Error("expected IsUp=true")
	}
	if got.LastStatusCode != 200 {
		t.Errorf("LastStatusCode: got %d, want 200", got.LastStatusCode)
	}
	if got.DownSinceAt != nil {
		t.Errorf("DownSinceAt: expected nil for up site, got %v", got.DownSinceAt)
	}
}

func TestUpsertMonitorState_DownSinceAt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	downSince := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)

	st := storage.MonitorState{
		SiteURL:       "https://down.com",
		IsUp:          false,
		LastCheckedAt: time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC),
		LastError:     "connection refused",
		DownSinceAt:   &downSince,
	}
	if err := db.UpsertMonitorState(ctx, st); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := db.GetMonitorState(ctx, "https://down.com")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if got.IsUp {
		t.Error("expected IsUp=false")
	}
	if got.DownSinceAt == nil {
		t.Fatal("expected DownSinceAt to be set")
	}
	if !got.DownSinceAt.Equal(downSince) {
		t.Errorf("DownSinceAt: got %v, want %v", got.DownSinceAt, downSince)
	}
}

func TestUpsertMonitorState_UpdatesClearDownSince(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	downSince := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)

	// First upsert: site is down
	_ = db.UpsertMonitorState(ctx, storage.MonitorState{
		SiteURL:       "https://flap.com",
		IsUp:          false,
		LastCheckedAt: downSince,
		DownSinceAt:   &downSince,
	})

	// Second upsert: site recovered
	_ = db.UpsertMonitorState(ctx, storage.MonitorState{
		SiteURL:        "https://flap.com",
		IsUp:           true,
		LastCheckedAt:  time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC),
		LastStatusCode: 200,
		DownSinceAt:    nil,
	})

	got, err := db.GetMonitorState(ctx, "https://flap.com")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !got.IsUp {
		t.Error("expected IsUp=true after recovery")
	}
	if got.DownSinceAt != nil {
		t.Errorf("expected DownSinceAt=nil after recovery, got %v", got.DownSinceAt)
	}
}

// ─── GetAllMonitorStates ──────────────────────────────────────────────────────

func TestGetAllMonitorStates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	_ = db.UpsertMonitorState(ctx, storage.MonitorState{SiteURL: "https://b.com", IsUp: true, LastCheckedAt: now})
	_ = db.UpsertMonitorState(ctx, storage.MonitorState{SiteURL: "https://a.com", IsUp: false, LastCheckedAt: now})

	states, err := db.GetAllMonitorStates(ctx)
	if err != nil {
		t.Fatalf("get all states: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %d", len(states))
	}
	// Should be ordered by site_url
	if states[0].SiteURL != "https://a.com" {
		t.Errorf("expected first site a.com, got %s", states[0].SiteURL)
	}
	if states[1].SiteURL != "https://b.com" {
		t.Errorf("expected second site b.com, got %s", states[1].SiteURL)
	}
}

func TestGetAllMonitorStates_Empty(t *testing.T) {
	db := openTestDB(t)
	states, err := db.GetAllMonitorStates(context.Background())
	if err != nil {
		t.Fatalf("get all states on empty db: %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("expected 0, got %d", len(states))
	}
}
