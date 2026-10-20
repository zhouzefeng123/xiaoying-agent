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
	"github.com/auramate/auramate/internal/permission"
)

const (
	DeleteToolName    = "delete_file"
	deleteDescription = `Deletes files or directories permanently.
USAGE:
- file_paths: Array of absolute paths to delete.
- Can delete files or directories (recursive).
SAFETY:
- Verify path before deletion.
- Operation is irreversible.
RESTRICTIONS:
- Can ONLY delete files within the working directory.
- Attempting to delete files outside will fail.
`
)

type DeleteParams struct {
	FilePaths []string `json:"file_paths"`
}

type DeletePermissionsParams struct {
	FilePaths []string `json:"file_paths"`
}

type deleteTool struct {
	permissions permission.Service
}

func NewDeleteTool(permissions permission.Service) BaseTool {
	return &deleteTool{
		permissions: permissions,
	}
}

func (d *deleteTool) Info() ToolInfo {
	return ToolInfo{
		Name:        DeleteToolName,
		Description: deleteDescription,
		Parameters: map[string]any{
			"file_paths": map[string]any{
				"type":        "array",
				"items":       map[string]string{"type": "string"},
				"description": "List of absolute paths to files or directories to delete.",
			},
		},
		Required: []string{"file_paths"},
	}
}

func (d *deleteTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params DeleteParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	if len(params.FilePaths) == 0 {
		return NewTextErrorResponse("file_paths is required and cannot be empty"), nil
	}

	sessionID, _ := GetContextValues(ctx)
	if sessionID == "" {
		return ToolResponse{}, fmt.Errorf("session_id is required")
	}

	wd := config.WorkingDirectoryFor(ctx)
	var pathsToDelete []string

	// Validate paths
	for _, path := range params.FilePaths {
		absPath := path
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(wd, absPath)
		}

		// Security check: ensure path is within working directory
		rel, err := filepath.Rel(wd, absPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return NewTextErrorResponse(fmt.Sprintf("cannot delete path outside of working directory: %s", path)), nil
		}

		pathsToDelete = append(pathsToDelete, absPath)
	}

	// Permission check
	p := d.permissions.Request(
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        wd,
			ToolName:    DeleteToolName,
			Action:      "delete",
			Description: fmt.Sprintf("Delete %d files/directories", len(pathsToDelete)),
			Params: DeletePermissionsParams{
				FilePaths: pathsToDelete,
			},
		},
	)
	if !p {
		return ToolResponse{}, permission.ErrorPermissionDenied
	}

	var results []string
	var errors []string

	for _, path := range pathsToDelete {
		// Check if file exists
		if _, err := os.Stat(path); os.IsNotExist(err) {
			results = append(results, fmt.Sprintf("Skipped (not found): %s", path))
			continue
		}

		err := removeAllWithRetry(path)
		if err != nil {
			errors = append(errors, fmt.Sprintf("Failed to delete %s: %v", path, err))
		} else {
			results = append(results, fmt.Sprintf("Deleted: %s", path))
		}
	}

	output := strings.Join(results, "\n")
	if len(errors) > 0 {
		output += "\nErrors:\n" + strings.Join(errors, "\n")
	}

	return NewTextResponse(output), nil
}

func removeAllWithRetry(path string) error {
	var err error
	for i := 0; i < 5; i++ {
		err = os.RemoveAll(path)
		if err == nil {
			return nil
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "process cannot access") || strings.Contains(errMsg, "used by another process") {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return err
	}
	return fmt.Errorf("failed to delete after 5 attempts: %w", err)
}
