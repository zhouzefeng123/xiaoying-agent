package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestManagePlanPersistsStableIDsAndDependencies(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tool := NewManagePlanTool(nil)
	ctx := context.Background()

	if _, err := tool.Run(ctx, ToolCall{Input: `{"action":"add","item":"prepare project"}`}); err != nil {
		t.Fatalf("add first task: %v", err)
	}
	if _, err := tool.Run(ctx, ToolCall{Input: `{"action":"add","item":"implement backend","depends_on":[1]}`}); err != nil {
		t.Fatalf("add second task: %v", err)
	}

	plans := readPlanJSON(t, filepath.Join(tempDir, "TODO.json"))
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if strings.TrimSpace(plans[0].ID) == "" || strings.TrimSpace(plans[1].ID) == "" {
		t.Fatalf("expected stable ids to be assigned, got %+v", plans)
	}
	if len(plans[1].DependsOnIDs) != 1 || plans[1].DependsOnIDs[0] != plans[0].ID {
		t.Fatalf("expected dependency to be stored by stable id, got %+v", plans[1])
	}

	listResp, err := tool.Run(ctx, ToolCall{Input: `{"action":"list"}`})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if !strings.Contains(listResp.Content, "[ID: ") {
		t.Fatalf("expected list output to expose stable ids, got %q", listResp.Content)
	}

	if _, err := tool.Run(ctx, ToolCall{Input: `{"action":"complete","id":"` + planIDDisplay(plans[0].ID) + `"}`}); err != nil {
		t.Fatalf("complete first task by id: %v", err)
	}

	plans = readPlanJSON(t, filepath.Join(tempDir, "TODO.json"))
	if len(plans) != 2 || plans[0].Status != "completed" || plans[1].Status != "pending" {
		t.Fatalf("expected completed task to remain visible until explicit archive, got %+v", plans)
	}

	if _, err := tool.Run(ctx, ToolCall{Input: `{"action":"complete","id":"` + planIDDisplay(plans[1].ID) + `"}`}); err != nil {
		t.Fatalf("complete second task by id: %v", err)
	}
	archiveResp, err := tool.Run(ctx, ToolCall{Input: `{"action":"archive"}`})
	if err != nil {
		t.Fatalf("archive completed tasks: %v", err)
	}
	if archiveResp.IsError {
		t.Fatalf("expected archive to succeed, got %+v", archiveResp)
	}

	plans = readPlanJSON(t, filepath.Join(tempDir, "TODO.json"))
	if len(plans) != 0 {
		t.Fatalf("expected all completed tasks to archive out of active plan, got %+v", plans)
	}
	archiveBytes, err := os.ReadFile(filepath.Join(tempDir, "TODO_ARCHIVE.md"))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	archiveText := string(archiveBytes)
	if !strings.Contains(archiveText, "prepare project") || !strings.Contains(archiveText, "implement backend") {
		t.Fatalf("expected archive to contain both completed tasks, got %q", archiveText)
	}
	archiveJSONLBytes, err := os.ReadFile(filepath.Join(tempDir, "TODO_ARCHIVE.jsonl"))
	if err != nil {
		t.Fatalf("read archive jsonl: %v", err)
	}
	archiveJSONLText := string(archiveJSONLBytes)
	if !strings.Contains(archiveJSONLText, "\"id\":") || !strings.Contains(archiveJSONLText, "\"depends_on_ids\":") {
		t.Fatalf("expected archive jsonl to preserve structured fields, got %q", archiveJSONLText)
	}
}

func TestManagePlanCompletionRemindsArchiveWhenNoActiveItemsRemain(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tool := NewManagePlanTool(nil)
	ctx := context.Background()

	if _, err := tool.Run(ctx, ToolCall{Input: `{"action":"add","item":"finish lifecycle"}`}); err != nil {
		t.Fatalf("add task: %v", err)
	}
	plans := readPlanJSON(t, filepath.Join(tempDir, "TODO.json"))
	resp, err := tool.Run(ctx, ToolCall{Input: `{"action":"complete","id":"` + planIDDisplay(plans[0].ID) + `"}`})
	if err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if !strings.Contains(resp.Content, "archive") {
		t.Fatalf("expected final completion response to remind archive closeout, got %q", resp.Content)
	}
}
func TestManagePlanResponsesIncludeStructuredSnapshotMetadata(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tool := NewManagePlanTool(nil)
	ctx := context.Background()

	addResp, err := tool.Run(ctx, ToolCall{Input: `{"action":"add","item":"implement progress card"}`})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	addMeta := readPlanSnapshotMetadata(t, addResp)
	if addMeta.Type != "plan_snapshot" || addMeta.Action != "add" {
		t.Fatalf("expected add plan snapshot metadata, got %+v", addMeta)
	}
	if addMeta.Revision <= 0 {
		t.Fatalf("expected positive add revision, got %+v", addMeta)
	}
	if len(addMeta.Items) != 1 || addMeta.Items[0].Title != "implement progress card" || addMeta.Items[0].Status != "pending" {
		t.Fatalf("expected pending item snapshot, got %+v", addMeta.Items)
	}
	if addMeta.Affected == nil || addMeta.Affected.Title != "implement progress card" {
		t.Fatalf("expected affected item metadata, got %+v", addMeta.Affected)
	}

	completeResp, err := tool.Run(ctx, ToolCall{Input: `{"action":"complete","id":"` + addMeta.Items[0].DisplayID + `"}`})
	if err != nil {
		t.Fatalf("complete task: %v", err)
	}
	completeMeta := readPlanSnapshotMetadata(t, completeResp)
	if completeMeta.Action != "complete" {
		t.Fatalf("expected complete metadata, got %+v", completeMeta)
	}
	if completeMeta.Revision <= addMeta.Revision {
		t.Fatalf("expected complete revision to advance from %d, got %d", addMeta.Revision, completeMeta.Revision)
	}
	if len(completeMeta.Items) != 1 || completeMeta.Items[0].Title != "implement progress card" || completeMeta.Items[0].Status != "completed" {
		t.Fatalf("expected completed item to remain in active snapshot, got %+v", completeMeta.Items)
	}
}

func TestManagePlanFilteredListKeepsFullSnapshotMetadata(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tool := NewManagePlanTool(nil)
	ctx := context.Background()

	if _, err := tool.Run(ctx, ToolCall{Input: `{"action":"add","item":"pending task"}`}); err != nil {
		t.Fatalf("add pending task: %v", err)
	}
	if _, err := tool.Run(ctx, ToolCall{Input: `{"action":"add","item":"second pending task"}`}); err != nil {
		t.Fatalf("add second task: %v", err)
	}

	listResp, err := tool.Run(ctx, ToolCall{Input: `{"action":"list","status":"completed"}`})
	if err != nil {
		t.Fatalf("list filtered task: %v", err)
	}
	meta := readPlanSnapshotMetadata(t, listResp)
	if !meta.IsFullSnapshot {
		t.Fatalf("expected full snapshot marker, got %+v", meta)
	}
	if meta.Revision <= 0 {
		t.Fatalf("expected positive list revision, got %+v", meta)
	}
	if len(meta.Items) != 2 {
		t.Fatalf("expected full plan items in metadata, got %+v", meta.Items)
	}
	if meta.Empty {
		t.Fatalf("expected non-empty full snapshot when filtered view has no rows")
	}
	if len(meta.ViewItems) != 0 {
		t.Fatalf("expected empty filtered view, got %+v", meta.ViewItems)
	}
}

func TestPlanRevisionRegistryHandlesConcurrentPlans(t *testing.T) {
	tempDir := t.TempDir()

	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			baseName := "plan-" + string(rune('a'+i%6))
			if revision := nextPlanRevision(tempDir, baseName); revision <= 0 {
				errs <- fmt.Errorf("expected positive revision for %s, got %d", baseName, revision)
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	registry, err := loadPlanRevisionRegistry(tempDir)
	if err != nil {
		t.Fatalf("load revision registry: %v", err)
	}
	var total int64
	for _, revision := range registry {
		total += revision
	}
	if total != workers {
		t.Fatalf("expected all revisions to be preserved, got registry=%+v total=%d", registry, total)
	}
}

func TestManagePlanBlocksRemovingActiveDependency(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tool := NewManagePlanTool(nil)
	ctx := context.Background()

	if _, err := tool.Run(ctx, ToolCall{Input: `{"action":"add","item":"prepare release"}`}); err != nil {
		t.Fatalf("add prerequisite: %v", err)
	}
	if _, err := tool.Run(ctx, ToolCall{Input: `{"action":"add","item":"execute release","depends_on":[1]}`}); err != nil {
		t.Fatalf("add dependent: %v", err)
	}
	plans := readPlanJSON(t, filepath.Join(tempDir, "TODO.json"))

	resp, err := tool.Run(ctx, ToolCall{Input: `{"action":"remove","id":"` + planIDDisplay(plans[0].ID) + `"}`})
	if err != nil {
		t.Fatalf("remove prerequisite: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected remove to be blocked when active dependents exist, got %+v", resp)
	}
	if strings.TrimSpace(resp.Content) == "" {
		t.Fatalf("expected dependency warning, got empty response")
	}
}
func TestManagePlanMarkdownMigrationPreservesFullIDs(t *testing.T) {
	tempDir := t.TempDir()
	mdPath := filepath.Join(tempDir, "TODO.md")
	firstID := "12345678-aaaa-bbbb-cccc-1234567890ab"
	secondID := "87654321-aaaa-bbbb-cccc-1234567890ab"
	content := strings.Join([]string{
		"- [ ] 閸掓繂顫愰崠鏍€嶉惄顔剧波閺?{id:" + firstID + "} [High]",
		"- [-] 鐞涖儱鍘栭崥搴ｎ伂閹恒儱褰?{id:" + secondID + "} depends_on_ids: [" + firstID + "] #backend",
	}, "\n")
	if err := os.WriteFile(mdPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write todo md: %v", err)
	}

	plans, err := normalizePlanEntriesFromMarkdown(mdPath)
	if err != nil {
		t.Fatalf("normalize plan from md: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0].ID != firstID {
		t.Fatalf("expected first id preserved, got %q", plans[0].ID)
	}
	if len(plans[1].DependsOnIDs) != 1 || plans[1].DependsOnIDs[0] != firstID {
		t.Fatalf("expected dependency id preserved, got %+v", plans[1].DependsOnIDs)
	}
}

func TestResolvePlanBaseNameOnlyReusesActivePlanWhenAllowed(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	ctx := SetContextValues(context.Background(), "session-1", "assistant-msg-12345678")
	rememberActivePlanFile(ctx, "release-plan")

	if got := resolvePlanBaseName(ctx, ""); got != "TODO_assistan" {
		t.Fatalf("expected fresh per-message plan without implicit reuse, got %q", got)
	}

	scopeCtx := SetPlanScopeID(ctx, "user-turn-87654321")
	if got := resolvePlanBaseName(scopeCtx, ""); got != "TODO_userturn" {
		t.Fatalf("expected request-scoped plan name to win over assistant message id, got %q", got)
	}

	reuseCtx := SetImplicitPlanReuse(ctx, true)
	if got := resolvePlanBaseName(reuseCtx, ""); got != "release-plan" {
		t.Fatalf("expected active plan to be reused when allowed, got %q", got)
	}
}

func TestManagePlanReusesAutoPlanWithinSameUserTurn(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tool := NewManagePlanTool(nil)
	firstCtx := SetPlanScopeID(SetContextValues(context.Background(), "session-1", "assistant-one"), "user-turn-12345678")
	if _, err := tool.Run(firstCtx, ToolCall{Input: `{"action":"add","item":"inspect pipeline"}`}); err != nil {
		t.Fatalf("add initial task: %v", err)
	}

	plans := readPlanJSON(t, filepath.Join(tempDir, "TODO_userturn.json"))
	if len(plans) != 1 {
		t.Fatalf("expected first plan file to contain one task, got %+v", plans)
	}

	secondCtx := SetPlanScopeID(SetContextValues(context.Background(), "session-1", "assistant-two"), "user-turn-12345678")
	resp, err := tool.Run(secondCtx, ToolCall{Input: `{"action":"update","id":"` + planIDDisplay(plans[0].ID) + `","status":"in_progress"}`})
	if err != nil {
		t.Fatalf("update task on later assistant message: %v", err)
	}
	if resp.IsError {
		t.Fatalf("expected same user turn to reuse auto plan, got %+v", resp)
	}

	plans = readPlanJSON(t, filepath.Join(tempDir, "TODO_userturn.json"))
	if len(plans) != 1 || plans[0].Status != "in_progress" {
		t.Fatalf("expected later assistant message to update original turn plan, got %+v", plans)
	}
}

func TestManagePlanKeepsAutoPlansIsolatedAcrossUserTurns(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tool := NewManagePlanTool(nil)
	firstTurnCtx := SetPlanScopeID(SetContextValues(context.Background(), "session-1", "assistant-one"), "11111111-user-turn")
	if _, err := tool.Run(firstTurnCtx, ToolCall{Input: `{"action":"add","item":"first task"}`}); err != nil {
		t.Fatalf("add first-turn task: %v", err)
	}

	secondTurnCtx := SetPlanScopeID(SetContextValues(context.Background(), "session-1", "assistant-two"), "22222222-user-turn")
	if _, err := tool.Run(secondTurnCtx, ToolCall{Input: `{"action":"add","item":"second task"}`}); err != nil {
		t.Fatalf("add second-turn task: %v", err)
	}

	firstPlan := readPlanJSON(t, filepath.Join(tempDir, "TODO_11111111.json"))
	if len(firstPlan) != 1 || firstPlan[0].Item != "first task" {
		t.Fatalf("expected first user turn plan to stay isolated, got %+v", firstPlan)
	}

	secondPlan := readPlanJSON(t, filepath.Join(tempDir, "TODO_22222222.json"))
	if len(secondPlan) != 1 || secondPlan[0].Item != "second task" {
		t.Fatalf("expected second user turn plan to stay isolated, got %+v", secondPlan)
	}
}

func TestLoadActivePlanSnapshotMetadataUsesAuthoritativePlanState(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tool := NewManagePlanTool(nil)
	ctx := SetPlanScopeID(SetContextValues(context.Background(), "session-1", "assistant-one"), "user-turn-12345678")
	if _, err := tool.Run(ctx, ToolCall{Input: `{"action":"add","item":"restore current plan"}`}); err != nil {
		t.Fatalf("add task: %v", err)
	}

	metadata, ok, err := LoadActivePlanSnapshotMetadata("session-1")
	if err != nil {
		t.Fatalf("load active plan snapshot metadata: %v", err)
	}
	if !ok {
		t.Fatalf("expected active plan snapshot metadata")
	}

	var snapshot planSnapshotMetadata
	if err := json.Unmarshal([]byte(metadata), &snapshot); err != nil {
		t.Fatalf("unmarshal active plan snapshot metadata: %v", err)
	}
	if snapshot.Type != "plan_snapshot" || snapshot.Action != "list" {
		t.Fatalf("expected authoritative list snapshot metadata, got %+v", snapshot)
	}
	if snapshot.BaseName != "TODO_userturn" {
		t.Fatalf("expected active plan base name, got %+v", snapshot)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].Title != "restore current plan" {
		t.Fatalf("expected active plan item in snapshot, got %+v", snapshot.Items)
	}
}

func TestManagePlanAddCreatesIsolatedPerTurnPlanWithoutImplicitReuse(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tool := NewManagePlanTool(nil)
	oldPlanCtx := SetContextValues(context.Background(), "session-1", "old-message")
	rememberActivePlanFile(oldPlanCtx, "release-plan")
	if err := os.WriteFile(filepath.Join(tempDir, "release-plan.json"), []byte(`[{"id":"12345678-aaaa-bbbb-cccc-1234567890ab","item":"old task","status":"pending"}]`), 0o644); err != nil {
		t.Fatalf("write active plan: %v", err)
	}

	ctx := SetContextValues(context.Background(), "session-1", "new-message-1234")
	resp, err := tool.Run(ctx, ToolCall{Input: `{"action":"add","item":"new task"}`})
	if err != nil {
		t.Fatalf("add isolated task: %v", err)
	}
	if !strings.Contains(resp.Content, "TODO_newmessa.md") {
		t.Fatalf("expected response to expose isolated plan file, got %q", resp.Content)
	}

	newPlan := readPlanJSON(t, filepath.Join(tempDir, "TODO_newmessa.json"))
	if len(newPlan) != 1 || newPlan[0].Item != "new task" {
		t.Fatalf("expected isolated plan to contain only new task, got %+v", newPlan)
	}

	oldPlan := readPlanJSON(t, filepath.Join(tempDir, "release-plan.json"))
	if len(oldPlan) != 1 || oldPlan[0].Item != "old task" {
		t.Fatalf("expected previous active plan untouched, got %+v", oldPlan)
	}

	registry, err := loadActivePlanRegistry(tempDir)
	if err != nil {
		t.Fatalf("load active plan registry: %v", err)
	}
	if got := registry[activePlanStateKey("session-1")]; got != "TODO_newmessa" {
		t.Fatalf("expected new turn plan to become active, got %q", got)
	}
}
func readPlanSnapshotMetadata(t *testing.T, resp ToolResponse) planSnapshotMetadata {
	t.Helper()
	if strings.TrimSpace(resp.Metadata) == "" {
		t.Fatalf("expected metadata on response: %+v", resp)
	}
	var meta planSnapshotMetadata
	if err := json.Unmarshal([]byte(resp.Metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v\nmetadata: %s", err, resp.Metadata)
	}
	return meta
}

func readPlanJSON(t *testing.T, path string) []PlanEntry {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plan json: %v", err)
	}
	var plans []PlanEntry
	if err := json.Unmarshal(content, &plans); err != nil {
		t.Fatalf("unmarshal plan json: %v", err)
	}
	return plans
}
