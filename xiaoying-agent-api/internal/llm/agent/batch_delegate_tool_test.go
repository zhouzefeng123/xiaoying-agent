package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/message"
)

func TestBatchDependencyResultFromContentParsesStructuredArtifacts(t *testing.T) {
	content := `<worker_result>{"status":"completed","summary":"已生成客户清单","artifacts":["/tmp/customer.json","/tmp/customer.md"],"retryable":false,"failure_type":"","evidence_refs":["workspace/input.xlsx"]}</worker_result>`
	result := batchDependencyResultFromContent("collect_customer", content)
	if result.TaskID != "collect_customer" {
		t.Fatalf("unexpected task id: %q", result.TaskID)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.Summary != "已生成客户清单" {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("unexpected artifacts: %#v", result.Artifacts)
	}
	if len(result.EvidenceRefs) != 1 || result.EvidenceRefs[0] != "workspace/input.xlsx" {
		t.Fatalf("unexpected evidence refs: %#v", result.EvidenceRefs)
	}
}

func TestBatchDependencyResultFromContentMarksFailures(t *testing.T) {
	result := batchDependencyResultFromContent("collect_customer", "Error: worker execution failed: timeout")
	if result.Status != "failed" {
		t.Fatalf("expected failed status, got %q", result.Status)
	}
	if !strings.Contains(strings.ToLower(result.Summary), "worker execution failed") {
		t.Fatalf("unexpected failure summary: %q", result.Summary)
	}
}

func TestPrependBatchDependencyContextIncludesArtifactsAndTask(t *testing.T) {
	content := prependBatchDependencyContext("整理最终报告", []batchDependencyResult{{
		TaskID:       "collect_customer",
		Status:       "completed",
		Summary:      "已生成客户清单",
		Artifacts:    []string{"/tmp/customer.json"},
		EvidenceRefs: []string{"workspace/input.xlsx"},
	}})
	for _, snippet := range []string{"<dependency_context>", "task_id: collect_customer", "artifacts:", "/tmp/customer.json", "evidence_refs:", "workspace/input.xlsx", "当前任务：", "整理最终报告"} {
		if !strings.Contains(content, snippet) {
			t.Fatalf("expected content to include %q, got %q", snippet, content)
		}
	}
}

func TestLatestBatchWorkerResultPrefersLatestAssistantMessage(t *testing.T) {
	history := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "start"}}},
		{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "first result"}}},
		{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: `<worker_result>{"status":"completed","summary":"second result","artifacts":[],"retryable":false,"failure_type":"","evidence_refs":[]}</worker_result>`}}},
	}
	got := latestBatchWorkerResult(history, "fallback result")
	if !strings.Contains(got, "second result") {
		t.Fatalf("expected latest assistant result, got %q", got)
	}
}

func TestBatchDependencyListUnmarshalAcceptsStringsAndObjects(t *testing.T) {
	payload := []byte(`{"tasks":[{"id":"downstream","task":"combine","depends_on":["alpha",{"task_id":"beta","artifacts":["beta.json","shared.md"],"include_summary":false}]}]}`)
	var params BatchDelegateParams
	if err := json.Unmarshal(payload, &params); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(params.Tasks) != 1 {
		t.Fatalf("unexpected tasks count: %d", len(params.Tasks))
	}
	deps := params.Tasks[0].DependsOn
	if len(deps) != 2 {
		t.Fatalf("unexpected deps count: %#v", deps)
	}
	if deps[0].TaskID != "alpha" || len(deps[0].Artifacts) != 0 || deps[0].IncludeSummary != nil {
		t.Fatalf("string form mis-parsed: %#v", deps[0])
	}
	if deps[1].TaskID != "beta" || len(deps[1].Artifacts) != 2 {
		t.Fatalf("object form mis-parsed: %#v", deps[1])
	}
	if deps[1].IncludeSummary == nil || *deps[1].IncludeSummary {
		t.Fatalf("include_summary expected false, got %#v", deps[1].IncludeSummary)
	}
}

func TestBatchDependencyListUnmarshalRejectsEmptyTaskID(t *testing.T) {
	var list batchDependencyList
	if err := json.Unmarshal([]byte(`[{"artifacts":["a.md"]}]`), &list); err == nil {
		t.Fatalf("expected error for empty task_id")
	}
}

func TestApplyDependencySpecFiltersArtifactsByFullPathAndBasename(t *testing.T) {
	dep := batchDependencyResult{
		TaskID:       "alpha",
		Status:       "completed",
		Summary:      "alpha summary",
		Artifacts:    []string{"/tmp/workspace/spec.md", "/tmp/workspace/plan.md", "/tmp/workspace/other.json"},
		EvidenceRefs: []string{"workspace/input.txt"},
	}
	spec := batchDependencySpec{
		TaskID:    "alpha",
		Artifacts: []string{"spec.md", "/tmp/workspace/plan.md"},
	}
	filtered := applyDependencySpec(dep, spec)
	if len(filtered.Artifacts) != 2 {
		t.Fatalf("expected two matching artifacts, got %#v", filtered.Artifacts)
	}
	joined := strings.Join(filtered.Artifacts, "|")
	if !strings.Contains(joined, "spec.md") || !strings.Contains(joined, "plan.md") {
		t.Fatalf("expected spec.md and plan.md, got %q", joined)
	}
	if strings.Contains(joined, "other.json") {
		t.Fatalf("unexpected artifact leaked through filter: %q", joined)
	}
	if filtered.Summary != "alpha summary" {
		t.Fatalf("summary should be kept when include_summary unset, got %q", filtered.Summary)
	}
	if len(filtered.EvidenceRefs) != 1 {
		t.Fatalf("evidence should be kept when include_evidence unset, got %#v", filtered.EvidenceRefs)
	}
}

func TestApplyDependencySpecWildcardKeepsAll(t *testing.T) {
	dep := batchDependencyResult{
		TaskID:    "alpha",
		Status:    "completed",
		Artifacts: []string{"/tmp/a.md", "/tmp/b.md"},
	}
	spec := batchDependencySpec{TaskID: "alpha", Artifacts: []string{"*"}}
	filtered := applyDependencySpec(dep, spec)
	if len(filtered.Artifacts) != 2 {
		t.Fatalf("wildcard should keep all artifacts, got %#v", filtered.Artifacts)
	}
}

func TestApplyDependencySpecCanSuppressSummaryAndEvidence(t *testing.T) {
	no := false
	dep := batchDependencyResult{
		TaskID:       "alpha",
		Status:       "completed",
		Summary:      "alpha summary",
		Artifacts:    []string{"/tmp/a.md"},
		EvidenceRefs: []string{"ws/in.txt"},
	}
	spec := batchDependencySpec{TaskID: "alpha", IncludeSummary: &no, IncludeEvidence: &no}
	filtered := applyDependencySpec(dep, spec)
	if filtered.Summary != "" {
		t.Fatalf("summary should be cleared, got %q", filtered.Summary)
	}
	if len(filtered.EvidenceRefs) != 0 {
		t.Fatalf("evidence should be cleared, got %#v", filtered.EvidenceRefs)
	}
	if len(filtered.Artifacts) != 1 {
		t.Fatalf("artifacts should be preserved when no artifact filter set, got %#v", filtered.Artifacts)
	}
}
