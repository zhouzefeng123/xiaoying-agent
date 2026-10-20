package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/google/uuid"
)

type scheduleTaskTool struct {
	q  *db.Queries
	db *sql.DB
}

const (
	ScheduleTaskToolName = "schedule_task"
)

type ScheduleTaskParams struct {
	Type        string `json:"type"`
	TaskType    string `json:"task_type"` // Alias for Type
	Content     string `json:"content"`
	Schedule    string `json:"schedule"`     // e.g., "10s", "1h", "2024-01-01T12:00:00Z"
	IsRecurring bool   `json:"is_recurring"` // Whether the task should repeat
}

func (t *scheduleTaskTool) Info() ToolInfo {
	return ToolInfo{
		Name:        ScheduleTaskToolName,
		Description: "Schedule a future task or reminder. Distinguish between 3 types: 1. One-time (一次性): Run once at specific time or after delay (is_recurring=false). 2. Timed (定时): Run daily at specific time (is_recurring=true, schedule='daily:HH:MM'). 3. Recurring (循环): Run at fixed intervals (is_recurring=true, schedule='30m'). Supported types: 'general_reminder' and 'general_agent' (for executing complex agent tasks). Schedule format: duration (e.g. '10s', '1h'), ISO 8601 time, or 'daily:HH:MM'.",
		Parameters: map[string]any{
			"type": map[string]any{
				"type":        "string",
				"description": "Task type (supports 'general_reminder', 'general_agent')",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content of the reminder or task parameters",
			},
			"schedule": map[string]any{
				"type":        "string",
				"description": "When to run the task. One-time: '1m', '2024-01-01T12:00:00Z'. Timed: 'daily:09:00'. Recurring: '30m'.",
			},
			"is_recurring": map[string]any{
				"type":        "boolean",
				"description": "Whether the task should repeat. Set to false for One-time tasks. Set to true for Timed (daily) or Recurring (interval) tasks.",
			},
		},
		Required: []string{"type", "content", "schedule"},
	}
}

func (t *scheduleTaskTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params ScheduleTaskParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	// Handle alias
	if params.Type == "" && params.TaskType != "" {
		params.Type = params.TaskType
	}

	if params.Type != "general_reminder" && params.Type != "general_agent" {
		return NewTextErrorResponse("Currently only 'general_reminder' and 'general_agent' types are supported"), nil
	}

	// Calculate NextRunAt
	var nextRunTime time.Time
	var scheduleExpression sql.NullString

	// 1. Try parsing as "daily:HH:MM"
	var hour, minute int
	if n, err := fmt.Sscanf(params.Schedule, "daily:%d:%d", &hour, &minute); err == nil && n == 2 {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		nextRunTime = next
		scheduleExpression = sql.NullString{String: params.Schedule, Valid: true}
		params.IsRecurring = true // Force recurring for daily tasks
	} else {
		// 2. Try parsing as duration
		duration, err := time.ParseDuration(params.Schedule)
		if err == nil {
			nextRunTime = time.Now().Add(duration)
			if params.IsRecurring {
				scheduleExpression = sql.NullString{String: params.Schedule, Valid: true}
			}
		} else {
			// 3. Try parsing as specific time (ISO 8601)
			parsedTime, err := time.Parse(time.RFC3339, params.Schedule)
			if err == nil {
				nextRunTime = parsedTime
			} else {
				// Try simpler date format
				parsedTime, err = time.Parse("2006-01-02T15:04:05", params.Schedule)
				if err == nil {
					nextRunTime = parsedTime
				} else {
					return NewTextErrorResponse("Invalid schedule format. Supported formats: duration (e.g., '1m'), daily (e.g., 'daily:09:00'), or ISO 8601 time."), nil
				}
			}
			// Specific time cannot be recurring with a simple schedule expression unless we implement cron or interval logic.
			// For now, if it's a specific time, we ignore IsRecurring unless the user provides a cron expression (not supported yet).
		}
	}

	id := uuid.New().String()
	now := time.Now().Unix()

	dbParams := db.CreateScheduledTaskParams{
		ID:                 id,
		TaskType:           params.Type,
		Parameters:         params.Content,
		ScheduleExpression: scheduleExpression, // Only set if recurring
		IsActive:           sql.NullBool{Bool: true, Valid: true},
		CreatedAt:          now,
		NextRunAt:          sql.NullInt64{Int64: nextRunTime.Unix(), Valid: true},
		OwnerEmail:         config.EffectiveOwnerEmail(ctx),
	}

	_, err := t.q.CreateScheduledTask(ctx, dbParams)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("failed to create scheduled task: %w", err)
	}

	recurInfo := "One-time"
	if params.IsRecurring {
		recurInfo = "Recurring"
	}

	return NewTextResponse(fmt.Sprintf("Task scheduled successfully! ID: %s, Time: %s, Mode: %s", id, nextRunTime.Format(time.RFC3339), recurInfo)), nil
}

func NewScheduleTaskTool(q *db.Queries, dbConn *sql.DB) BaseTool {
	return &scheduleTaskTool{
		q:  q,
		db: dbConn,
	}
}
