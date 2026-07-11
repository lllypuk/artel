package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/artel/internal/config"
	"github.com/lllypuk/artel/internal/storage"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func openMonitorTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestMonitorTool(t *testing.T, sites []config.MonitorSite) *MonitorTool {
	t.Helper()
	db := openMonitorTestDB(t)
	return NewMonitorTool(MonitorToolConfig{Sites: sites, TimeoutSeconds: 5}, db)
}

func monitorExecute(t *testing.T, tool *MonitorTool, action, siteURL string, limit int) string {
	t.Helper()
	args := map[string]any{"action": action}
	if siteURL != "" {
		args["url"] = siteURL
	}
	if limit > 0 {
		args["limit"] = limit
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute(%s): %v", action, err)
	}
	return result
}

// ─── Tool interface ───────────────────────────────────────────────────────────

func TestMonitorToolInterface(t *testing.T) {
	tool := newTestMonitorTool(t, nil)

	if tool.Name() != "monitor" {
		t.Errorf("Name: got %q, want %q", tool.Name(), "monitor")
	}
	if tool.Description() == "" {
		t.Error("Description: empty")
	}

	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters type: got %v", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("parameters.properties is not a map")
	}
	for _, key := range []string{"action", "url", "limit"} {
		if _, exists := props[key]; !exists {
			t.Errorf("parameters.properties missing key %q", key)
		}
	}
	required, ok := params["required"].([]string)
	if !ok {
		t.Fatal("parameters.required is not []string")
	}
	if len(required) != 1 || required[0] != "action" {
		t.Errorf("required: got %v, want [action]", required)
	}
}

// ─── action: invalid ─────────────────────────────────────────────────────────

func TestMonitorToolExecute_InvalidAction(t *testing.T) {
	tool := newTestMonitorTool(t, nil)
	raw, _ := json.Marshal(map[string]any{"action": "unknown"})
	_, err := tool.Execute(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("error message: %q", err.Error())
	}
}

func TestMonitorToolExecute_BadJSON(t *testing.T) {
	tool := newTestMonitorTool(t, nil)
	_, err := tool.Execute(context.Background(), []byte("{bad json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ─── action: list ─────────────────────────────────────────────────────────────

func TestMonitorToolList_NoSites(t *testing.T) {
	tool := newTestMonitorTool(t, nil)
	result := monitorExecute(t, tool, "list", "", 0)
	if !strings.Contains(result, "Нет настроенных сайтов") {
		t.Errorf("expected no-sites message, got: %q", result)
	}
}

func TestMonitorToolList_NoData(t *testing.T) {
	sites := []config.MonitorSite{{URL: "https://example.com", Name: "Example"}}
	tool := newTestMonitorTool(t, sites)

	result := monitorExecute(t, tool, "list", "", 0)
	if !strings.Contains(result, "https://example.com") {
		t.Errorf("expected site URL in list, got: %q", result)
	}
	if !strings.Contains(result, "нет данных") {
		t.Errorf("expected 'no data' message for unchecked site, got: %q", result)
	}
}

func TestMonitorToolList_WithState(t *testing.T) {
	url := "https://example.com"
	sites := []config.MonitorSite{{URL: url, Name: "Example"}}
	tool := newTestMonitorTool(t, sites)

	_ = tool.db.UpsertMonitorState(context.Background(), storage.MonitorState{
		SiteURL:        url,
		SiteName:       "Example",
		IsUp:           true,
		LastCheckedAt:  time.Now().UTC(),
		LastStatusCode: 200,
		LastResponseMS: 123,
	})

	result := monitorExecute(t, tool, "list", "", 0)
	if !strings.Contains(result, "✅") {
		t.Errorf("expected ✅ for up site, got: %q", result)
	}
	if !strings.Contains(result, "200") {
		t.Errorf("expected HTTP 200 in list, got: %q", result)
	}
}

func TestMonitorToolList_DownSite(t *testing.T) {
	siteURL := "https://down.com"
	sites := []config.MonitorSite{{URL: siteURL, Name: "Down"}}
	tool := newTestMonitorTool(t, sites)

	_ = tool.db.UpsertMonitorState(context.Background(), storage.MonitorState{
		SiteURL:       siteURL,
		IsUp:          false,
		LastCheckedAt: time.Now().UTC(),
		LastError:     "connection refused",
	})

	result := monitorExecute(t, tool, "list", "", 0)
	if !strings.Contains(result, "❌") {
		t.Errorf("expected ❌ for down site, got: %q", result)
	}
	if !strings.Contains(result, "connection refused") {
		t.Errorf("expected error text, got: %q", result)
	}
}

// ─── action: check ────────────────────────────────────────────────────────────

func TestMonitorToolCheck_NoSites(t *testing.T) {
	tool := newTestMonitorTool(t, nil)
	result := monitorExecute(t, tool, "check", "", 0)
	if !strings.Contains(result, "Нет сайтов") {
		t.Errorf("expected no-sites message, got: %q", result)
	}
}

func TestMonitorToolCheck_HTTP200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sites := []config.MonitorSite{{URL: srv.URL, Name: "TestSite"}}
	tool := newTestMonitorTool(t, sites)

	result := monitorExecute(t, tool, "check", "", 0)
	if !strings.Contains(result, "✅") {
		t.Errorf("expected ✅ for 200 response, got: %q", result)
	}
	if !strings.Contains(result, "200") {
		t.Errorf("expected HTTP status 200 in output, got: %q", result)
	}
}

func TestMonitorToolCheck_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sites := []config.MonitorSite{{URL: srv.URL, Name: "BrokenSite"}}
	tool := newTestMonitorTool(t, sites)

	result := monitorExecute(t, tool, "check", "", 0)
	if !strings.Contains(result, "❌") {
		t.Errorf("expected ❌ for 500 response, got: %q", result)
	}
}

func TestMonitorToolCheck_UpdatesState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sites := []config.MonitorSite{{URL: srv.URL, Name: "Site"}}
	tool := newTestMonitorTool(t, sites)

	// Before check: no state
	st, _ := tool.db.GetMonitorState(context.Background(), srv.URL)
	if st != nil {
		t.Fatal("expected nil state before check")
	}

	monitorExecute(t, tool, "check", "", 0)

	// After check: state should be updated
	st, err := tool.db.GetMonitorState(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if st == nil {
		t.Fatal("expected state after check, got nil")
	}
	if !st.IsUp {
		t.Error("expected IsUp=true after 200 response")
	}
}

// ─── action: history ─────────────────────────────────────────────────────────

func TestMonitorToolHistory_NoURL(t *testing.T) {
	tool := newTestMonitorTool(t, nil)
	raw, _ := json.Marshal(map[string]any{"action": "history"})
	_, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute history with no sites: %v", err)
	}
}

func TestMonitorToolHistory_Empty(t *testing.T) {
	sites := []config.MonitorSite{{URL: "https://example.com", Name: "E"}}
	tool := newTestMonitorTool(t, sites)

	result := monitorExecute(t, tool, "history", "https://example.com", 0)
	if !strings.Contains(result, "Нет истории") {
		t.Errorf("expected no-history message, got: %q", result)
	}
}

func TestMonitorToolHistory_ShowsEntries(t *testing.T) {
	siteURL := "https://example.com"
	sites := []config.MonitorSite{{URL: siteURL, Name: "E"}}
	tool := newTestMonitorTool(t, sites)

	for range 3 {
		_ = tool.db.InsertMonitorCheck(context.Background(), storage.MonitorCheck{
			SiteURL:    siteURL,
			SiteName:   "E",
			CheckedAt:  time.Now().UTC(),
			StatusCode: 200,
			IsUp:       true,
		})
	}

	result := monitorExecute(t, tool, "history", siteURL, 5)
	if !strings.Contains(result, "История проверок") {
		t.Errorf("expected history header, got: %q", result)
	}
	if !strings.Contains(result, "✅") {
		t.Errorf("expected ✅ entries, got: %q", result)
	}
}

// ─── targetSites ─────────────────────────────────────────────────────────────

func TestTargetSites_AllWhenEmpty(t *testing.T) {
	sites := []config.MonitorSite{
		{URL: "https://a.com", Name: "A"},
		{URL: "https://b.com", Name: "B"},
	}
	tool := newTestMonitorTool(t, sites)

	got := tool.targetSites("")
	if len(got) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(got))
	}
}

func TestTargetSites_FilterByURL(t *testing.T) {
	sites := []config.MonitorSite{
		{URL: "https://a.com", Name: "A"},
		{URL: "https://b.com", Name: "B"},
	}
	tool := newTestMonitorTool(t, sites)

	got := tool.targetSites("https://a.com")
	if len(got) != 1 {
		t.Fatalf("expected 1 site, got %d", len(got))
	}
	if got[0].URL != "https://a.com" {
		t.Errorf("wrong URL: %q", got[0].URL)
	}
}

func TestTargetSites_AdHocURL(t *testing.T) {
	tool := newTestMonitorTool(t, nil)
	got := tool.targetSites("https://adhoc.com")
	if len(got) != 1 {
		t.Fatalf("expected 1 ad-hoc site, got %d", len(got))
	}
	if got[0].URL != "https://adhoc.com" {
		t.Errorf("wrong URL: %q", got[0].URL)
	}
}

// ─── formatting helpers ───────────────────────────────────────────────────────

func TestWriteCheckResult_Up(t *testing.T) {
	var sb strings.Builder
	writeCheckResult(&sb, storage.MonitorCheck{
		SiteURL:    "https://example.com",
		SiteName:   "Example",
		StatusCode: 200,
		ResponseMS: 55,
		IsUp:       true,
	})
	out := sb.String()
	if !strings.Contains(out, "✅") {
		t.Errorf("expected ✅: %q", out)
	}
	if !strings.Contains(out, "200") {
		t.Errorf("expected 200: %q", out)
	}
}

func TestWriteCheckResult_Down(t *testing.T) {
	var sb strings.Builder
	writeCheckResult(&sb, storage.MonitorCheck{
		SiteURL:   "https://example.com",
		SiteName:  "Example",
		IsUp:      false,
		ErrorText: "timeout",
	})
	out := sb.String()
	if !strings.Contains(out, "❌") {
		t.Errorf("expected ❌: %q", out)
	}
	if !strings.Contains(out, "timeout") {
		t.Errorf("expected error text: %q", out)
	}
}

// ─── monitorDownSince ─────────────────────────────────────────────────────────

func TestMonitorDownSince_SiteUp(t *testing.T) {
	result := monitorDownSince(true, time.Now(), nil)
	if result != nil {
		t.Errorf("expected nil for up site, got %v", result)
	}
}

func TestMonitorDownSince_FirstDown(t *testing.T) {
	now := time.Now()
	result := monitorDownSince(false, now, nil)
	if result == nil {
		t.Fatal("expected non-nil for first down")
	}
	if !result.Equal(now) {
		t.Errorf("DownSince: got %v, want %v", result, now)
	}
}

func TestMonitorDownSince_StaysDown(t *testing.T) {
	original := time.Now().Add(-10 * time.Minute)
	prev := &storage.MonitorState{IsUp: false, DownSinceAt: &original}

	result := monitorDownSince(false, time.Now(), prev)
	if result == nil {
		t.Fatal("expected non-nil for continued downtime")
	}
	if !result.Equal(original) {
		t.Errorf("expected original down_since_at %v, got %v", original, result)
	}
}

func TestMonitorDownSince_WasPreviouslyUp(t *testing.T) {
	now := time.Now()
	prev := &storage.MonitorState{IsUp: true, DownSinceAt: nil}

	result := monitorDownSince(false, now, prev)
	if result == nil {
		t.Fatal("expected non-nil when transitioning to down")
	}
	if !result.Equal(now) {
		t.Errorf("got %v, want %v", result, now)
	}
}

// ─── writeSiteStatus ─────────────────────────────────────────────────────────

func TestWriteSiteStatus_NoState(t *testing.T) {
	var sb strings.Builder
	writeSiteStatus(&sb, config.MonitorSite{URL: "https://new.com", Name: "New"}, storage.MonitorState{})
	out := sb.String()
	if !strings.Contains(out, "нет данных") {
		t.Errorf("expected 'no data', got: %q", out)
	}
}

func TestWriteSiteStatus_Up(t *testing.T) {
	var sb strings.Builder
	writeSiteStatus(&sb,
		config.MonitorSite{URL: "https://ok.com", Name: "OK"},
		storage.MonitorState{
			SiteURL:        "https://ok.com",
			IsUp:           true,
			LastCheckedAt:  time.Now().UTC(),
			LastStatusCode: 200,
			LastResponseMS: 30,
		},
	)
	out := sb.String()
	if !strings.Contains(out, "✅") {
		t.Errorf("expected ✅: %q", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d мс", 30)) {
		t.Errorf("expected response time: %q", out)
	}
}

func TestWriteSiteStatus_Down(t *testing.T) {
	var sb strings.Builder
	writeSiteStatus(&sb,
		config.MonitorSite{URL: "https://down.com", Name: "Down"},
		storage.MonitorState{
			SiteURL:       "https://down.com",
			IsUp:          false,
			LastCheckedAt: time.Now().UTC(),
			LastError:     "connection refused",
		},
	)
	out := sb.String()
	if !strings.Contains(out, "❌") {
		t.Errorf("expected ❌: %q", out)
	}
	if !strings.Contains(out, "connection refused") {
		t.Errorf("expected error text: %q", out)
	}
}
