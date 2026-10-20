package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/diff"
	"github.com/auramate/auramate/internal/history"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/lsp"
	"github.com/auramate/auramate/internal/permission"
	"github.com/gofrs/flock"
)

const (
	EditToolName    = "edit"
	editDescription = `Edits a file by replacing a unique text block.
USAGE:
- file_path: Absolute path to file.
- old_string: Exact block of lines to replace.
- new_string: New block of lines to insert.
CRITICAL RULES:
1. 'old_string' MUST match exactly (including whitespace/indentation).
2. 'old_string' MUST be UNIQUE in the file.
3. Include 2-3 lines of surrounding context to ensure uniqueness.
4. DO NOT include the entire file content in 'old_string'.
5. DO NOT replace more than 500 lines at once. Split large edits into multiple calls to prevent JSON truncation 400 errors.
CONCURRENCY WARNING:
- You should try to bundle changes into a single 'edit' or 'patch' call if possible.
- If you call 'edit' multiple times on the SAME file in parallel, the system will execute them sequentially.
- Make sure 'old_string' for each concurrent edit accounts for the original file state, as fuzzy matching will attempt to apply them.
OUTPUT:
- Output is limited to ~25000 characters. Full diff is in response metadata.
`
)

type EditParams struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	OldContent string `json:"old_content"` // Alias for old_string
	NewContent string `json:"new_content"` // Alias for new_string
}

type EditPermissionsParams struct {
	FilePath string `json:"file_path"`
	Diff     string `json:"diff"`
}

type EditResponseMetadata struct {
	Diff      string `json:"diff"`
	Additions int    `json:"additions"`
	Removals  int    `json:"removals"`
}

type editTool struct {
	lspRegistry *lsp.Registry
	permissions permission.Service
	files       history.Service
}

type editUserError struct {
	message string
}

func (e *editUserError) Error() string {
	return e.message
}

func newEditUserError(format string, args ...any) error {
	return &editUserError{message: fmt.Sprintf(format, args...)}
}

func handleEditExecutionError(err error) (ToolResponse, error) {
	if err == nil {
		return ToolResponse{}, nil
	}

	var userErr *editUserError
	if errors.As(err, &userErr) {
		return NewTextErrorResponse(userErr.Error()), nil
	}

	return ToolResponse{}, err
}

func NewEditTool(lspRegistry *lsp.Registry, permissions permission.Service, files history.Service) BaseTool {
	return &editTool{
		lspRegistry: lspRegistry,
		permissions: permissions,
		files:       files,
	}
}

func (e *editTool) Info() ToolInfo {
	return ToolInfo{
		Name:        EditToolName,
		Description: editDescription,
		Parameters: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to modify",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "The text to replace",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "The text to replace it with",
			},
		},
		Required: []string{"file_path", "old_string", "new_string"},
	}
}

// Map concurrent edits by filepath to ensure they run sequentially in the same process
var (
	editFileMutexes sync.Map
)

const (
	editLockRetryCount = 120
	editLockRetryDelay = 250 * time.Millisecond
	editLockNameBytes  = 8
	editLockLabelLimit = 24
)

func getEditFileMutex(filePath string) *sync.Mutex {
	fileMuAny, _ := editFileMutexes.LoadOrStore(filePath, &sync.Mutex{})
	return fileMuAny.(*sync.Mutex)
}

func sanitizeEditLockLabel(label string) string {
	if label == "" {
		return "file"
	}
	var builder strings.Builder
	for _, r := range label {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
		case r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
		if builder.Len() >= editLockLabelLimit {
			break
		}
	}
	if builder.Len() == 0 {
		return "file"
	}
	return builder.String()
}

func buildEditLockFilePath(filePath string) (string, error) {
	lockDir := filepath.Join(os.TempDir(), "auramate-locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create lock directory: %w", err)
	}

	normalizedPath := filepath.Clean(filePath)
	if os.PathSeparator == '\\' {
		normalizedPath = strings.ToLower(normalizedPath)
	}

	digest := sha256.Sum256([]byte(normalizedPath))
	label := sanitizeEditLockLabel(filepath.Base(normalizedPath))
	lockName := fmt.Sprintf("%s-%s.edit.lock", label, hex.EncodeToString(digest[:editLockNameBytes]))
	return filepath.Join(lockDir, lockName), nil
}

func BuildEditFileLockPath(filePath string) (string, error) {
	return buildEditLockFilePath(filePath)
}

type editFileLockHandle struct {
	filePath string
	lockFile string
	fileMu   *sync.Mutex
	fileLock *flock.Flock
}

func acquireEditFileLock(ctx context.Context, filePath string) (*editFileLockHandle, error) {
	fileMu := getEditFileMutex(filePath)
	fileMu.Lock()

	lockFile, err := buildEditLockFilePath(filePath)
	if err != nil {
		fileMu.Unlock()
		return nil, newEditUserError("无法初始化文件锁: %v", err)
	}

	fileLock := flock.New(lockFile)
	lockStart := time.Now()
	locked, err := acquireLockWithRetry(ctx, fileLock, editLockRetryCount, editLockRetryDelay)
	if err != nil {
		fileMu.Unlock()
		return nil, newEditUserError("无法获取文件锁 (failed to acquire lock): %v", err)
	}
	if !locked {
		fileMu.Unlock()
		return nil, newEditUserError("无法获取文件锁 (resource busy after waiting %s), please try again sequentially", time.Duration(editLockRetryCount)*editLockRetryDelay)
	}

	logging.Debug("Edit tool acquired file lock", "file", filePath, "wait_ms", time.Since(lockStart).Milliseconds(), "lock_file", lockFile)

	return &editFileLockHandle{
		filePath: filePath,
		lockFile: lockFile,
		fileMu:   fileMu,
		fileLock: fileLock,
	}, nil
}

func (h *editFileLockHandle) unlock() {
	if h == nil {
		return
	}
	if h.fileLock != nil {
		if unlockErr := h.fileLock.Unlock(); unlockErr != nil {
			logging.Warn("Edit tool failed to unlock file lock", "file", h.filePath, "lock_file", h.lockFile, "error", unlockErr)
		}
	}
	if h.fileMu != nil {
		h.fileMu.Unlock()
	}
}

func normalizeEditLockPaths(filePaths []string) []string {
	uniquePaths := make(map[string]struct{}, len(filePaths))
	paths := make([]string, 0, len(filePaths))
	for _, filePath := range filePaths {
		if filePath == "" {
			continue
		}
		cleanPath := filepath.Clean(filePath)
		if _, exists := uniquePaths[cleanPath]; exists {
			continue
		}
		uniquePaths[cleanPath] = struct{}{}
		paths = append(paths, cleanPath)
	}
	sort.Strings(paths)
	return paths
}

func runWithEditFileLocks(ctx context.Context, filePaths []string, fn func() error) error {
	normalizedPaths := normalizeEditLockPaths(filePaths)
	locks := make([]*editFileLockHandle, 0, len(normalizedPaths))
	for _, filePath := range normalizedPaths {
		lockHandle, err := acquireEditFileLock(ctx, filePath)
		if err != nil {
			for i := len(locks) - 1; i >= 0; i-- {
				locks[i].unlock()
			}
			return err
		}
		locks = append(locks, lockHandle)
	}
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].unlock()
		}
	}()

	return fn()
}

func runWithEditFileLock(ctx context.Context, filePath string, fn func() error) error {
	return runWithEditFileLocks(ctx, []string{filePath}, fn)
}

func RunWithEditFileLock(ctx context.Context, filePath string, fn func() error) error {
	return runWithEditFileLock(ctx, filePath, fn)
}

func (e *editTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params EditParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse("invalid parameters"), nil
	}

	// Handle parameter aliases for robustness
	if params.OldString == "" && params.OldContent != "" {
		params.OldString = params.OldContent
	}
	if params.NewString == "" && params.NewContent != "" {
		params.NewString = params.NewContent
	}

	if params.FilePath == "" {
		return NewTextErrorResponse("file_path is required"), nil
	}

	// Use ResolvePath which handles relative paths, overlapping prefixes, and project root fallback
	params.FilePath = ResolvePathFor(ctx, params.FilePath)

	var response ToolResponse
	var err error

	// FIX: Three operations are mutually exclusive.
	// old_string="" means create new file (only if file doesn't exist).
	// new_string="" means delete old_string from file.
	// Otherwise, replace old_string with new_string.
	if params.OldString == "" {
		// Check if file already exists - if so, this is a full content replacement, not creation
		if _, statErr := os.Stat(params.FilePath); statErr == nil {
			// File exists: treat as full content replacement (write entire file)
			response, err = e.replaceFullContent(ctx, params.FilePath, params.NewString)
			if err != nil {
				return response, err
			}
			if response.IsError {
				return response, nil
			}
			return finalizeEditResponse(ctx, response, params.FilePath, e.lspRegistry)
		}
		// File doesn't exist: create new file
		response, err = e.createNewFile(ctx, params.FilePath, params.NewString)
		if err != nil {
			return response, err
		}
		if response.IsError {
			return response, nil
		}
		return finalizeEditResponse(ctx, response, params.FilePath, e.lspRegistry)
	}

	if params.NewString == "" {
		response, err = e.deleteContent(ctx, params.FilePath, params.OldString)
		if err != nil {
			return response, err
		}
		if response.IsError {
			return response, nil
		}
		return finalizeEditResponse(ctx, response, params.FilePath, e.lspRegistry)
	}

	response, err = e.replaceContent(ctx, params.FilePath, params.OldString, params.NewString)
	if err != nil {
		return response, err
	}
	if response.IsError {
		return response, nil
	}

	return finalizeEditResponse(ctx, response, params.FilePath, e.lspRegistry)
}

// normalizeContent removes all whitespace for fuzzy matching
func normalizeContent(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func mapNormalizedIndices(content string) (string, []int) {
	var b strings.Builder
	var indices []int

	for i, r := range content {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
			runeBytes := len(string(r))
			for k := 0; k < runeBytes; k++ {
				indices = append(indices, i+k)
			}
		}
	}
	return b.String(), indices
}

func findBestMatch(ctx context.Context, s, target string) (string, int) {
	if len(target) > len(s) {
		return "", -1
	}
	bestDist := len(target)
	bestSub := ""

	targetLen := len(target)
	for i := 0; i <= len(s)-targetLen; i++ {
		// Check for cancellation in heavy loops
		if i%100 == 0 {
			if err := CheckCancellation(ctx); err != nil {
				return "", -1
			}
		}

		sub := s[i : i+targetLen]
		dist := levenshtein(ctx, sub, target)
		if dist == -1 {
			return "", -1
		}
		if dist < bestDist {
			bestDist = dist
			bestSub = sub
		}
	}
	return bestSub, bestDist
}

func levenshtein(ctx context.Context, a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	column := make([]int, len(br)+1)
	for y := 1; y <= len(br); y++ {
		column[y] = y
	}
	for x := 1; x <= len(ar); x++ {
		// Check for cancellation in heavy loops
		if x%100 == 0 {
			if err := CheckCancellation(ctx); err != nil {
				return -1
			}
		}

		column[0] = x
		lastdiag := x - 1
		for y := 1; y <= len(br); y++ {
			olddiag := column[y]
			cost := 0
			if ar[x-1] != br[y-1] {
				cost = 1
			}
			column[y] = min(column[y]+1, column[y-1]+1, lastdiag+cost)
			lastdiag = olddiag
		}
	}
	return column[len(br)]
}

func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func (e *editTool) createNewFile(ctx context.Context, filePath, content string) (ToolResponse, error) {
	fileInfo, err := os.Stat(filePath)
	if err == nil {
		if fileInfo.IsDir() {
			return NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", filePath)), nil
		}
		return NewTextErrorResponse(fmt.Sprintf("file already exists: %s", filePath)), nil
	} else if !os.IsNotExist(err) {
		return ToolResponse{}, fmt.Errorf("failed to access file: %w", err)
	}

	dir := filepath.Dir(filePath)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return ToolResponse{}, fmt.Errorf("failed to create parent directories: %w", err)
	}

	sessionID, messageID := GetContextValues(ctx)
	if sessionID == "" || messageID == "" {
		return ToolResponse{}, fmt.Errorf("session ID and message ID are required for creating a new file")
	}

	diff, additions, removals := diff.GenerateDiff(
		"",
		content,
		filePath,
	)
	rootDir := config.WorkingDirectoryFor(ctx)
	permissionPath := filepath.Dir(filePath)
	if strings.HasPrefix(filePath, rootDir) {
		permissionPath = rootDir
	}
	p := e.permissions.Request(
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        permissionPath,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Create file %s", filePath),
			Params: EditPermissionsParams{
				FilePath: filePath,
				Diff:     diff,
			},
		},
	)
	if !p {
		return ToolResponse{}, permission.ErrorPermissionDenied
	}

	err = writeFileWithRetry(ctx, filePath, []byte(content), 0o644)
	err = runWithEditFileLock(ctx, filePath, func() error {
		fileInfo, err := os.Stat(filePath)
		if err == nil {
			if fileInfo.IsDir() {
				return newEditUserError("path is a directory, not a file: %s", filePath)
			}
			return newEditUserError("file already exists: %s", filePath)
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to access file: %w", err)
		}

		dir := filepath.Dir(filePath)
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create parent directories: %w", err)
		}

		return writeFileWithRetry(ctx, filePath, []byte(content), 0o644)
	})
	if err != nil {
		return handleEditExecutionError(err)
	}

	// File can't be in the history so we create a new file history
	_, err = e.files.Create(ctx, sessionID, filePath, "")
	if err != nil {
		// Log error but don't fail the operation
		return ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
	}

	// Add the new content to the file history
	_, err = e.files.CreateVersion(ctx, sessionID, filePath, content)
	if err != nil {
		// Log error but don't fail the operation
		logging.Debug("Error creating file history version", "error", err)
	}

	recordFileWrite(filePath)
	recordFileRead(filePath)

	result := fmt.Sprintf("File created: %s", filePath)

	return WithResponseMetadata(
		NewTextResponse(result),
		EditResponseMetadata{
			Diff:      diff,
			Additions: additions,
			Removals:  removals,
		},
	), nil
}

// replaceFullContent replaces the entire file content with newContent.
// Used when old_string="" and the file already exists (full content replacement).
func (e *editTool) replaceFullContent(ctx context.Context, filePath, newContent string) (ToolResponse, error) {
	oldContent, err := os.ReadFile(filePath)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("failed to read file: %w", err)
	}

	oldStr := string(oldContent)
	if oldStr == newContent {
		return NewTextErrorResponse("new content is the same as old content. No changes made."), nil
	}

	sessionID, messageID := GetContextValues(ctx)
	if sessionID == "" || messageID == "" {
		return ToolResponse{}, fmt.Errorf("session ID and message ID are required for editing")
	}

	diffResult, additions, removals := diff.GenerateDiff(oldStr, newContent, filePath)

	rootDir := config.WorkingDirectoryFor(ctx)
	permissionPath := filepath.Dir(filePath)
	if strings.HasPrefix(filePath, rootDir) {
		permissionPath = rootDir
	}
	p := e.permissions.Request(
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        permissionPath,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Replace full content of file %s", filePath),
			Params: EditPermissionsParams{
				FilePath: filePath,
				Diff:     diffResult,
			},
		},
	)
	if !p {
		return ToolResponse{}, permission.ErrorPermissionDenied
	}

	err = runWithEditFileLock(ctx, filePath, func() error {
		currentContent, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to re-read file before write: %w", err)
		}

		currentStr := string(currentContent)
		if currentStr == newContent {
			return newEditUserError("new content is already present in the file. No changes made.")
		}
		if currentStr != oldStr {
			return newEditUserError("file changed after it was read. Read the file again before editing.")
		}

		return writeFileWithRetry(ctx, filePath, []byte(newContent), 0o644)
	})
	if err != nil {
		return handleEditExecutionError(err)
	}

	// Update file history
	file, err := e.files.GetByPathAndSession(ctx, filePath, sessionID)
	if err != nil {
		_, err = e.files.Create(ctx, sessionID, filePath, oldStr)
		if err != nil {
			return ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
		}
	}
	if file.Content != oldStr {
		_, err = e.files.CreateVersion(ctx, sessionID, filePath, oldStr)
		if err != nil {
			logging.Debug("Error creating file history version", "error", err)
		}
	}
	_, err = e.files.CreateVersion(ctx, sessionID, filePath, newContent)
	if err != nil {
		logging.Debug("Error creating file history version", "error", err)
	}

	recordFileWrite(filePath)
	recordFileRead(filePath)

	result := fmt.Sprintf("Content replaced in file: %s", filePath)

	return WithResponseMetadata(
		NewTextResponse(result),
		EditResponseMetadata{
			Diff:      diffResult,
			Additions: additions,
			Removals:  removals,
		},
	), nil
}

func (e *editTool) deleteContent(ctx context.Context, filePath, oldString string) (ToolResponse, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewTextErrorResponse(fmt.Sprintf("file not found: %s", filePath)), nil
		}
		return ToolResponse{}, fmt.Errorf("failed to access file: %w", err)
	}

	if fileInfo.IsDir() {
		return NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", filePath)), nil
	}

	if getLastReadTime(filePath).IsZero() {
		return NewTextErrorResponse("you must read the file before editing it. Use the View tool first"), nil
	}

	modTime := fileInfo.ModTime()
	lastRead := getLastReadTime(filePath)

	if !lastRead.IsZero() && modTime.After(lastRead) {
		logging.Warn("edit tool concurrent modification warning (delete)", "file", filePath, "mod_time", modTime, "last_read", lastRead)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("failed to read file: %w", err)
	}

	oldContent := string(content)

	index := strings.Index(oldContent, oldString)
	if index == -1 {
		// Try fuzzy matching
		normalizedOld := normalizeContent(oldString)
		normalizedContent := normalizeContent(oldContent)

		// If normalized content doesn't match, it's definitely not there
		if !strings.Contains(normalizedContent, normalizedOld) {
			return NewTextErrorResponse("old_string not found in file (even with fuzzy matching). Please check your indentation and content."), nil
		}

		// It matches fuzzily, but we need to find WHERE in the original string.
		// This is hard to do perfectly without a complex diff algorithm.
		// For now, let's just return a helpful error.
		return NewTextErrorResponse("old_string not found exactly, but a similar block exists. Please verify whitespace/indentation and try again with the EXACT content from the file."), nil
	}

	lastIndex := strings.LastIndex(oldContent, oldString)
	if index != lastIndex {
		return NewTextErrorResponse("old_string appears multiple times in the file. Please provide more context to ensure a unique match"), nil
	}

	newContent := oldContent[:index] + oldContent[index+len(oldString):]

	sessionID, messageID := GetContextValues(ctx)

	if sessionID == "" || messageID == "" {
		return ToolResponse{}, fmt.Errorf("session ID and message ID are required for creating a new file")
	}

	diff, additions, removals := diff.GenerateDiff(
		oldContent,
		newContent,
		filePath,
	)

	rootDir := config.WorkingDirectoryFor(ctx)
	permissionPath := filepath.Dir(filePath)
	if strings.HasPrefix(filePath, rootDir) {
		permissionPath = rootDir
	}
	p := e.permissions.Request(
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        permissionPath,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Delete content from file %s", filePath),
			Params: EditPermissionsParams{
				FilePath: filePath,
				Diff:     diff,
			},
		},
	)
	if !p {
		return ToolResponse{}, permission.ErrorPermissionDenied
	}

	err = runWithEditFileLock(ctx, filePath, func() error {
		currentContent, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to re-read file before write: %w", err)
		}
		if string(currentContent) != oldContent {
			return newEditUserError("file changed after it was read. Read the file again before editing.")
		}
		return writeFileWithRetry(ctx, filePath, []byte(newContent), 0o644)
	})
	if err != nil {
		return handleEditExecutionError(err)
	}

	// Check if file exists in history
	file, err := e.files.GetByPathAndSession(ctx, filePath, sessionID)
	if err != nil {
		_, err = e.files.Create(ctx, sessionID, filePath, oldContent)
		if err != nil {
			// Log error but don't fail the operation
			return ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
		}
	}
	if file.Content != oldContent {
		// User Manually changed the content store an intermediate version
		_, err = e.files.CreateVersion(ctx, sessionID, filePath, oldContent)
		if err != nil {
			logging.Debug("Error creating file history version", "error", err)
		}
	}
	// Store the new version
	_, err = e.files.CreateVersion(ctx, sessionID, filePath, "")
	if err != nil {
		logging.Debug("Error creating file history version", "error", err)
	}

	recordFileWrite(filePath)
	recordFileRead(filePath)

	result := fmt.Sprintf("Content deleted from file: %s", filePath)

	return WithResponseMetadata(
		NewTextResponse(result),
		EditResponseMetadata{
			Diff:      diff,
			Additions: additions,
			Removals:  removals,
		},
	), nil
}

func writeFileWithRetry(ctx context.Context, filename string, data []byte, perm os.FileMode) error {
	var err error

	// Exponential backoff for file locking
	backoff := 100 * time.Millisecond
	maxRetries := 5

	for i := 0; i < maxRetries; i++ {
		if err := CheckCancellation(ctx); err != nil {
			return err
		}

		err = os.WriteFile(filename, data, perm)
		if err == nil {
			if verifyErr := verifyWriteResult(ctx, filename, data); verifyErr != nil {
				return verifyErr
			}
			return nil
		}

		// 1. Check for Permission Errors immediately - don't retry, just fallback
		if os.IsPermission(err) || strings.Contains(err.Error(), "Access is denied") {
			logging.Warn("Write permission denied, attempting fallback...", "file", filename)
			fallbackPath, fallbackErr := writeToFallbackLocation(ctx, filename, data, perm)
			if fallbackErr == nil {
				// We return a special error that the caller can detect if they want,
				// or we just log it. But for the tool output to be useful,
				// we should probably consider this a "partial success".
				// Since this function signature returns error, we wrap it.
				return fmt.Errorf("permission denied for original path, but saved copy to: %s", fallbackPath)
			}
			return fmt.Errorf("permission denied and fallback failed: %w", fallbackErr)
		}

		// 2. Check for Locking Errors (Windows specific mainly)
		errMsg := err.Error()
		if strings.Contains(errMsg, "process cannot access") || strings.Contains(errMsg, "used by another process") {
			logging.Debug("File locked, retrying...", "file", filename, "attempt", i+1, "backoff", backoff)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}

			backoff *= 2 // Exponential backoff
			continue
		}

		// If it's another error (e.g. disk full), return immediately
		return err
	}
	return fmt.Errorf("failed to write file after %d attempts: %w. The file is locked or unwritable. DO NOT retry using 'write' or 'edit'. Tell the user.", maxRetries, err)
}

func verifyWriteResult(ctx context.Context, filename string, expected []byte) error {
	verifyRead := func() ([]byte, error) {
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		return data, nil
	}

	firstRead, err := verifyRead()
	if err != nil {
		return fmt.Errorf("write verification failed: cannot read back file: %w", err)
	}

	if string(firstRead) != string(expected) {
		return fmt.Errorf("WRITE_VERIFY_FAILED: content mismatch right after write; file may be locked or modified by another process. DO NOT keep retrying blindly.")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond):
	}

	secondRead, err := verifyRead()
	if err != nil {
		return fmt.Errorf("write stability verification failed: cannot read file after delay: %w", err)
	}

	if string(secondRead) != string(expected) {
		return fmt.Errorf("WRITE_VERIFY_FAILED: content changed shortly after write; file may be occupied or overwritten by another process. DO NOT retry blindly.")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(400 * time.Millisecond):
	}

	thirdRead, err := verifyRead()
	if err != nil {
		return fmt.Errorf("write stability verification failed: cannot read file after extended delay: %w", err)
	}

	if string(thirdRead) != string(expected) {
		return fmt.Errorf("WRITE_VERIFY_FAILED: content changed after extended delay; external process likely overwrote file. DO NOT retry blindly.")
	}

	return nil
}

// writeToFallbackLocation attempts to write to a safe temporary location
func writeToFallbackLocation(ctx context.Context, originalPath string, data []byte, perm os.FileMode) (string, error) {
	rootDir := config.WorkingDirectoryFor(ctx)
	fallbackDir := filepath.Join(rootDir, "workspace", "tmp")

	if err := os.MkdirAll(fallbackDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create fallback directory: %w", err)
	}

	baseName := filepath.Base(originalPath)
	timestamp := time.Now().Format("20060102_150405")
	fallbackPath := filepath.Join(fallbackDir, fmt.Sprintf("%s_%s", timestamp, baseName))

	if err := os.WriteFile(fallbackPath, data, perm); err != nil {
		return "", err
	}
	return fallbackPath, nil
}

func (e *editTool) replaceContent(ctx context.Context, filePath, oldString, newString string) (ToolResponse, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewTextErrorResponse(fmt.Sprintf("file not found: %s", filePath)), nil
		}
		return ToolResponse{}, fmt.Errorf("failed to access file: %w", err)
	}

	if fileInfo.IsDir() {
		return NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", filePath)), nil
	}

	if getLastReadTime(filePath).IsZero() {
		return NewTextErrorResponse("you must read the file before editing it. Use the View tool first"), nil
	}

	modTime := fileInfo.ModTime()
	lastRead := getLastReadTime(filePath)

	if !lastRead.IsZero() && modTime.After(lastRead) {
		logging.Warn("edit tool concurrent modification warning", "file", filePath, "mod_time", modTime, "last_read", lastRead)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("failed to read file: %w", err)
	}

	oldContent := string(content)

	index := strings.Index(oldContent, oldString)
	var newContent string

	if index == -1 {
		// Try fuzzy matching
		normalizedOld := normalizeContent(oldString)
		normContent, mapping := mapNormalizedIndices(oldContent)

		// If normalized content doesn't match, it's definitely not there
		if !strings.Contains(normContent, normalizedOld) {
			// Try to find a similar block for intelligent error feedback
			bestMatch, score := findBestMatch(ctx, normContent, normalizedOld)
			if bestMatch != "" && score < len(normalizedOld)/2 { // Threshold: < 50% difference
				// Map normalized best match back to original content to show "Actual"
				// We need to find where bestMatch (normalized) comes from in oldContent.
				idx := strings.Index(normContent, bestMatch)
				if idx != -1 {
					start := mapping[idx]
					endNorm := idx + len(bestMatch) - 1
					if endNorm < len(mapping) {
						end := mapping[endNorm] + 1
						actualBlock := oldContent[start:end]

						return NewTextErrorResponse(fmt.Sprintf(
							"old_string not found. Did you mean this block?\n<<<< Expected\n%s\n==== Actual (Found in file)\n%s\n>>>>\n(Similarity match. Please use the Exact Actual content in your request. Also ensure you read the file again if it was modified recently.)",
							oldString, actualBlock)), nil
					}
				}
			}

			return NewTextErrorResponse("old_string not found in file (even with fuzzy matching). Please check your indentation and content."), nil
		}

		// It matches fuzzily, let's find the location
		idx := strings.Index(normContent, normalizedOld)
		lastIdx := strings.LastIndex(normContent, normalizedOld)

		if idx != lastIdx {
			return NewTextErrorResponse(fmt.Sprintf("old_string appears multiple times in the file (fuzzy match). idx=%d, lastIdx=%d. NormFile: %s, NormOld: %s", idx, lastIdx, normContent, normalizedOld)), nil
		}

		// Calculate exact range in original content
		start := mapping[idx]
		endNorm := idx + len(normalizedOld) - 1
		end := mapping[endNorm] + 1

		// Replace content
		// Be careful with bounds
		if start < 0 || end > len(oldContent) {
			return NewTextErrorResponse("Fuzzy match index calculation error"), nil
		}

		newContent = oldContent[:start] + newString + oldContent[end:]
	} else {
		lastIndex := strings.LastIndex(oldContent, oldString)
		if index != lastIndex {
			return NewTextErrorResponse("old_string appears multiple times in the file. Please provide more context to ensure a unique match"), nil
		}

		newContent = oldContent[:index] + newString + oldContent[index+len(oldString):]
	}
	if oldContent == newContent {
		return NewTextErrorResponse("new content is the same as old content. No changes made."), nil
	}
	sessionID, messageID := GetContextValues(ctx)

	if sessionID == "" || messageID == "" {
		return ToolResponse{}, fmt.Errorf("session ID and message ID are required for creating a new file")
	}
	diff, additions, removals := diff.GenerateDiff(
		oldContent,
		newContent,
		filePath,
	)
	rootDir := config.WorkingDirectoryFor(ctx)
	permissionPath := filepath.Dir(filePath)
	if strings.HasPrefix(filePath, rootDir) {
		permissionPath = rootDir
	}
	p := e.permissions.Request(
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        permissionPath,
			ToolName:    EditToolName,
			Action:      "write",
			Description: fmt.Sprintf("Replace content in file %s", filePath),
			Params: EditPermissionsParams{
				FilePath: filePath,
				Diff:     diff,
			},
		},
	)
	if !p {
		return ToolResponse{}, permission.ErrorPermissionDenied
	}

	err = runWithEditFileLock(ctx, filePath, func() error {
		currentContent, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to re-read file before write: %w", err)
		}
		currentStr := string(currentContent)
		if currentStr != oldContent {
			return newEditUserError("file changed after it was read. Read the file again before editing.")
		}
		return writeFileWithRetry(ctx, filePath, []byte(newContent), 0o644)
	})
	if err != nil {
		return handleEditExecutionError(err)
	}

	// Check if file exists in history
	file, err := e.files.GetByPathAndSession(ctx, filePath, sessionID)
	if err != nil {
		_, err = e.files.Create(ctx, sessionID, filePath, oldContent)
		if err != nil {
			// Log error but don't fail the operation
			return ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
		}
	}
	if file.Content != oldContent {
		// User Manually changed the content store an intermediate version
		_, err = e.files.CreateVersion(ctx, sessionID, filePath, oldContent)
		if err != nil {
			logging.Debug("Error creating file history version", "error", err)
		}
	}
	// Store the new version
	_, err = e.files.CreateVersion(ctx, sessionID, filePath, newContent)
	if err != nil {
		logging.Debug("Error creating file history version", "error", err)
	}

	recordFileWrite(filePath)
	recordFileRead(filePath)

	result := fmt.Sprintf("Content replaced in file: %s", filePath)

	return WithResponseMetadata(
		NewTextResponse(result),
		EditResponseMetadata{
			Diff:      diff,
			Additions: additions,
			Removals:  removals,
		}), nil
}

// finalizeEditResponse applies LSP diagnostics and formats the edit response.
// Extracted to avoid duplicating the LSP wait + diagnostics logic across create/delete/replace paths.
func finalizeEditResponse(ctx context.Context, response ToolResponse, filePath string, lspRegistry *lsp.Registry) (ToolResponse, error) {
	waitForLspDiagnostics(ctx, filePath, lspRegistry)
	text := fmt.Sprintf("<result>\n%s\n</result>\n", response.Content)
	text += getDiagnostics(filePath, lspRegistry)
	text += lspStatusNoteForFile(filePath, lspRegistry)
	text += getPythonSyntaxDiagnostics(ctx, filePath)
	response.Content = text
	return response, nil
}

func getPythonSyntaxDiagnostics(ctx context.Context, filePath string) string {
	if strings.ToLower(filepath.Ext(filePath)) != ".py" {
		return ""
	}
	if _, err := os.Stat(filePath); err != nil {
		return ""
	}
	pythonPath, err := exec.LookPath("python")
	if err != nil {
		return ""
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, pythonPath, "-m", "py_compile", filePath)
	output, err := cmd.CombinedOutput()
	if checkCtx.Err() == context.DeadlineExceeded {
		return "\n<syntax_diagnostics>\npython py_compile timed out\n</syntax_diagnostics>\n"
	}
	if err != nil {
		return fmt.Sprintf("\n<syntax_diagnostics>\npython py_compile failed:\n%s\n</syntax_diagnostics>\n", strings.TrimSpace(string(output)))
	}
	return ""
}
