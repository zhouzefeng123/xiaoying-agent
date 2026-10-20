package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/history"
)

const (
	FileHistoryToolName    = "manage_file_history"
	fileHistoryDescription = `Manages file history (snapshots).
Usage:
- action: "list", "read", "restore".
- file_path: Path to the file.
- version: Version ID (for "read" or "restore").
Features:
- List all available versions of a file.
- Read content of a specific version.
- Restore a file to a previous version.
`
)

type FileHistoryParams struct {
	Action   string `json:"action"`
	FilePath string `json:"file_path"`
	Version  string `json:"version"` // Optional for list, required for read/restore
}

type fileHistoryTool struct {
	files history.Service
}

func NewFileHistoryTool(files history.Service) BaseTool {
	return &fileHistoryTool{
		files: files,
	}
}

func (t *fileHistoryTool) Info() ToolInfo {
	return ToolInfo{
		Name:        FileHistoryToolName,
		Description: fileHistoryDescription,
		Parameters: map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "The action to perform: 'list' (show versions), 'read' (get content of version), 'restore' (revert file to version)",
				"enum":        []string{"list", "read", "restore"},
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "The path to the file.",
			},
			"version": map[string]any{
				"type":        "string",
				"description": "The version ID (e.g., 'v1', 'v172...') to read or restore. Required for 'read' and 'restore'.",
			},
		},
		Required: []string{"action", "file_path"},
	}
}

func (t *fileHistoryTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params FileHistoryParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("invalid parameters: %s", err)), nil
	}

	if params.FilePath == "" {
		return NewTextErrorResponse("file_path is required"), nil
	}

	// Handle relative paths
	filePath := params.FilePath
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(config.WorkingDirectoryFor(ctx), filePath)
	}

	switch params.Action {
	case "list":
		// List all versions for the file path
		relPath, err := filepath.Rel(config.WorkingDirectoryFor(ctx), filePath)
		if err != nil {
			relPath = filePath
		}

		versions, err := t.files.ListVersionsByPath(ctx, filePath)
		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("Failed to list versions: %v", err)), nil
		}

		if len(versions) == 0 {
			return NewTextResponse(fmt.Sprintf("No history found for file: %s", relPath)), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("History for %s:\n\n", relPath))
		for _, v := range versions {
			sb.WriteString(fmt.Sprintf("- Version: %s\n  Created: %s\n  Size: %d bytes\n",
				v.Version,
				time.Unix(v.CreatedAt, 0).Format(time.RFC3339),
				len(v.Content)))
		}
		return NewTextResponse(sb.String()), nil

	case "read":
		if params.Version == "" {
			return NewTextErrorResponse("version is required for read action"), nil
		}

		file, err := t.files.GetByPathAndVersion(ctx, filePath, params.Version)
		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("Failed to read version: %v", err)), nil
		}

		return NewTextResponse(file.Content), nil

	case "restore":
		if params.Version == "" {
			return NewTextErrorResponse("version is required for restore action"), nil
		}

		file, err := t.files.GetByPathAndVersion(ctx, filePath, params.Version)
		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("Failed to find version to restore: %v", err)), nil
		}

		err = os.WriteFile(filePath, []byte(file.Content), 0644)
		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("Failed to write file: %v", err)), nil
		}

		sessionID, _ := GetContextValues(ctx)
		if sessionID == "" {
			sessionID = "unknown"
		}

		_, err = t.files.CreateVersion(ctx, sessionID, filePath, file.Content)
		if err != nil {
			// log error
		}

		return NewTextResponse(fmt.Sprintf("Successfully restored %s to version %s", filePath, params.Version)), nil

	default:
		return NewTextErrorResponse("Invalid action. Must be 'list', 'read', or 'restore'."), nil
	}
}
