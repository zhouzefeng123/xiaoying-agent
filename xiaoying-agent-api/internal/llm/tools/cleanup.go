package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/logging"
)

const (
	CleanupToolName    = "cleanup_workspace"
	cleanupDescription = `Cleans up temporary artifacts in the workspace.
Usage:
- target: What to clean ("scripts", "images", "all").
  - "scripts": Deletes workspace/scripts/*.{py,sh,ps1,bat}
  - "images": Deletes workspace/images/web_search/* (Does NOT delete text-to-image results)
  - "all": Deletes both scripts and web search images.
- older_than: Optional duration (e.g. "1h", "24h"). If set, only deletes files older than this.
`
)

type CleanupParams struct {
	Target    string `json:"target"`     // scripts, images, all
	OlderThan string `json:"older_than"` // duration string
}

type cleanupTool struct{}

func NewCleanupTool() BaseTool {
	return &cleanupTool{}
}

func (t *cleanupTool) Info() ToolInfo {
	return ToolInfo{
		Name:        CleanupToolName,
		Description: cleanupDescription,
		Parameters: map[string]any{
			"target": map[string]any{
				"type":        "string",
				"description": "Target to clean: 'scripts', 'images' (web search), or 'all'.",
				"enum":        []string{"scripts", "images", "all"},
			},
			"older_than": map[string]any{
				"type":        "string",
				"description": "Optional duration (e.g., '1h', '30m'). Only delete files older than this.",
			},
		},
		Required: []string{"target"},
	}
}

func (t *cleanupTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params CleanupParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse("invalid parameters"), nil
	}

	if params.Target == "" {
		params.Target = "all"
	}

	var cutoffTime time.Time
	if params.OlderThan != "" {
		d, err := time.ParseDuration(params.OlderThan)
		if err != nil {
			return NewTextErrorResponse("invalid older_than format (use 1h, 30m)"), nil
		}
		cutoffTime = time.Now().Add(-d)
	}

	wd := config.WorkingDirectoryFor(ctx)
	deletedCount := 0
	errors := []string{}

	// Helper to clean directory
	cleanDir := func(dir string, filter func(string) bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				errors = append(errors, fmt.Sprintf("failed to read %s: %v", dir, err))
			}
			return
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			path := filepath.Join(dir, entry.Name())

			// Filter check
			if filter != nil && !filter(entry.Name()) {
				continue
			}

			// Time check
			if !cutoffTime.IsZero() {
				info, err := entry.Info()
				if err == nil && info.ModTime().After(cutoffTime) {
					continue // Too new
				}
			}

			if err := os.Remove(path); err != nil {
				errors = append(errors, fmt.Sprintf("failed to delete %s: %v", entry.Name(), err))
			} else {
				deletedCount++
			}
		}
	}

	// Clean Scripts
	if params.Target == "scripts" || params.Target == "all" {
		scriptDir := filepath.Join(wd, "workspace", "scripts")
		cleanDir(scriptDir, func(name string) bool {
			ext := filepath.Ext(name)
			return ext == ".py" || ext == ".sh" || ext == ".ps1" || ext == ".bat"
		})
	}

	// Clean Images (Web Search Only)
	if params.Target == "images" || params.Target == "all" {
		// Only clean web_search folder to avoid deleting generated art
		imgDir := filepath.Join(wd, "workspace", "images", "web_search")
		cleanDir(imgDir, nil) // Delete all in this folder
	}

	msg := fmt.Sprintf("Cleanup complete. Deleted %d files.", deletedCount)
	if len(errors) > 0 {
		msg += fmt.Sprintf("\nErrors: %v", errors)
		logging.Warn("Cleanup errors", "errors", errors)
	}

	return NewTextResponse(msg), nil
}

// cleanupBackgroundCommands removes finished commands older than 1 hour
func cleanupBackgroundCommands() {
	bgMu.Lock()
	defer bgMu.Unlock()

	now := time.Now()
	for id, cmd := range bgCommands {
		if !cmd.Running && now.Sub(cmd.StartTime) > 1*time.Hour {
			delete(bgCommands, id)
		}
	}
}
