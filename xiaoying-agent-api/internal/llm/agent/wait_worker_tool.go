package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
)

type waitForWorkersTool struct {
	orchestrator *Orchestrator
}

func NewWaitForWorkersTool(orchestrator *Orchestrator) tools.BaseTool {
	return &waitForWorkersTool{
		orchestrator: orchestrator,
	}
}

func (t *waitForWorkersTool) Info() tools.ToolInfo {
	return tools.ToolInfo{
		Name:        "wait_for_workers",
		Description: "Wait for multiple background workers to complete their tasks and retrieve their results. Use this after calling 'delegate_task' with async=true multiple times. This enables parallel execution of tasks. If worker_ids is empty, it waits for ALL active workers.",
		Parameters: map[string]any{
			"worker_ids": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "List of worker IDs to wait for. If omitted or empty, waits for ALL active workers.",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds. Default 600.",
			},
			"wait_mode": map[string]any{
				"type":        "string",
				"description": "Wait mode: 'all_finished' or 'any_finished'. Default all_finished.",
				"enum":        []string{"all_finished", "any_finished"},
			},
		},
		Required: []string{},
	}
}

type WaitForWorkersParams struct {
	WorkerIDs []string `json:"worker_ids"`
	Timeout   int      `json:"timeout"`
	WaitMode  string   `json:"wait_mode"`
}

func extractBatchWorkerText(msg message.Message) string {
	var builder strings.Builder
	for _, part := range msg.Parts {
		if txt, ok := part.(message.TextContent); ok {
			builder.WriteString(txt.Text)
		}
	}
	return strings.TrimSpace(builder.String())
}

func extractBatchWorkerSummary(history []message.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != message.Assistant {
			continue
		}
		content := extractBatchWorkerText(msg)
		if content == "" {
			continue
		}
		if structured, ok := parseWorkerResultEnvelope(content); ok {
			if rendered := renderWorkerResultEnvelope(structured); rendered != "" {
				return rendered
			}
		}
		if start := strings.Index(content, "<summary>"); start != -1 {
			if end := strings.Index(content, "</summary>"); end != -1 && end > start {
				return strings.TrimSpace(content[start+9 : end])
			}
		}
		return content
	}
	return ""
}

func (t *waitForWorkersTool) waitForUsableWorkerResult(ctx context.Context, workerID string, gracePeriod time.Duration) string {
	deadline := time.Now().Add(gracePeriod)
	for {
		history, historyErr := t.orchestrator.GetWorkerHistory(workerID)
		if historyErr == nil {
			if summary := extractBatchWorkerSummary(history); summary != "" && summary != "..." {
				return summary
			}
		}

		_, result, stateErr := t.orchestrator.GetWorkerState(workerID)
		if stateErr == nil {
			trimmed := strings.TrimSpace(result)
			if structured, ok := parseWorkerResultEnvelope(trimmed); ok {
				if rendered := renderWorkerResultEnvelope(structured); rendered != "" {
					return rendered
				}
			}
			if trimmed != "" && trimmed != "..." {
				return trimmed
			}
		}

		if time.Now().After(deadline) {
			return ""
		}

		select {
		case <-ctx.Done():
			return ""
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (t *waitForWorkersTool) Run(ctx context.Context, call tools.ToolCall) (tools.ToolResponse, error) {
	var params WaitForWorkersParams
	// Try to unmarshal, but if it fails, we'll just use default behavior (wait for all)
	// This is robust against LLM hallucinating arguments or bad JSON
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		// Log warning but proceed with empty params (which triggers default behavior)
		// logging.Warn("wait_for_workers: failed to parse params, defaulting to wait for all", "error", err)
	}

	if len(params.WorkerIDs) == 0 {
		params.WorkerIDs = t.orchestrator.ListActiveWorkers()
		if len(params.WorkerIDs) == 0 {
			params.WorkerIDs = t.orchestrator.GetAllWorkerIDs()
		}
		if len(params.WorkerIDs) == 0 {
			return tools.NewTextResponse("No background workers found (neither active nor finished)."), nil
		}
	}

	results := make(map[string]string)

	timeoutSeconds := 600
	if params.Timeout > 0 {
		timeoutSeconds = params.Timeout
	}
	recommendedWaitSeconds := t.estimateRecommendedWaitSeconds(params.WorkerIDs)
	if timeoutSeconds < recommendedWaitSeconds {
		timeoutSeconds = recommendedWaitSeconds
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	waitMode := strings.ToLower(strings.TrimSpace(params.WaitMode))
	if waitMode == "" {
		waitMode = "all_finished"
	}
	if waitMode != "all_finished" && waitMode != "any_finished" {
		return tools.NewTextErrorResponse("wait_mode must be 'all_finished' or 'any_finished'"), nil
	}

	pending := make(map[string]bool)
	for _, id := range params.WorkerIDs {
		pending[id] = true
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Anti-race condition: Wait a short moment before accepting "not busy" as "done"
	// because the worker might be in the process of picking up the task from the bus.
	startTime := time.Now()
	minWaitDuration := 2 * time.Second

	for len(pending) > 0 {
		select {
		case <-ctx.Done():
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Timeout waiting for workers after %v.\n", timeout))
			if len(results) > 0 {
				sb.WriteString("Partial completed results:\n")
				for _, id := range params.WorkerIDs {
					if res, ok := results[id]; ok {
						sb.WriteString(fmt.Sprintf("\n--- Result from %s ---\n%s\n", id, res))
					}
				}
			}

			pendingIDs := make([]string, 0, len(pending))
			for _, id := range params.WorkerIDs {
				if pending[id] {
					pendingIDs = append(pendingIDs, id)
				}
			}
			if len(pendingIDs) > 0 {
				sb.WriteString("\nPending workers diagnostics:\n")
			}
			now := time.Now()
			progressingWorkers := make([]string, 0, len(pendingIDs))
			stalledWorkers := make([]string, 0, len(pendingIDs))
			for _, id := range pendingIDs {
				runtimeInfo, runtimeErr := t.orchestrator.GetWorkerRuntime(id)
				if runtimeErr != nil {
					sb.WriteString(fmt.Sprintf("- %s: runtime unavailable (%v)\n", id, runtimeErr))
					continue
				}
				idleFor := now.Sub(runtimeInfo.LastActivityAt).Round(time.Second)
				progressAt := runtimeInfo.LastProgressAt
				if progressAt.IsZero() {
					progressAt = runtimeInfo.LastActivityAt
				}
				progressIdleFor := time.Duration(0)
				if !progressAt.IsZero() {
					progressIdleFor = now.Sub(progressAt).Round(time.Second)
				}
				heartbeatIdleFor := time.Duration(0)
				if !runtimeInfo.LastHeartbeatAt.IsZero() {
					heartbeatIdleFor = now.Sub(runtimeInfo.LastHeartbeatAt).Round(time.Second)
				}
				sb.WriteString(fmt.Sprintf("- %s: status=%s last_activity=%s idle_for=%s progress_idle_for=%s heartbeat_idle_for=%s timeout_seconds=%d\n", id, runtimeInfo.Status, runtimeInfo.LastActivityAt.Format(time.RFC3339), idleFor, progressIdleFor, heartbeatIdleFor, runtimeInfo.TimeoutSeconds))
				if runtimeInfo.Status == "running" {
					if progressIdleFor > 45*time.Second && (heartbeatIdleFor == 0 || heartbeatIdleFor > 90*time.Second) {
						stalledWorkers = append(stalledWorkers, id)
					} else {
						progressingWorkers = append(progressingWorkers, id)
					}
				}
			}
			if len(progressingWorkers) > 0 {
				sb.WriteString("\nRunning workers still showing recent activity (kept alive):\n")
				for _, id := range progressingWorkers {
					sb.WriteString(fmt.Sprintf("- %s\n", id))
				}
			}
			if len(stalledWorkers) > 0 {
				sb.WriteString("\nRunning workers with stale progress and no fresh heartbeat:\n")
				for _, id := range stalledWorkers {
					sb.WriteString(fmt.Sprintf("- %s\n", id))
				}
			}
			sb.WriteString("Root-cause hint: fresh heartbeat with stale progress usually means provider/tool latency, not event communication loss.\n")
			sb.WriteString("Next step: either increase task timeout and wait again, inspect/resume the worker, or execute fallback with local tools.\n")
			return tools.NewTextResponse(sb.String()), nil
		case <-ticker.C:
			// Snapshot keys to avoid modification during iteration issues (though map iteration is safe-ish, deletion is safe)
			// But iterating and deleting is safe in Go.
			for id := range pending {
				busy, result, err := t.orchestrator.GetWorkerState(id)
				if err != nil {
					// Worker died or not found
					results[id] = fmt.Sprintf("Error: %v", err)
					delete(pending, id)
					continue
				}

				if !busy {
					if result == "" && time.Since(startTime) < minWaitDuration {
						continue
					}

					stableResult := t.waitForUsableWorkerResult(ctx, id, 2*time.Second)
					if stableResult != "" {
						results[id] = stableResult
					} else {
						results[id] = result
					}
					delete(pending, id)
					if waitMode == "any_finished" {
						var sb strings.Builder
						sb.WriteString("First worker completed:\n")
						sb.WriteString(fmt.Sprintf("\n--- Result from %s ---\n%s\n", id, results[id]))
						return tools.NewTextResponse(sb.String()), nil
					}
				}
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("All %d workers completed:\n", len(params.WorkerIDs)))
	failedWorkers := make([]string, 0)
	for _, id := range params.WorkerIDs { // Iterate in original order
		res := results[id]
		sb.WriteString(fmt.Sprintf("\n--- Result from %s ---\n%s\n", id, res))
		if isWorkerFailureResult(res) {
			failedWorkers = append(failedWorkers, id)
		}
	}
	if len(failedWorkers) > 0 {
		sb.WriteString("\nWorkers with execution failures detected:\n")
		for _, id := range failedWorkers {
			sb.WriteString(fmt.Sprintf("- %s\n", id))
		}
		sb.WriteString("Next step: continue the remaining work directly with local tools instead of waiting again.\n")
	}
	return tools.NewTextResponse(sb.String()), nil
}

func isWorkerFailureResult(result string) bool {
	if structured, ok := parseWorkerResultEnvelope(result); ok {
		switch structured.Status {
		case "failed":
			return true
		case "completed", "partial":
			return false
		}
	}
	text := strings.ToLower(strings.TrimSpace(result))
	if text == "" {
		return false
	}
	patterns := []string{
		"error:",
		"worker execution failed",
		"tool error",
		"wait timed out",
		"timed out after",
		"connection refused",
		"tool not found",
		"worker not found",
		"执行失败",
		"工具调用失败",
		"等待超时",
		"连接被拒绝",
	}
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

func (t *waitForWorkersTool) estimateRecommendedWaitSeconds(workerIDs []string) int {
	maxWorkerTimeout := 0
	for _, id := range workerIDs {
		runtimeInfo, err := t.orchestrator.GetWorkerRuntime(id)
		if err != nil {
			continue
		}
		if runtimeInfo.TimeoutSeconds > maxWorkerTimeout {
			maxWorkerTimeout = runtimeInfo.TimeoutSeconds
		}
	}
	if maxWorkerTimeout <= 0 {
		return 90
	}
	recommended := maxWorkerTimeout + 30
	if recommended < 90 {
		recommended = 90
	}
	if recommended > 900 {
		recommended = 900
	}
	return recommended
}
