package agent

import (
	"strings"
	"testing"
)

func TestParseWorkerResultEnvelope(t *testing.T) {
	input := `<worker_result>{"status":"completed","summary":"已生成报告","artifacts":["/tmp/report.md"],"retryable":false,"failure_type":"","evidence_refs":["src/a.go"]}</worker_result>`
	result, ok := parseWorkerResultEnvelope(input)
	if !ok {
		t.Fatalf("expected structured worker result to parse")
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.Summary != "已生成报告" {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0] != "/tmp/report.md" {
		t.Fatalf("unexpected artifacts: %#v", result.Artifacts)
	}
	if len(result.EvidenceRefs) != 1 || result.EvidenceRefs[0] != "src/a.go" {
		t.Fatalf("unexpected evidence refs: %#v", result.EvidenceRefs)
	}
}

func TestRenderWorkerResultEnvelope(t *testing.T) {
	output := renderWorkerResultEnvelope(workerResultEnvelope{
		Status:       "partial",
		Summary:      "完成检索，剩余步骤需人工确认。",
		Artifacts:    []string{"/tmp/findings.md"},
		Retryable:    true,
		FailureType:  "missing_input",
		EvidenceRefs: []string{"workspace/input.txt"},
	})
	for _, snippet := range []string{"status=partial", "failure_type=missing_input", "retryable=true", "/tmp/findings.md", "workspace/input.txt", "完成检索，剩余步骤需人工确认。"} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("expected rendered output to contain %q, got %q", snippet, output)
		}
	}
}
