package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/auramate/auramate/internal/diff"
	"github.com/auramate/auramate/internal/history"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/lsp"
	"github.com/auramate/auramate/internal/permission"
)

const (
	PatchToolName    = "patch"
	patchDescription = `Applies unified diffs to multiple files.
Usage:
- patch_text: Unified diff content.
Requirements:
- Absolute paths.
- Unique context lines.
- Exact whitespace matching.
`
)

type PatchParams struct {
	PatchText string `json:"patch_text"`
}

type PatchResponseMetadata struct {
	FilesChanged []string `json:"files_changed"`
	Additions    int      `json:"additions"`
	Removals     int      `json:"removals"`
}

type patchTool struct {
	lspRegistry *lsp.Registry
	permissions permission.Service
	files       history.Service
}

func NewPatchTool(lspRegistry *lsp.Registry, permissions permission.Service, files history.Service) BaseTool {
	return &patchTool{
		lspRegistry: lspRegistry,
		permissions: permissions,
		files:       files,
	}
}

func (p *patchTool) Info() ToolInfo {
	return ToolInfo{
		Name:        PatchToolName,
		Description: patchDescription,
		Parameters: map[string]any{
			"patch_text": map[string]any{
				"type":        "string",
				"description": "The full patch text that describes all changes to be made",
			},
		},
		Required: []string{"patch_text"},
	}
}

func (p *patchTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params PatchParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse("invalid parameters"), nil
	}

	if params.PatchText == "" {
		return NewTextErrorResponse("patch_text is required"), nil
	}

	// Identify all files needed for the patch and verify they've been read
	filesToRead := diff.IdentifyFilesNeeded(params.PatchText)
	for _, filePath := range filesToRead {
		absPath := ResolvePathFor(ctx, filePath)

		if getLastReadTime(absPath).IsZero() {
			return NewTextErrorResponse(fmt.Sprintf("you must read the file %s before patching it. Use the FileRead tool first", filePath)), nil
		}

		fileInfo, err := os.Stat(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				return NewTextErrorResponse(fmt.Sprintf("file not found: %s", absPath)), nil
			}
			return ToolResponse{}, fmt.Errorf("failed to access file: %w", err)
		}

		if fileInfo.IsDir() {
			return NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", absPath)), nil
		}

		modTime := fileInfo.ModTime()
		lastRead := getLastReadTime(absPath)
		if modTime.After(lastRead) {
			return NewTextErrorResponse(
				fmt.Sprintf("file %s has been modified since it was last read (mod time: %s, last read: %s)",
					absPath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339),
				)), nil
		}
	}

	// Check for new files to ensure they don't already exist
	filesToAdd := diff.IdentifyFilesAdded(params.PatchText)
	for _, filePath := range filesToAdd {
		absPath := ResolvePathFor(ctx, filePath)

		_, err := os.Stat(absPath)
		if err == nil {
			return NewTextErrorResponse(fmt.Sprintf("file already exists and cannot be added: %s", absPath)), nil
		} else if !os.IsNotExist(err) {
			return ToolResponse{}, fmt.Errorf("failed to check file: %w", err)
		}
	}

	// Load all required files
	currentFiles := make(map[string]string)
	for _, filePath := range filesToRead {
		absPath := ResolvePathFor(ctx, filePath)

		content, err := os.ReadFile(absPath)
		if err != nil {
			return ToolResponse{}, fmt.Errorf("failed to read file %s: %w", absPath, err)
		}
		currentFiles[filePath] = string(content)
	}

	// Process the patch
	patch, fuzz, err := diff.TextToPatch(params.PatchText, currentFiles)
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("failed to parse patch: %s", err)), nil
	}

	if fuzz > 3 {
		return NewTextErrorResponse(fmt.Sprintf("patch contains fuzzy matches (fuzz level: %d). Please make your context lines more precise", fuzz)), nil
	}

	// Convert patch to commit
	commit, err := diff.PatchToCommit(patch, currentFiles)
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("failed to create commit from patch: %s", err)), nil
	}

	// Get session ID and message ID
	sessionID, messageID := GetContextValues(ctx)
	if sessionID == "" || messageID == "" {
		return ToolResponse{}, fmt.Errorf("session ID and message ID are required for creating a patch")
	}

	lockPaths := make([]string, 0, len(commit.Changes)*2)
	for path, change := range commit.Changes {
		lockPaths = append(lockPaths, ResolvePathFor(ctx, path))
		if change.MovePath != nil {
			lockPaths = append(lockPaths, ResolvePathFor(ctx, *change.MovePath))
		}
	}

	// Request permission for all changes
	for path, change := range commit.Changes {
		switch change.Type {
		case diff.ActionAdd:
			dir := filepath.Dir(path)
			patchDiff, _, _ := diff.GenerateDiff("", *change.NewContent, path)
			p := p.permissions.Request(
				permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        dir,
					ToolName:    PatchToolName,
					Action:      "create",
					Description: fmt.Sprintf("Create file %s", path),
					Params: EditPermissionsParams{
						FilePath: path,
						Diff:     patchDiff,
					},
				},
			)
			if !p {
				return ToolResponse{}, permission.ErrorPermissionDenied
			}
		case diff.ActionUpdate:
			currentContent := ""
			if change.OldContent != nil {
				currentContent = *change.OldContent
			}
			newContent := ""
			if change.NewContent != nil {
				newContent = *change.NewContent
			}
			patchDiff, _, _ := diff.GenerateDiff(currentContent, newContent, path)
			dir := filepath.Dir(path)

			description := fmt.Sprintf("Update file %s", path)
			if change.MovePath != nil {
				description = fmt.Sprintf("Move file %s to %s", path, *change.MovePath)
			}

			p := p.permissions.Request(
				permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        dir,
					ToolName:    PatchToolName,
					Action:      "update",
					Description: description,
					Params: EditPermissionsParams{
						FilePath: path,
						Diff:     patchDiff,
					},
				},
			)
			if !p {
				return ToolResponse{}, permission.ErrorPermissionDenied
			}
		case diff.ActionDelete:
			dir := filepath.Dir(path)
			patchDiff, _, _ := diff.GenerateDiff(*change.OldContent, "", path)
			p := p.permissions.Request(
				permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        dir,
					ToolName:    PatchToolName,
					Action:      "delete",
					Description: fmt.Sprintf("Delete file %s", path),
					Params: EditPermissionsParams{
						FilePath: path,
						Diff:     patchDiff,
					},
				},
			)
			if !p {
				return ToolResponse{}, permission.ErrorPermissionDenied
			}
		}
	}

	// Apply the changes to the filesystem while holding stable per-file locks.
	err = runWithEditFileLocks(ctx, lockPaths, func() error {
		for _, filePath := range filesToRead {
			absPath := ResolvePathFor(ctx, filePath)
			content, readErr := os.ReadFile(absPath)
			if readErr != nil {
				if os.IsNotExist(readErr) {
					return newEditUserError("file changed after it was read. Read the file again before patching: %s", absPath)
				}
				return fmt.Errorf("failed to re-read file %s before patching: %w", absPath, readErr)
			}
			if string(content) != currentFiles[filePath] {
				return newEditUserError("file changed after it was read. Read the file again before patching: %s", absPath)
			}
		}

		for _, filePath := range filesToAdd {
			absPath := ResolvePathFor(ctx, filePath)
			_, statErr := os.Stat(absPath)
			if statErr == nil {
				return newEditUserError("file already exists and cannot be added: %s", absPath)
			}
			if !os.IsNotExist(statErr) {
				return fmt.Errorf("failed to check file %s before patching: %w", absPath, statErr)
			}
		}

		return diff.ApplyCommit(commit, func(path string, content string) error {
			absPath := ResolvePathFor(ctx, path)

			// Create parent directories if needed
			dir := filepath.Dir(absPath)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("failed to create parent directories for %s: %w", absPath, err)
			}

			return writeFileWithRetry(ctx, absPath, []byte(content), 0o644)
		}, func(path string) error {
			absPath := ResolvePathFor(ctx, path)
			if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		})
	})
	if err != nil {
		if response, _ := handleEditExecutionError(err); response.IsError {
			return response, nil
		}
		return NewTextErrorResponse(fmt.Sprintf("failed to apply patch: %s. If this is a file lock/permission error, DO NOT blindly retry patching. Tell the user.", err)), nil
	}

	// Update file history for all modified files
	changedFiles := []string{}
	totalAdditions := 0
	totalRemovals := 0

	for path, change := range commit.Changes {
		absPath := ResolvePathFor(ctx, path)
		changedFiles = append(changedFiles, absPath)

		oldContent := ""
		if change.OldContent != nil {
			oldContent = *change.OldContent
		}

		newContent := ""
		if change.NewContent != nil {
			newContent = *change.NewContent
		}

		// Calculate diff statistics
		_, additions, removals := diff.GenerateDiff(oldContent, newContent, path)
		totalAdditions += additions
		totalRemovals += removals

		// Handle Move specially
		if change.Type == diff.ActionUpdate && change.MovePath != nil {
			newAbsPath := ResolvePathFor(ctx, *change.MovePath)
			changedFiles = append(changedFiles, newAbsPath)

			// 1. Mark old file as deleted in history
			_, err = p.files.CreateVersion(ctx, sessionID, absPath, "")
			if err != nil {
				logging.Debug("Error creating file history version (move delete)", "error", err)
			}

			// 2. Create history for new file
			_, err = p.files.Create(ctx, sessionID, newAbsPath, newContent)
			if err != nil {
				// Fallback to CreateVersion if Create fails (e.g. if it somehow exists in DB)
				_, err = p.files.CreateVersion(ctx, sessionID, newAbsPath, newContent)
				if err != nil {
					logging.Debug("Error creating file history (move create)", "error", err)
				}
			}

			recordFileWrite(newAbsPath)
			recordFileRead(newAbsPath)
			continue
		}

		// Update history
		file, err := p.files.GetByPathAndSession(ctx, absPath, sessionID)
		if err != nil && change.Type != diff.ActionAdd {
			// If not adding a file, create history entry for existing file
			_, err = p.files.Create(ctx, sessionID, absPath, oldContent)
			if err != nil {
				logging.Debug("Error creating file history", "error", err)
			}
		}

		if err == nil && change.Type != diff.ActionAdd && file.Content != oldContent {
			// User manually changed content, store intermediate version
			_, err = p.files.CreateVersion(ctx, sessionID, absPath, oldContent)
			if err != nil {
				logging.Debug("Error creating file history version", "error", err)
			}
		}

		// Store new version
		if change.Type == diff.ActionDelete {
			_, err = p.files.CreateVersion(ctx, sessionID, absPath, "")
		} else {
			_, err = p.files.CreateVersion(ctx, sessionID, absPath, newContent)
		}
		if err != nil {
			logging.Debug("Error creating file history version", "error", err)
		}

		// Record file operations
		recordFileWrite(absPath)
		recordFileRead(absPath)
	}

	// Run LSP diagnostics on all changed files
	for _, filePath := range changedFiles {
		waitForLspDiagnostics(ctx, filePath, p.lspRegistry)
	}

	result := fmt.Sprintf("Patch applied successfully. %d files changed, %d additions, %d removals",
		len(changedFiles), totalAdditions, totalRemovals)

	diagnosticsText := ""
	for _, filePath := range changedFiles {
		diagnosticsText += getDiagnostics(filePath, p.lspRegistry)
		diagnosticsText += lspStatusNoteForFile(filePath, p.lspRegistry)
	}

	if diagnosticsText != "" {
		result += "\n\nDiagnostics:\n" + diagnosticsText
	}
	for _, filePath := range changedFiles {
		result += getPythonSyntaxDiagnostics(ctx, filePath)
	}

	return WithResponseMetadata(
		NewTextResponse(result),
		PatchResponseMetadata{
			FilesChanged: changedFiles,
			Additions:    totalAdditions,
			Removals:     totalRemovals,
		}), nil
}
