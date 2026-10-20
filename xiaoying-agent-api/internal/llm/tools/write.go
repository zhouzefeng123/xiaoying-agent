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
	"github.com/auramate/auramate/internal/diff"
	"github.com/auramate/auramate/internal/history"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/lsp"
	"github.com/auramate/auramate/internal/permission"
)

const (
	WriteToolName        = "write"
	MaxWriteContentChars = 23000
	writeDescription     = `Writes content to a file.
USAGE:
- file_path: Path to the file.
- content: FULL content to write.
BEHAVIOR:
- OVERWRITES existing file completely. NOT append.
- Creates parent directories automatically.
- To edit a file, use 'edit' tool instead.
WARNING:
- DO NOT use this tool for writing files larger than 500 lines. The JSON argument WILL be truncated and cause a 400 Error. Use 'patch' for large files.
- Output is limited to ~25000 characters. Full diff is in response metadata.
`
)

type WriteParams struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
	// Aliases
	FileName string `json:"file_name"`
	Path     string `json:"path"`
	Code     string `json:"code"`
}

type WritePermissionsParams struct {
	FilePath string `json:"file_path"`
	Diff     string `json:"diff"`
}

type writeTool struct {
	lspRegistry *lsp.Registry
	permissions permission.Service
	files       history.Service
}

type WriteResponseMetadata struct {
	Diff      string `json:"diff"`
	Additions int    `json:"additions"`
	Removals  int    `json:"removals"`
}

func NewWriteTool(lspRegistry *lsp.Registry, permissions permission.Service, files history.Service) BaseTool {
	return &writeTool{
		lspRegistry: lspRegistry,
		permissions: permissions,
		files:       files,
	}
}

func (w *writeTool) Info() ToolInfo {
	return ToolInfo{
		Name:        WriteToolName,
		Description: writeDescription,
		Parameters: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write to the file",
			},
		},
		Required: []string{"file_path", "content"},
	}
}

func (w *writeTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params WriteParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		// Provide a more helpful error message if JSON is truncated
		errMsg := err.Error()
		if strings.Contains(errMsg, "unexpected end of JSON input") || strings.Contains(errMsg, "invalid character") {
			return NewTextErrorResponse("JSON parse error (likely truncated because content is too large). Please use the 'patch' or 'edit' tool for targeted chunked changes. If you are creating a very large file, create a small scaffold first and expand it incrementally."), nil
		}
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	// Parameter Normalization
	if params.FilePath == "" {
		if params.FileName != "" {
			params.FilePath = params.FileName
		} else if params.Path != "" {
			params.FilePath = params.Path
		}
	}
	if params.Content == "" {
		if params.Code != "" {
			params.Content = params.Code
		}
	}

	if params.FilePath == "" {
		return NewTextErrorResponse("file_path is required (or use 'file_name'/'path')"), nil
	}

	if params.Content == "" {
		return NewTextErrorResponse("content is required (or use 'code')"), nil
	}

	// Content size pre-check to prevent JSON truncation
	if len(params.Content) > MaxWriteContentChars {
		return NewTextErrorResponse(fmt.Sprintf(
			"Content too large (%d chars, limit %d chars ≈ %d tokens). "+
				"This will cause JSON truncation. "+
				"Please use 'edit' or 'patch' for targeted chunked changes. If this is a brand-new large file, create a small scaffold first and expand it incrementally.",
			len(params.Content), MaxWriteContentChars, MaxWriteContentChars/4,
		)), nil
	}

	filePath := params.FilePath
	// Use ResolvePath to handle both absolute and relative paths robustly
	filePath = ResolvePathFor(ctx, filePath)

	fileInfo, err := os.Stat(filePath)
	if err == nil {
		if fileInfo.IsDir() {
			return NewTextErrorResponse(fmt.Sprintf("Path is a directory, not a file: %s", filePath)), nil
		}

		modTime := fileInfo.ModTime()
		lastRead := getLastReadTime(filePath)
		if modTime.After(lastRead) {
			return NewTextErrorResponse(fmt.Sprintf("File %s has been modified since it was last read.\nLast modification: %s\nLast read: %s\n\nPlease read the file again before modifying it.",
				filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339))), nil
		}

		oldContent, readErr := os.ReadFile(filePath)
		if readErr == nil && string(oldContent) == params.Content {
			return NewTextErrorResponse(fmt.Sprintf("File %s already contains the exact content. No changes made.", filePath)), nil
		}
	} else if !os.IsNotExist(err) {
		return ToolResponse{}, fmt.Errorf("error checking file: %w", err)
	}

	dir := filepath.Dir(filePath)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return ToolResponse{}, fmt.Errorf("error creating directory: %w", err)
	}

	oldContent := ""
	if fileInfo != nil && !fileInfo.IsDir() {
		oldBytes, readErr := os.ReadFile(filePath)
		if readErr == nil {
			oldContent = string(oldBytes)
		}
	}

	sessionID, messageID := GetContextValues(ctx)
	if sessionID == "" || messageID == "" {
		return ToolResponse{}, fmt.Errorf("session_id and message_id are required")
	}

	diff, additions, removals := diff.GenerateDiff(
		oldContent,
		params.Content,
		filePath,
	)

	rootDir := config.WorkingDirectoryFor(ctx)
	permissionPath := filepath.Dir(filePath)
	if strings.HasPrefix(filePath, rootDir) {
		permissionPath = rootDir
	}
	p := w.permissions.Request(
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        permissionPath,
			ToolName:    WriteToolName,
			Action:      "write",
			Description: fmt.Sprintf("Create file %s", filePath),
			Params: WritePermissionsParams{
				FilePath: filePath,
				Diff:     diff,
			},
		},
	)
	if !p {
		return ToolResponse{}, permission.ErrorPermissionDenied
	}

	// NOTE: We DO NOT add BOM anymore. Python and many Linux tools hate BOM.
	// If users need BOM for Windows Notepad, they should explicitly request it or configure their editor.
	// Previously we forced BOM which broke Python scripts.
	contentBytes := []byte(params.Content)
	if strings.HasPrefix(params.Content, "\ufeff") {
		// If the LLM explicitly added BOM in the content string, we strip it to be safe,
		// or we can leave it. But generally, we want "clean" UTF-8.
		// Let's strip it if it's there to enforce consistency.
		contentBytes = []byte(strings.TrimPrefix(params.Content, "\ufeff"))
	}

	// Open file for writing
	f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("failed to open file for writing: %w. The file may be locked. DO NOT blindly retry writing. Tell the user about the lock.", err)
	}

	// Use ContextAwareWriter for unified cancellation (Phase 5)
	cw := NewContextAwareWriter(ctx, f)
	_, err = cw.Write(contentBytes)
	f.Close()
	if err != nil {
		return ToolResponse{}, fmt.Errorf("write aborted: %w", err)
	}

	// [FIX] Verify file write to prevent hallucinations
	if err := CheckCancellation(ctx); err != nil {
		return ToolResponse{}, err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("write operation reported success but verification failed: %w", err)
	}
	// Note: We don't check for size > 0 because sometimes we legitimately write empty files.
	// But we could check if content was not empty but file is.
	if len(contentBytes) > 0 && info.Size() == 0 {
		return ToolResponse{}, fmt.Errorf("write operation reported success but file is empty (expected %d bytes). The file may be locked by another process. DO NOT blindly retry writing. Tell the user about the lock.", len(contentBytes))
	}

	if err := CheckCancellation(ctx); err != nil {
		return ToolResponse{}, err
	}

	file, err := w.files.GetByPathAndSession(ctx, filePath, sessionID)
	if err != nil {
		if err := CheckCancellation(ctx); err != nil {
			return ToolResponse{}, err
		}
		_, err = w.files.Create(ctx, sessionID, filePath, oldContent)
		if err != nil {
			// Log error but don't fail the operation
			return ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
		}
	}
	if file.Content != oldContent {
		if err := CheckCancellation(ctx); err != nil {
			return ToolResponse{}, err
		}
		// User Manually changed the content store an intermediate version
		_, err = w.files.CreateVersion(ctx, sessionID, filePath, oldContent)
		if err != nil {
			logging.Debug("Error creating file history version", "error", err)
		}
	}

	if err := CheckCancellation(ctx); err != nil {
		return ToolResponse{}, err
	}

	// Store the new version
	_, err = w.files.CreateVersion(ctx, sessionID, filePath, params.Content)
	if err != nil {
		logging.Debug("Error creating file history version", "error", err)
	}

	recordFileWrite(filePath)
	recordFileRead(filePath)
	waitForLspDiagnostics(ctx, filePath, w.lspRegistry)

	result := fmt.Sprintf("<result>\nFile successfully written: %s\n</result>", filePath)
	result += getDiagnostics(filePath, w.lspRegistry)
	result += lspStatusNoteForFile(filePath, w.lspRegistry)

	return WithResponseMetadata(NewTextResponse(result),
		WriteResponseMetadata{
			Diff:      diff,
			Additions: additions,
			Removals:  removals,
		},
	), nil
}
