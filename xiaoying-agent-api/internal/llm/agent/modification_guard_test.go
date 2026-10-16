package agent

import (
	"testing"

	"github.com/auramate/auramate/internal/message"
)

func TestCollectModificationTurnTargetsKeepsCrossFileTurnBroad(t *testing.T) {
	toolCalls := []message.ToolCall{
		{Name: "write", Input: `{"file_path":"blueprints/extensions.py","content":"x"}`},
		{Name: "edit", Input: `{"file_path":"models.py","old_string":"a","new_string":"b"}`},
		{Name: "edit", Input: `{"file_path":"blueprints/admin.py","old_string":"c","new_string":"d"}`},
	}

	targets := collectModificationTurnTargets(toolCalls)
	if len(targets) != 3 {
		t.Fatalf("expected 3 distinct targets, got %d: %#v", len(targets), targets)
	}
	normalizedModelPath := normalizeToolTargetPath("models.py")

	dominantTarget, dominantCount := dominantModificationTurnTarget(toolCalls)
	if dominantTarget == "" {
		t.Fatalf("expected dominant target for broad modification turn")
	}
	if dominantCount != 1 {
		t.Fatalf("expected broad turn not to amplify a single target, got %d for %q", dominantCount, dominantTarget)
	}
	if !targetsOverlap(targets, []string{normalizedModelPath}) {
		t.Fatalf("expected overlap detection for modified file")
	}
}

func TestDominantModificationTurnTargetCountsRepeatedEdits(t *testing.T) {
	toolCalls := []message.ToolCall{
		{Name: "edit", Input: `{"file_path":"blueprints/admin.py","old_string":"a","new_string":"b"}`},
		{Name: "edit", Input: `{"file_path":"blueprints/admin.py","old_string":"c","new_string":"d"}`},
		{Name: "edit", Input: `{"file_path":"blueprints/admin.py","old_string":"e","new_string":"f"}`},
		{Name: "edit", Input: `{"file_path":"blueprints/admin.py","old_string":"g","new_string":"h"}`},
		{Name: "edit", Input: `{"file_path":"models.py","old_string":"i","new_string":"j"}`},
	}

	dominantTarget, dominantCount := dominantModificationTurnTarget(toolCalls)
	if dominantTarget != normalizeToolTargetPath("blueprints/admin.py") {
		t.Fatalf("expected dominant repeated target to be admin blueprint, got %q", dominantTarget)
	}
	if dominantCount != 4 {
		t.Fatalf("expected repeated edits to count per tool call, got %d", dominantCount)
	}
}

func TestIsModificationValidationToolCallRecognizesTargetedReadsAndBuilds(t *testing.T) {
	recentTargets := []string{normalizeToolTargetPath("blueprints/admin.py")}

	if !isModificationValidationToolCall(message.ToolCall{Name: "read", Input: `{"file_path":"blueprints/admin.py"}`}, recentTargets) {
		t.Fatalf("expected targeted read to count as validation")
	}
	if isModificationValidationToolCall(message.ToolCall{Name: "read", Input: `{"file_path":"models.py"}`}, recentTargets) {
		t.Fatalf("expected unrelated read not to count as validation")
	}
	if !isModificationValidationToolCall(message.ToolCall{Name: "run_command", Input: `go test ./...`}, recentTargets) {
		t.Fatalf("expected build or test command to count as validation")
	}
}
