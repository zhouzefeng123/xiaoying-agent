package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	StopCommandToolName    = "stop_command"
	stopCommandDescription = `Stops a running background command.
Usage:
- command_id: The ID returned by 'run_command' (blocking=false).
`
)

type StopCommandParams struct {
	CommandID string `json:"command_id"`
}

type stopCommandTool struct{}

func NewStopCommandTool() BaseTool {
	return &stopCommandTool{}
}

func (t *stopCommandTool) Info() ToolInfo {
	return ToolInfo{
		Name:        StopCommandToolName,
		Description: stopCommandDescription,
		Parameters: map[string]any{
			"command_id": map[string]any{
				"type":        "string",
				"description": "ID of the command to stop.",
			},
		},
		Required: []string{"command_id"},
	}
}

func (t *stopCommandTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params StopCommandParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("Invalid JSON: %v", err)), nil
	}

	if params.CommandID == "" {
		return NewTextErrorResponse("command_id is required"), nil
	}

	bgMu.Lock()
	bgCmd, exists := bgCommands[params.CommandID]
	bgMu.Unlock()

	if !exists {
		return NewTextErrorResponse(fmt.Sprintf("Command ID %s not found.", params.CommandID)), nil
	}

	bgCmd.mu.Lock()
	defer bgCmd.mu.Unlock()

	if !bgCmd.Running {
		return NewTextResponse("Command is already finished."), nil
	}

	if bgCmd.Cmd != nil && bgCmd.Cmd.Process != nil {
		if err := bgCmd.Cmd.Process.Kill(); err != nil {
			return NewTextErrorResponse(fmt.Sprintf("Failed to kill process: %v", err)), nil
		}
		bgCmd.Running = false
		bgCmd.Stderr += "\n[Terminated by user]"
		return NewTextResponse(fmt.Sprintf("Command %s stopped successfully.", params.CommandID)), nil
	}

	return NewTextErrorResponse("Process not found or already exited."), nil
}
