package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/auramate/auramate/internal/llm/tools"
)

func TestParsePlanSnapshotMetadata(t *testing.T) {
	payload, ok := parsePlanSnapshotMetadata(`{"type":"plan_snapshot","revision":3,"items":[]}`)
	if !ok {
		t.Fatalf("expected plan snapshot metadata to parse")
	}
	if payload["type"] != "plan_snapshot" {
		t.Fatalf("unexpected payload: %+v", payload)
	}

	if _, ok := parsePlanSnapshotMetadata(`{"type":"session_summary"}`); ok {
		t.Fatalf("expected non-plan metadata to be ignored")
	}
	if _, ok := parsePlanSnapshotMetadata(`not-json`); ok {
		t.Fatalf("expected invalid metadata to be ignored")
	}
}

func TestHandleSessionPlanSnapshotReturnsAuthoritativeActivePlan(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tool := tools.NewManagePlanTool(nil)
	ctx := tools.SetPlanScopeID(tools.SetContextValues(context.Background(), "session-1", "assistant-one"), "user-turn-12345678")
	if _, err := tool.Run(ctx, tools.ToolCall{Input: `{"action":"add","item":"restore plan in ui"}`}); err != nil {
		t.Fatalf("add plan task: %v", err)
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/session-1/plan-snapshot", nil)
	(&Server{}).handleSessionPlanSnapshot(recorder, req, "session-1")

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload["type"] != "plan_snapshot" {
		t.Fatalf("expected plan snapshot payload, got %+v", payload)
	}
	if payload["base_name"] != "TODO_userturn" {
		t.Fatalf("expected active plan base name, got %+v", payload)
	}

	if _, err := os.Stat(filepath.Join(tempDir, "TODO_userturn.json")); err != nil {
		t.Fatalf("expected authoritative plan file to exist: %v", err)
	}
}
