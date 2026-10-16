package agent

import (
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/message"
)

func TestShouldRunCompletionReflection(t *testing.T) {
	historyWithToolError := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "修一下这个问题"}}},
		{Role: message.Assistant, Parts: []message.ContentPart{message.ToolCall{Name: "read_file", Input: "{}"}}},
		{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{Name: "read_file", Content: "failed", IsError: true}}},
	}
	if shouldRunCompletionReflection(historyWithToolError, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "这一步失败了，我先说明错误原因。"}}}, nil) {
		t.Fatalf("did not expect reflection after single-step tool failure")
	}

	historySimple := []message.Message{{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "你好"}}}}
	if shouldRunCompletionReflection(historySimple, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "你好，有什么我可以处理的？"}}}, nil) {
		t.Fatalf("did not expect reflection for simple single-step chat")
	}

	historyStableRule := []message.Message{{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "以后默认这样做，先读后写。"}}}}
	if shouldRunCompletionReflection(historyStableRule, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "收到，我后续会按这个规则执行。"}}}, nil) {
		t.Fatalf("did not expect reflection when stable memory is already captured directly")
	}

	historyWithCompletion := append(buildReflectionHistory(1), message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "已完成修复，定位到 [facts] artifact=config/app.yaml; marker=endpoint; current=/v2/api; expected=/v3/api; evidence=endpoint=/v2/api"}}})
	stateWithFindings := &sessionTaskState{
		Status:          "completed",
		StructuredFacts: []taskStructuredFact{{Artifact: "config/app.yaml", Marker: "endpoint", CurrentValue: "/v2/api"}},
		FindingsLedger:  []taskFindingLedgerEntry{{Summary: "定位到 endpoint 配置仍是旧值"}, {Summary: "修复后完成验证"}},
	}
	if shouldRunCompletionReflection(historyWithCompletion, historyWithCompletion[len(historyWithCompletion)-1], stateWithFindings) {
		t.Fatalf("did not expect reflection when task completed without repeated failures")
	}

	historyThresholdOnly := buildReflectionHistory(5)
	if shouldRunCompletionReflection(historyThresholdOnly, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "我已经检查完了。"}}}, &sessionTaskState{Status: "active", FindingsLedger: []taskFindingLedgerEntry{{Summary: "仅完成常规检查"}}}) {
		t.Fatalf("did not expect reflection from tool count alone")
	}

	historyPriorTurnHeavyCurrentTurnLight := append(buildReflectionHistory(5),
		message.Message{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "继续"}}},
		message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.ToolCall{Name: "read_file", Input: "{}"}}},
		message.Message{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{Name: "read_file", Content: "ok", IsError: false}}},
	)
	if shouldRunCompletionReflection(historyPriorTurnHeavyCurrentTurnLight, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "继续处理中。"}}}, &sessionTaskState{Status: "active", StructuredFacts: []taskStructuredFact{{Artifact: "a.go", Marker: "timeout", CurrentValue: "30s"}}}) {
		t.Fatalf("did not expect reflection when only prior turns met the threshold")
	}

	historyWithRepeatedFailures := buildReflectionFailureHistory(6, 4)
	if !shouldRunCompletionReflection(historyWithRepeatedFailures, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "这轮已经多次失败，我先收敛原因并暂停继续尝试。"}}}, &sessionTaskState{Status: "active", OutstandingIssues: []string{"read | a.go | failed"}}) {
		t.Fatalf("expected reflection for long task with repeated failures in current turn")
	}

	historyWithTooFewFailures := buildReflectionFailureHistory(6, 3)
	if shouldRunCompletionReflection(historyWithTooFewFailures, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "当前失败次数还不多，我继续处理。"}}}, &sessionTaskState{Status: "active", OutstandingIssues: []string{"read | a.go | failed"}}) {
		t.Fatalf("did not expect reflection when failure count is below threshold")
	}

	historyNotLongEnough := buildReflectionFailureHistory(5, 4)
	if shouldRunCompletionReflection(historyNotLongEnough, message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "虽然失败很多，但当前轮还不算长任务。"}}}, &sessionTaskState{Status: "active", OutstandingIssues: []string{"read | a.go | failed"}}) {
		t.Fatalf("did not expect reflection when current turn is not long enough")
	}
}

func TestNormalizeReflectionMemoryCategory(t *testing.T) {
	tests := map[string]string{
		"knowledge":  "fact",
		"rule":       "instruction",
		"experience": "experience",
		"":           "experience",
		"unexpected": "experience",
	}

	for input, want := range tests {
		if got := normalizeReflectionMemoryCategory(input); got != want {
			t.Fatalf("category %q: got %q want %q", input, got, want)
		}
	}
}

func TestBuildFallbackReflectionMemoryFromToolError(t *testing.T) {
	history := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "读取这个文件"}}},
		{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{
			Name:    "read",
			Content: "Error: File not found: C:/workspace/memory_missing_case.txt",
			IsError: true,
		}}},
	}

	mem, ok := buildFallbackReflectionMemory(history)
	if !ok || mem == nil {
		t.Fatalf("expected fallback reflection memory")
	}
	if mem.Action != "add" {
		t.Fatalf("expected add action, got %q", mem.Action)
	}
	if mem.Category != "experience" {
		t.Fatalf("expected experience category, got %q", mem.Category)
	}
	if mem.TaskStatus != "failure" {
		t.Fatalf("expected failure task status, got %q", mem.TaskStatus)
	}
	if mem.Content == "" || mem.Keywords == "" {
		t.Fatalf("expected fallback content and keywords, got %+v", mem)
	}
	if got := mem.Content; got == "" || !containsAll(got, "memory_missing_case.txt", "不存在") {
		t.Fatalf("expected file-specific fallback content, got %q", got)
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}

func buildReflectionHistory(toolCount int) []message.Message {
	history := make([]message.Message, 0, toolCount*2+1)
	history = append(history, message.Message{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "执行多步任务"}}})
	for i := 0; i < toolCount; i++ {
		history = append(history,
			message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.ToolCall{Name: "read_file", Input: "{}"}}},
			message.Message{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{Name: "read_file", Content: "ok", IsError: false}}},
		)
	}
	return history
}

func buildReflectionFailureHistory(successCount, errorCount int) []message.Message {
	history := make([]message.Message, 0, (successCount+errorCount)*2+1)
	history = append(history, message.Message{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "执行长任务"}}})
	for i := 0; i < successCount; i++ {
		history = append(history,
			message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.ToolCall{Name: "read_file", Input: "{}"}}},
			message.Message{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{Name: "read_file", Content: "ok", IsError: false}}},
		)
	}
	for i := 0; i < errorCount; i++ {
		history = append(history,
			message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.ToolCall{Name: "read_file", Input: "{}"}}},
			message.Message{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{Name: "read_file", Content: "failed", IsError: true}}},
		)
	}
	return history
}
