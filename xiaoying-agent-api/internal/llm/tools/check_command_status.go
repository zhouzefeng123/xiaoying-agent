package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	CheckCommandStatusToolName    = "check_command_status"
	checkCommandStatusDescription = `Checks the status of background commands.
Usage:
- command_id: Optional. The ID returned by 'run_command' (blocking=false).
- If 'command_id' is omitted, returns a list of ALL running background commands with their status and recent output.

Returns:
- Status (Running/Done).
- Output (Stdout/Stderr) so far.
`
)

type CheckCommandStatusParams struct {
	CommandID string `json:"command_id,omitempty"`
}

type checkCommandStatusTool struct{}

func NewCheckCommandStatusTool() BaseTool {
	return &checkCommandStatusTool{}
}

func (t *checkCommandStatusTool) Info() ToolInfo {
	return ToolInfo{
		Name:        CheckCommandStatusToolName,
		Description: checkCommandStatusDescription,
		Parameters: map[string]any{
			"command_id": map[string]any{
				"type":        "string",
				"description": "Optional. ID of the command to check. If empty, lists all running commands.",
			},
		},
	}
}

func (t *checkCommandStatusTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params CheckCommandStatusParams
	// Input might be empty if user wants to list all
	if call.Input != "" {
		if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
			// Try parsing as just the ID string if simple string passed (robustness)
			// But standard is JSON. Let's assume JSON.
			// If error, it might be empty input which is fine?
			// Actually json.Unmarshal on empty string returns error.
			// If call.Input is "{}" it works.
		}
	}

	bgMu.Lock()
	defer bgMu.Unlock()

	// If CommandID is provided, check specific command
	if params.CommandID != "" {
		bgCmd, exists := bgCommands[params.CommandID]
		if !exists {
			return NewTextErrorResponse(fmt.Sprintf("Command ID %s not found.", params.CommandID)), nil
		}

		return t.formatCommandStatus(bgCmd, true), nil
	}

	// List all running commands
	var sb strings.Builder
	sb.WriteString("Background Commands:\n")
	count := 0
	for id, cmd := range bgCommands {
		if cmd.Running {
			count++
			sb.WriteString(fmt.Sprintf("\n--- Command ID: %s ---\n", id))
			// Don't show full output in list mode, just summary
			status := t.formatCommandStatus(cmd, false)
			sb.WriteString(status.Content)
		}
	}

	if count == 0 {
		return NewTextResponse("No background commands are currently running."), nil
	}

	return NewTextResponse(sb.String()), nil
}

func (t *checkCommandStatusTool) formatCommandStatus(bgCmd *BackgroundCommand, includeFullOutput bool) ToolResponse {
	bgCmd.mu.Lock()
	defer bgCmd.mu.Unlock()

	status := "Running"
	if !bgCmd.Running {
		status = "Done"
	} else if bgCmd.StreamParse && bgCmd.Progress > 0 {
		status = fmt.Sprintf("Running (%.1f%%)", bgCmd.Progress)
		if bgCmd.ProgressDetails != "" {
			status += fmt.Sprintf(" - %s", bgCmd.ProgressDetails)
		}
	}

	var output string
	if includeFullOutput {
		output = bgCmd.Stdout
		if bgCmd.Stderr != "" {
			output += "\n[Stderr]: " + bgCmd.Stderr
		}
		if bgCmd.Running && output == "" {
			output = "(Command is running but no output yet)"
		}
		return NewTextResponse(fmt.Sprintf("Status: %s\nOutput:\n%s", status, output))
	} else {
		// Summary mode
		preview := bgCmd.Stdout
		if len(preview) > 100 {
			preview = preview[len(preview)-100:] + "..."
		}
		if bgCmd.Stderr != "" {
			preview += " (Has Stderr)"
		}
		return NewTextResponse(fmt.Sprintf("Status: %s | Start Time: %s\nLast Output: %s", status, bgCmd.StartTime.Format("15:04:05"), preview))
	}
}
