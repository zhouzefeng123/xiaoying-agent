package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const workerResultTag = "worker_result"

type workerResultEnvelope struct {
	Status       string   `json:"status"`
	Summary      string   `json:"summary"`
	Artifacts    []string `json:"artifacts"`
	Retryable    bool     `json:"retryable"`
	FailureType  string   `json:"failure_type"`
	EvidenceRefs []string `json:"evidence_refs"`
}

func extractTaggedBlock(text string, tag string) string {
	startTag := "<" + tag + ">"
	endTag := "</" + tag + ">"
	end := strings.LastIndex(text, endTag)
	if end == -1 {
		return ""
	}
	start := strings.LastIndex(text[:end], startTag)
	if start == -1 {
		return ""
	}
	return strings.TrimSpace(text[start+len(startTag) : end])
}

func parseWorkerResultEnvelope(text string) (workerResultEnvelope, bool) {
	payload := extractTaggedBlock(text, workerResultTag)
	if payload == "" {
		return workerResultEnvelope{}, false
	}
	var result workerResultEnvelope
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return workerResultEnvelope{}, false
	}
	result.Status = strings.ToLower(strings.TrimSpace(result.Status))
	result.Summary = strings.TrimSpace(result.Summary)
	result.FailureType = strings.TrimSpace(result.FailureType)
	result.Artifacts = compactNonEmpty(result.Artifacts)
	result.EvidenceRefs = compactNonEmpty(result.EvidenceRefs)
	return result, true
}

func renderWorkerResultEnvelope(result workerResultEnvelope) string {
	parts := make([]string, 0, 5)
	if result.Status != "" {
		parts = append(parts, fmt.Sprintf("status=%s", result.Status))
	}
	if result.FailureType != "" {
		parts = append(parts, fmt.Sprintf("failure_type=%s", result.FailureType))
	}
	if result.Retryable {
		parts = append(parts, "retryable=true")
	}
	if len(result.Artifacts) > 0 {
		parts = append(parts, fmt.Sprintf("artifacts=%s", strings.Join(result.Artifacts, ", ")))
	}
	if len(result.EvidenceRefs) > 0 {
		parts = append(parts, fmt.Sprintf("evidence_refs=%s", strings.Join(result.EvidenceRefs, ", ")))
	}

	var builder strings.Builder
	if len(parts) > 0 {
		builder.WriteString("[worker_result] ")
		builder.WriteString(strings.Join(parts, " | "))
	}
	if result.Summary != "" {
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(result.Summary)
	}
	return strings.TrimSpace(builder.String())
}

func buildWorkerResultEnvelope(result workerResultEnvelope) string {
	payload, err := json.Marshal(result)
	if err != nil {
		fallback := workerResultEnvelope{
			Status:      "failed",
			Summary:     "worker result encoding failed",
			Retryable:   false,
			FailureType: "serialization_error",
		}
		payload, _ = json.Marshal(fallback)
	}
	return "<" + workerResultTag + ">" + string(payload) + "</" + workerResultTag + ">"
}

func buildWorkerFailureEnvelope(summary, failureType string, retryable bool) string {
	result := workerResultEnvelope{
		Status:      "failed",
		Summary:     normalizeWorkerSummaryText(summary, 320),
		Retryable:   retryable,
		FailureType: strings.TrimSpace(failureType),
	}
	if result.Summary == "" {
		result.Summary = "worker execution failed"
	}
	return buildWorkerResultEnvelope(result)
}

func normalizeWorkerSummaryText(text string, maxLen int) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if maxLen > 0 && len(trimmed) > maxLen {
		return strings.TrimSpace(trimmed[:maxLen-3]) + "..."
	}
	return trimmed
}

func compactNonEmpty(values []string) []string {
	compacted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		compacted = append(compacted, value)
	}
	return compacted
}

func missingAbsoluteArtifacts(result workerResultEnvelope) []string {
	missing := make([]string, 0)
	for _, artifact := range result.Artifacts {
		if !filepath.IsAbs(artifact) {
			continue
		}
		if _, err := os.Stat(artifact); err != nil {
			missing = append(missing, artifact)
		}
	}
	return missing
}

func workerResultProtocolInstructions() string {
	return `
7. STRUCTURED RESULT PROTOCOL: At the end of the task, ALWAYS try to include one machine-readable block:
   <worker_result>{"status":"completed|failed|partial","summary":"short final summary","artifacts":["absolute/path/or/output/name"],"retryable":false,"failure_type":"","evidence_refs":["optional evidence reference"]}</worker_result>
   - Keep summary concise and user-facing.
   - If the task failed, set status to failed and explain the root cause in summary.
   - If you also provide <summary>...</summary>, keep both consistent.`
}
