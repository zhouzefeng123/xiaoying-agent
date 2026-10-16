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

type manageWorkerTool struct {
	orchestrator *Orchestrator
}

func NewManageWorkerTool(orchestrator *Orchestrator) tools.BaseTool {
	return &manageWorkerTool{
		orchestrator: orchestrator,
	}
}

func (t *manageWorkerTool) Info() tools.ToolInfo {
	return tools.ToolInfo{
		Name:        "manage_worker",
		Description: "Manage background worker agents. Use this to check status, view output, resume from checkpoint, or stop workers started with 'delegate_task' (async=true).",
		Parameters: map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action to perform: 'list', 'status', 'wait', 'history', 'stop', 'resume', or 'snapshots'.",
				"enum":        []string{"list", "status", "wait", "history", "stop", "resume", "snapshots"},
			},
			"id": map[string]any{
				"type":        "string",
				"description": "Worker ID (required for status, wait, history, stop, resume).",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Number of recent messages to retrieve for 'history' (default: 10).",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds for 'wait' action (default: 60).",
			},
			"task": map[string]any{
				"type":        "string",
				"description": "Optional additional instruction for 'resume'. If omitted, the worker resumes the original task from checkpoint.",
			},
		},
		Required: []string{"action"},
	}
}

type ManageWorkerParams struct {
	Action  string `json:"action"`
	ID      string `json:"id"`
	Limit   int    `json:"limit"`
	Timeout int    `json:"timeout"`
	Task    string `json:"task"`
}

func extractWorkerText(msg message.Message) string {
	var builder strings.Builder
	for _, part := range msg.Parts {
		if txt, ok := part.(message.TextContent); ok {
			builder.WriteString(txt.Text)
		}
	}
	return strings.TrimSpace(builder.String())
}

func extractWorkerSummary(history []message.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != message.Assistant {
			continue
		}
		content := extractWorkerText(msg)
		if content == "" {
			continue
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

func (t *manageWorkerTool) waitForUsableWorkerResult(ctx context.Context, workerID string, gracePeriod time.Duration) string {
	deadline := time.Now().Add(gracePeriod)
	for {
		history, historyErr := t.orchestrator.GetWorkerHistory(workerID)
		if historyErr == nil {
			if summary := extractWorkerSummary(history); summary != "" && summary != "..." {
				return summary
			}
		}

		_, result, stateErr := t.orchestrator.GetWorkerState(workerID)
		if stateErr == nil {
			trimmed := strings.TrimSpace(result)
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

func (t *manageWorkerTool) Run(ctx context.Context, call tools.ToolCall) (tools.ToolResponse, error) {
	var params ManageWorkerParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	if params.Action == "" {
		return tools.NewTextErrorResponse("action is required"), nil
	}

	switch params.Action {
	case "list":
		snapshots := t.orchestrator.ListWorkerRuntimeSnapshots(20)
		if len(snapshots) == 0 {
			return tools.NewTextResponse("No worker snapshots."), nil
		}
		var sb strings.Builder
		sb.WriteString("Worker Snapshots:\n")
		for _, runtimeInfo := range snapshots {
			archivedSuffix := ""
			if runtimeInfo.Archived {
				archivedSuffix = " [archived]"
			}
			sb.WriteString(fmt.Sprintf("- ID: %s, Role: %s, Status: %s%s, TaskID: %s\n", runtimeInfo.WorkerID, runtimeInfo.Role, runtimeInfo.Status, archivedSuffix, runtimeInfo.TaskID))
		}
		return tools.NewTextResponse(sb.String()), nil

	case "snapshots":
		limit := params.Limit
		if limit <= 0 {
			limit = 20
		}
		entries := t.orchestrator.ListWorkerRuntimeSnapshots(limit)
		if len(entries) == 0 {
			return tools.NewTextResponse("No worker snapshots."), nil
		}
		var sb strings.Builder
		sb.WriteString("Worker Runtime Snapshots:\n")
		for _, runtimeInfo := range entries {
			sb.WriteString(fmt.Sprintf("\n- ID: %s\n  Role: %s\n  Status: %s\n  Archived: %t\n  TaskID: %s\n  LastActivity: %s\n", runtimeInfo.WorkerID, runtimeInfo.Role, runtimeInfo.Status, runtimeInfo.Archived, runtimeInfo.TaskID, runtimeInfo.LastActivityAt.Format(time.RFC3339)))
			if runtimeInfo.FailureType != "" {
				sb.WriteString(fmt.Sprintf("  FailureType: %s\n", runtimeInfo.FailureType))
			}
			if runtimeInfo.LastError != "" {
				sb.WriteString(fmt.Sprintf("  LastError: %s\n", runtimeInfo.LastError))
			}
			if runtimeInfo.StructuredStatus != "" {
				sb.WriteString(fmt.Sprintf("  StructuredStatus: %s\n", runtimeInfo.StructuredStatus))
			}
			if runtimeInfo.StructuredSummary != "" {
				sb.WriteString(fmt.Sprintf("  StructuredSummary: %s\n", runtimeInfo.StructuredSummary))
			}
			if runtimeInfo.LastResultPreview != "" {
				sb.WriteString(fmt.Sprintf("  ResultPreview: %s\n", runtimeInfo.LastResultPreview))
			}
			if runtimeInfo.Checkpoint != nil {
				sb.WriteString(fmt.Sprintf("  Checkpoint: status=%s original_task=%s\n", runtimeInfo.Checkpoint.Status, normalizeWorkerSummaryText(runtimeInfo.Checkpoint.OriginalTask, 120)))
			}
			if runtimeInfo.ProviderRetryCount > 0 || runtimeInfo.OutputRetryCount > 0 {
				sb.WriteString(fmt.Sprintf("  Retries: provider=%d output=%d retryable=%t\n", runtimeInfo.ProviderRetryCount, runtimeInfo.OutputRetryCount, runtimeInfo.RetryableFailure))
			}
			if runtimeInfo.LastRecoveryNote != "" {
				sb.WriteString(fmt.Sprintf("  RecoveryNote: %s\n", runtimeInfo.LastRecoveryNote))
			}
			if runtimeInfo.ArchivedReason != "" {
				sb.WriteString(fmt.Sprintf("  ArchivedReason: %s\n", runtimeInfo.ArchivedReason))
			}
		}
		return tools.NewTextResponse(sb.String()), nil

	case "status":
		if params.ID == "" {
			return tools.NewTextErrorResponse("id is required for status"), nil
		}
		runtimeInfo, err := t.orchestrator.GetWorkerRuntime(params.ID)
		if err != nil {
			return tools.NewTextErrorResponse(fmt.Sprintf("Worker %s not found", params.ID)), nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Worker Status\n- ID: %s\n- Role: %s\n- Status: %s\n- TaskID: %s\n- LastActivity: %s\n- Archived: %t", runtimeInfo.WorkerID, runtimeInfo.Role, runtimeInfo.Status, runtimeInfo.TaskID, runtimeInfo.LastActivityAt.Format(time.RFC3339), runtimeInfo.Archived))
		if runtimeInfo.FailureType != "" {
			sb.WriteString(fmt.Sprintf("\n- FailureType: %s", runtimeInfo.FailureType))
		}
		if runtimeInfo.LastError != "" {
			sb.WriteString(fmt.Sprintf("\n- LastError: %s", runtimeInfo.LastError))
		}
		if runtimeInfo.StructuredSummary != "" {
			sb.WriteString(fmt.Sprintf("\n- StructuredSummary: %s", runtimeInfo.StructuredSummary))
		}
		if runtimeInfo.LastResultPreview != "" {
			sb.WriteString(fmt.Sprintf("\n- ResultPreview: %s", runtimeInfo.LastResultPreview))
		}
		if runtimeInfo.Checkpoint != nil {
			sb.WriteString(fmt.Sprintf("\n- CheckpointStatus: %s", runtimeInfo.Checkpoint.Status))
			if runtimeInfo.Checkpoint.OriginalTask != "" {
				sb.WriteString(fmt.Sprintf("\n- OriginalTask: %s", normalizeWorkerSummaryText(runtimeInfo.Checkpoint.OriginalTask, 180)))
			}
		}
		if runtimeInfo.ProviderRetryCount > 0 || runtimeInfo.OutputRetryCount > 0 {
			sb.WriteString(fmt.Sprintf("\n- Retries: provider=%d output=%d retryable=%t", runtimeInfo.ProviderRetryCount, runtimeInfo.OutputRetryCount, runtimeInfo.RetryableFailure))
		}
		return tools.NewTextResponse(sb.String()), nil

	case "wait":
		if params.ID == "" {
			return tools.NewTextErrorResponse("id is required for wait"), nil
		}

		timeout := params.Timeout
		if timeout <= 0 {
			timeout = 60 // Default 60s
		}

		// Check if worker exists first
		workers := t.orchestrator.GetWorkers()
		if _, ok := workers[params.ID]; !ok {
			runtimeInfo, runtimeErr := t.orchestrator.GetWorkerRuntime(params.ID)
			if runtimeErr == nil {
				return tools.NewTextResponse(fmt.Sprintf("Worker snapshot available.\nStatus: %s\nSummary: %s", runtimeInfo.Status, firstNonEmpty(runtimeInfo.StructuredSummary, runtimeInfo.LastResultPreview, runtimeInfo.LastError))), nil
			}
			return tools.NewTextResponse("Worker already completed or not found."), nil
		}

		// Blocking Wait
		err := t.orchestrator.WaitWorker(ctx, params.ID, timeout)
		if err != nil {
			return tools.NewTextResponse(fmt.Sprintf("Wait result: %s", err.Error())), nil
		}

		if stableResult := t.waitForUsableWorkerResult(ctx, params.ID, 3*time.Second); stableResult != "" {
			return tools.NewTextResponse(fmt.Sprintf("Worker Completed.\nResult:\n%s", stableResult)), nil
		}

		history, historyErr := t.orchestrator.GetWorkerHistory(params.ID)
		if historyErr == nil {
			if summary := extractWorkerSummary(history); summary != "" {
				return tools.NewTextResponse(fmt.Sprintf("Worker Completed.\nResult:\n%s", summary)), nil
			}
		}

		_, result, stateErr := t.orchestrator.GetWorkerState(params.ID)
		if stateErr == nil && strings.TrimSpace(result) != "" {
			return tools.NewTextResponse(fmt.Sprintf("Worker Completed.\nResult:\n%s", result)), nil
		}
		history, err = t.orchestrator.GetWorkerHistory(params.ID)
		if err == nil && len(history) > 0 {
			lastMsg := history[len(history)-1]
			content := ""
			for _, part := range lastMsg.Parts {
				if txt, ok := part.(message.TextContent); ok {
					content += txt.Text
				}
			}
			if strings.TrimSpace(content) != "" {
				return tools.NewTextResponse(fmt.Sprintf("Worker Completed.\nLast Message: %s", content)), nil
			}
		}
		return tools.NewTextResponse("Worker Completed."), nil

	case "history":
		if params.ID == "" {
			return tools.NewTextErrorResponse("id is required for history"), nil
		}
		// We need to implement GetWorkerHistory in Orchestrator
		history, err := t.orchestrator.GetWorkerHistory(params.ID)
		if err != nil {
			return tools.NewTextErrorResponse(fmt.Sprintf("Error: %s", err)), nil
		}

		limit := params.Limit
		if limit <= 0 {
			limit = 10
		}

		start := 0
		if len(history) > limit {
			start = len(history) - limit
		}

		var sb strings.Builder
		for i := start; i < len(history); i++ {
			msg := history[i]
			content := ""
			for _, part := range msg.Parts {
				if txt, ok := part.(message.TextContent); ok {
					content += txt.Text
				}
			}
			// Truncate long content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("[%s] %s\n", msg.Role, content))
		}
		return tools.NewTextResponse(sb.String()), nil

	case "stop":
		if params.ID == "" {
			return tools.NewTextErrorResponse("id is required for stop"), nil
		}
		t.orchestrator.KillAgent(params.ID)
		return tools.NewTextResponse(fmt.Sprintf("Worker %s stopped.", params.ID)), nil

	case "resume":
		if params.ID == "" {
			return tools.NewTextErrorResponse("id is required for resume"), nil
		}
		newID, err := t.orchestrator.ResumeWorkerFromCheckpoint(ctx, params.ID, params.Task)
		if err != nil {
			return tools.NewTextErrorResponse(fmt.Sprintf("Resume failed: %s", err)), nil
		}
		return tools.NewTextResponse(fmt.Sprintf("Worker %s resumed as %s. Use manage_worker status/history or wait_for_workers to inspect the resumed execution.", params.ID, newID)), nil

	default:
		return tools.NewTextErrorResponse(fmt.Sprintf("Unknown action: %s", params.Action)), nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
