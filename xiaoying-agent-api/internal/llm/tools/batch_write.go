package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/diff"
)

const (
	BatchWriteToolName    = "batch_write"
	batchWriteDescription = `Writes multiple files in a "best-effort" atomic batch.
It attempts to back up existing files before writing. If any write fails, it attempts to rollback all changes.
USE CASES:
- Refactoring multiple DIFFERENT files at once.
- Creating a set of related new files.
LIMITATIONS:
- Cannot be used to apply multiple edits to the SAME file.
- Overwrites entire file content.
- Output is limited to ~20000 characters. Full diff is in response metadata.
Params:
- files: A map where keys are file paths (absolute or relative to cwd) and values are the new file contents.
`
)

type BatchWriteParams struct {
	Files map[string]string `json:"files"`
}

type batchWriteTool struct {
	mu sync.Mutex
}

var (
	batchWriteFileWriter  = writeFileWithRetry
	batchWriteFileRemover = func(path string) error {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
)

func NewBatchWriteTool() BaseTool {
	return &batchWriteTool{}
}

func (t *batchWriteTool) Info() ToolInfo {
	return ToolInfo{
		Name:        BatchWriteToolName,
		Description: batchWriteDescription,
		Parameters: map[string]any{
			"files": map[string]any{
				"type":        "object",
				"description": "Map of file paths to their new content",
				"additionalProperties": map[string]any{
					"type": "string",
				},
			},
		},
		Required: []string{"files"},
	}
}

func (t *batchWriteTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var params BatchWriteParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		// Detect JSON truncation error for large output
		errMsg := err.Error()
		if strings.Contains(errMsg, "unexpected end of JSON input") || strings.Contains(errMsg, "invalid character") {
			return NewTextErrorResponse("JSON parse error (likely truncated because content is too large). Please write fewer files at once or use 'patch'/'edit' tool for large file modifications."), nil
		}
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %v", err)), nil
	}

	if len(params.Files) == 0 {
		return NewTextResponse("No files to write."), nil
	}

	// Total content size check
	totalSize := 0
	for _, content := range params.Files {
		totalSize += len(content)
	}
	if totalSize > MaxWriteContentChars {
		return NewTextErrorResponse(fmt.Sprintf(
			"Total content too large (%d chars across %d files, limit %d chars ≈ %d tokens). "+
				"This will cause JSON truncation. "+
				"Please write fewer files at once, use 'edit'/'patch' for modifications, or split into multiple batch_write calls.",
			totalSize, len(params.Files), MaxWriteContentChars, MaxWriteContentChars/4,
		)), nil
	}

	// Per-file size check
	for path, content := range params.Files {
		if len(content) > MaxWriteContentChars {
			return NewTextErrorResponse(fmt.Sprintf(
				"File %s content too large (%d chars, limit %d chars). "+
					"Use 'edit' or 'patch' tool instead.",
				path, len(content), MaxWriteContentChars,
			)), nil
		}
	}

	workDir := config.WorkingDirectory()
	backups := make(map[string][]byte)
	targetPaths := make(map[string]string) // relative -> absolute
	orderedRelPaths := make([]string, 0, len(params.Files))

	// 1. Validation and Backup
	for relPath := range params.Files {
		orderedRelPaths = append(orderedRelPaths, relPath)
		absPath := relPath
		if !filepath.IsAbs(relPath) {
			absPath = filepath.Join(workDir, relPath)
		}

		// Security check: ensure path is within workDir (optional but good practice)
		// For now, assuming agent is trusted or sandboxed elsewhere.

		targetPaths[relPath] = absPath

		// Backup if exists
		if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
			data, err := os.ReadFile(absPath)
			if err != nil {
				return NewTextErrorResponse(fmt.Sprintf("failed to read file for backup: %s (error: %v)", relPath, err)), nil
			}
			backups[absPath] = data
		} else if err == nil && info.IsDir() {
			return NewTextErrorResponse(fmt.Sprintf("cannot write to directory: %s", relPath)), nil
		}
		// If not exist, backup is "not exist" (handled by deletion on rollback)
	}
	sort.Strings(orderedRelPaths)

	// 2. Execution
	createdFiles := []string{}  // Absolute paths
	modifiedFiles := []string{} // Relative paths for reporting
	var diffBuilder strings.Builder
	lockPaths := make([]string, 0, len(targetPaths))
	for _, absPath := range targetPaths {
		lockPaths = append(lockPaths, absPath)
	}

	err := runWithEditFileLocks(ctx, lockPaths, func() error {
		for _, relPath := range orderedRelPaths {
			absPath := targetPaths[relPath]
			originalContent, existed := backups[absPath]
			info, statErr := os.Stat(absPath)
			if existed {
				if statErr != nil {
					if os.IsNotExist(statErr) {
						return newEditUserError("file changed after it was read. Read the file again before batch_write: %s", relPath)
					}
					return fmt.Errorf("failed to access file %s before batch_write: %w", relPath, statErr)
				}
				if info.IsDir() {
					return newEditUserError("cannot write to directory: %s", relPath)
				}
				currentContent, readErr := os.ReadFile(absPath)
				if readErr != nil {
					return fmt.Errorf("failed to read file %s before batch_write: %w", relPath, readErr)
				}
				if string(currentContent) != string(originalContent) {
					return newEditUserError("file changed after it was read. Read the file again before batch_write: %s", relPath)
				}
				continue
			}

			if statErr == nil {
				if info.IsDir() {
					return newEditUserError("cannot write to directory: %s", relPath)
				}
				return newEditUserError("file already exists: %s", relPath)
			}
			if !os.IsNotExist(statErr) {
				return fmt.Errorf("failed to access file %s before batch_write: %w", relPath, statErr)
			}
		}

		for _, relPath := range orderedRelPaths {
			content := params.Files[relPath]
			absPath := targetPaths[relPath]

			if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
				rollbackErr := t.rollback(ctx, backups, createdFiles)
				if rollbackErr != nil {
					return fmt.Errorf("failed to create directory for %s: %v (rollback failed: %v)", relPath, err, rollbackErr)
				}
				return fmt.Errorf("failed to create directory for %s: %v", relPath, err)
			}

			originalContent, existed := backups[absPath]
			fileDiff, _, _ := diff.GenerateDiff(string(originalContent), content, relPath)
			if fileDiff != "" {
				diffBuilder.WriteString(fileDiff)
				diffBuilder.WriteString("\n")
			}

			if err := batchWriteFileWriter(ctx, absPath, []byte(content), 0o644); err != nil {
				rollbackErr := t.rollback(ctx, backups, createdFiles)
				if rollbackErr != nil {
					return fmt.Errorf("failed to write file %s: %v (rollback failed: %v)", relPath, err, rollbackErr)
				}
				return fmt.Errorf("failed to write file %s: %v", relPath, err)
			}

			if existed {
				modifiedFiles = append(modifiedFiles, relPath)
			} else {
				createdFiles = append(createdFiles, absPath)
			}

			recordFileWrite(absPath)
			recordFileRead(absPath)
		}

		return nil
	})
	if err != nil {
		if response, _ := handleEditExecutionError(err); response.IsError {
			return response, nil
		}
		return NewTextErrorResponse(fmt.Sprintf("failed to write batch: %v. The file might be locked. DO NOT blindly retry writing. Tell the user about the issue.", err)), nil
	}

	// Prepare report
	createdRel := []string{}
	for _, p := range createdFiles {
		if rel, err := filepath.Rel(workDir, p); err == nil {
			createdRel = append(createdRel, rel)
		} else {
			createdRel = append(createdRel, p)
		}
	}

	resultText := fmt.Sprintf("Successfully wrote %d files.\nModified: %v\nCreated: %v", len(params.Files), modifiedFiles, createdRel)
	totalDiff := diffBuilder.String() // Keep for metadata only

	return WithResponseMetadata(
		NewTextResponse(resultText),
		map[string]any{
			"diff": totalDiff,
			"files": map[string]any{
				"modified": modifiedFiles,
				"created":  createdRel,
			},
		},
	), nil
}

func (t *batchWriteTool) rollback(ctx context.Context, backups map[string][]byte, createdFiles []string) error {
	var rollbackErrors []string
	backupPaths := make([]string, 0, len(backups))
	for path := range backups {
		backupPaths = append(backupPaths, path)
	}
	sort.Strings(backupPaths)
	// Restore modified files
	for _, path := range backupPaths {
		content := backups[path]
		if err := batchWriteFileWriter(ctx, path, content, 0o644); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("restore %s: %v", path, err))
		}
	}
	// Delete newly created files
	for _, path := range createdFiles {
		if err := batchWriteFileRemover(path); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("delete %s: %v", path, err))
		}
	}
	if len(rollbackErrors) > 0 {
		return errors.New(strings.Join(rollbackErrors, "; "))
	}
	return nil
}
