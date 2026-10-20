package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
)

type manageScheduleTool struct {
	q  *db.Queries
	db *sql.DB
}

const (
	ManageScheduleToolName = "manage_scheduled_tasks"
)

type ManageScheduleParams struct {
	Action string `json:"action"` // "list" or "cancel"
	TaskID string `json:"task_id,omitempty"`
}

func (t *manageScheduleTool) Info() ToolInfo {
	return ToolInfo{
		Name:        ManageScheduleToolName,
		Description: "Manage scheduled tasks (cron jobs). Actions: 'list' (list all active scheduled tasks), 'cancel' (cancel a scheduled task by ID).",
		Parameters: map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action to perform: 'list' or 'cancel'",
				"enum":        []string{"list", "cancel"},
			},
			"task_id": map[string]any{
				"type":        "string",
				"description": "The ID of the task to cancel (required for 'cancel' action)",
			},
		},
		Required: []string{"action"},
	}
}

func (t *manageScheduleTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params ManageScheduleParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	switch params.Action {
	case "list":
		tasks, err := t.q.ListActiveScheduledTasks(ctx)
		if err != nil {
			return ToolResponse{}, fmt.Errorf("failed to list tasks: %w", err)
		}

		ownerEmail := config.EffectiveOwnerEmail(ctx)
		owned := make([]db.ScheduledTask, 0, len(tasks))
		for _, task := range tasks {
			if strings.EqualFold(strings.TrimSpace(task.OwnerEmail), ownerEmail) {
				owned = append(owned, task)
			}
		}
		if len(owned) == 0 {
			return NewTextResponse("No active scheduled tasks found."), nil
		}

		var sb string
		sb += "Active Scheduled Tasks:\n"
		for _, task := range owned {
			nextRun := "N/A"
			if task.NextRunAt.Valid {
				nextRun = time.Unix(task.NextRunAt.Int64, 0).Format(time.RFC3339)
			}
			sb += fmt.Sprintf("- ID: %s\n  Type: %s\n  Content: %s\n  Next Run: %s\n\n",
				task.ID, task.TaskType, task.Parameters, nextRun)
		}
		return NewTextResponse(sb), nil

	case "cancel":
		if params.TaskID == "" {
			return NewTextErrorResponse("task_id is required for 'cancel' action"), nil
		}

		task, err := t.q.GetScheduledTask(ctx, params.TaskID)
		if err != nil || !strings.EqualFold(strings.TrimSpace(task.OwnerEmail), config.EffectiveOwnerEmail(ctx)) {
			return NewTextErrorResponse("Task not found"), nil
		}
		err = t.q.DeleteScheduledTask(ctx, params.TaskID)
		if err != nil {
			return ToolResponse{}, fmt.Errorf("failed to cancel task: %w", err)
		}

		return NewTextResponse(fmt.Sprintf("Task %s cancelled successfully.", params.TaskID)), nil

	default:
		return NewTextErrorResponse(fmt.Sprintf("Unknown action: %s", params.Action)), nil
	}
}

func NewManageScheduleTool(q *db.Queries, dbConn *sql.DB) BaseTool {
	return &manageScheduleTool{
		q:  q,
		db: dbConn,
	}
}
