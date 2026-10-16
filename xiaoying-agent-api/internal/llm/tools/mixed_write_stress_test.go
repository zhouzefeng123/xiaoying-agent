package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/history"
	"github.com/auramate/auramate/internal/permission"
	"github.com/auramate/auramate/internal/pubsub"
	"github.com/google/uuid"
)

type allowAllPermissionService struct{}

func (s *allowAllPermissionService) Subscribe(context.Context) <-chan pubsub.Event[permission.PermissionRequest] {
	ch := make(chan pubsub.Event[permission.PermissionRequest])
	close(ch)
	return ch
}

func (s *allowAllPermissionService) GrantPersistant(permission.PermissionRequest) {}
func (s *allowAllPermissionService) Grant(permission.PermissionRequest)           {}
func (s *allowAllPermissionService) Deny(permission.PermissionRequest)            {}
func (s *allowAllPermissionService) Request(permission.CreatePermissionRequest) bool {
	return true
}
func (s *allowAllPermissionService) AutoApproveSession(string) {}

type memoryHistoryService struct {
	mu      sync.Mutex
	files   map[string]history.File
	version int
}

func newMemoryHistoryService() *memoryHistoryService {
	return &memoryHistoryService{files: make(map[string]history.File)}
}

func (s *memoryHistoryService) Subscribe(context.Context) <-chan pubsub.Event[history.File] {
	ch := make(chan pubsub.Event[history.File])
	close(ch)
	return ch
}

func (s *memoryHistoryService) nextVersion() string {
	s.version++
	if s.version == 1 {
		return history.InitialVersion
	}
	return fmt.Sprintf("v%d", s.version-1)
}

func (s *memoryHistoryService) key(sessionID, path string) string {
	return sessionID + "|" + path
}

func (s *memoryHistoryService) Create(ctx context.Context, sessionID, path, content string) (history.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file := history.File{
		ID:        uuid.NewString(),
		SessionID: sessionID,
		Path:      path,
		Content:   content,
		Version:   s.nextVersion(),
		CreatedAt: time.Now().UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}
	s.files[s.key(sessionID, path)] = file
	return file, nil
}

func (s *memoryHistoryService) CreateVersion(ctx context.Context, sessionID, path, content string) (history.File, error) {
	return s.Create(ctx, sessionID, path, content)
}

func (s *memoryHistoryService) Get(ctx context.Context, id string) (history.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range s.files {
		if file.ID == id {
			return file, nil
		}
	}
	return history.File{}, sql.ErrNoRows
}

func (s *memoryHistoryService) GetByPathAndSession(ctx context.Context, path, sessionID string) (history.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, ok := s.files[s.key(sessionID, path)]
	if !ok {
		return history.File{}, sql.ErrNoRows
	}
	return file, nil
}

func (s *memoryHistoryService) ListBySession(context.Context, string) ([]history.File, error) {
	return nil, nil
}

func (s *memoryHistoryService) ListLatestSessionFiles(context.Context, string) ([]history.File, error) {
	return nil, nil
}

func (s *memoryHistoryService) ListAllLatestFiles(context.Context) ([]history.File, error) {
	return nil, nil
}

func (s *memoryHistoryService) ListAllBaselineFiles(context.Context) ([]history.File, error) {
	return nil, nil
}

func (s *memoryHistoryService) ListVersionsByPath(context.Context, string) ([]history.File, error) {
	return nil, nil
}

func (s *memoryHistoryService) GetByPathAndVersion(context.Context, string, string) (history.File, error) {
	return history.File{}, sql.ErrNoRows
}

func (s *memoryHistoryService) Update(context.Context, history.File) (history.File, error) {
	return history.File{}, nil
}

func (s *memoryHistoryService) Delete(context.Context, string) error {
	return nil
}

func (s *memoryHistoryService) DeleteSessionFiles(context.Context, string) error {
	return nil
}

type mixedStressResult struct {
	tool     string
	response ToolResponse
	err      error
	elapsed  time.Duration
}

func TestMixedWriteToolsAvoidLockBusyUnderContention(t *testing.T) {
	workDir := config.WorkingDirectory()
	stressRoot := filepath.Join(workDir, "workspace", "tmp")
	if err := os.MkdirAll(stressRoot, 0o755); err != nil {
		t.Fatalf("failed to create stress root: %v", err)
	}

	tempDir, err := os.MkdirTemp(stressRoot, "mixed-write-stress-")
	if err != nil {
		t.Fatalf("failed to create stress temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	targetPath := filepath.Join(tempDir, "shared.txt")
	if err := os.WriteFile(targetPath, []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("failed to seed target file: %v", err)
	}
	relTargetPath, err := filepath.Rel(workDir, targetPath)
	if err != nil {
		t.Fatalf("failed to compute relative target path: %v", err)
	}
	patchTargetPath := filepath.ToSlash(relTargetPath)

	permissions := &allowAllPermissionService{}
	files := newMemoryHistoryService()
	editTool := NewEditTool(nil, permissions, files)
	patchTool := NewPatchTool(nil, permissions, files)
	batchTool := NewBatchWriteTool()

	const workers = 9
	results := make(chan mixedStressResult, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results <- runMixedStressWorker(t, index, targetPath, patchTargetPath, editTool, patchTool, batchTool)
		}(worker)
	}

	close(start)
	wg.Wait()
	close(results)

	lockFailures := 0
	timeoutFailures := 0
	goFailures := 0
	successes := 0
	perToolSuccess := map[string]int{}
	perToolFailures := map[string]int{}

	for result := range results {
		if result.err != nil {
			goFailures++
			if strings.Contains(strings.ToLower(result.err.Error()), "deadline exceeded") {
				timeoutFailures++
			}
			t.Logf("tool=%s go_error=%v elapsed=%s", result.tool, result.err, result.elapsed)
			continue
		}

		contentLower := strings.ToLower(result.response.Content)
		if strings.Contains(contentLower, "resource busy") || strings.Contains(result.response.Content, "无法获取文件锁") || strings.Contains(contentLower, "failed to acquire lock") {
			lockFailures++
		}

		if result.response.IsError {
			perToolFailures[result.tool]++
			t.Logf("tool=%s tool_error=%s elapsed=%s", result.tool, result.response.Content, result.elapsed)
			continue
		}

		successes++
		perToolSuccess[result.tool]++
	}

	if goFailures != 0 {
		t.Fatalf("expected no Go-level failures, got %d", goFailures)
	}
	if timeoutFailures != 0 {
		t.Fatalf("expected no timeout failures, got %d", timeoutFailures)
	}
	if lockFailures != 0 {
		t.Fatalf("expected no lock busy failures, got %d", lockFailures)
	}
	if successes == 0 {
		t.Fatal("expected at least one successful mixed-tool write")
	}

	t.Logf("mixed stress summary: successes=%d per_tool_success=%v per_tool_failures=%v", successes, perToolSuccess, perToolFailures)
}

func TestPatchToolMultiFileMoveSucceeds(t *testing.T) {
	workDir := config.WorkingDirectory()
	tempDir := makeStressTempDir(t, workDir)
	defer os.RemoveAll(tempDir)

	keepPath := filepath.Join(tempDir, "keep.txt")
	movePath := filepath.Join(tempDir, "move.txt")
	movedPath := filepath.Join(tempDir, "moved.txt")

	writeTestFile(t, keepPath, "keep\n")
	writeTestFile(t, movePath, "move\n")

	patchKeepPath := mustRelativeSlashPath(t, workDir, keepPath)
	patchMovePath := mustRelativeSlashPath(t, workDir, movePath)
	patchMovedPath := mustRelativeSlashPath(t, workDir, movedPath)

	recordFileRead(keepPath)
	recordFileRead(movePath)

	permissions := &allowAllPermissionService{}
	files := newMemoryHistoryService()
	patchTool := NewPatchTool(nil, permissions, files)

	ctx := SetContextValues(context.Background(), "patch-move-session", "patch-move-message")
	patchText := buildMultiFilePatchText([]string{
		buildAppendPatchText(patchKeepPath, "keep\n", "keep-updated"),
		buildMovePatchText(patchMovePath, patchMovedPath),
	})

	response, err := patchTool.Run(ctx, ToolCall{
		ID:    "patch-multi-move",
		Name:  PatchToolName,
		Input: mustJSON(t, PatchParams{PatchText: patchText}),
	})
	if err != nil {
		t.Fatalf("patch tool returned Go error: %v", err)
	}
	if response.IsError {
		t.Fatalf("patch tool returned tool error: %s", response.Content)
	}

	assertFileContent(t, keepPath, "keep\nkeep-updated\n")
	assertPathNotExists(t, movePath)
	assertFileContent(t, movedPath, "move\n")
	if !strings.Contains(response.Content, "Patch applied successfully") {
		t.Fatalf("unexpected patch success response: %s", response.Content)
	}
}

func TestBatchWriteRollbackRestoresCreatedAndModifiedFiles(t *testing.T) {
	workDir := config.WorkingDirectory()
	tempDir := makeStressTempDir(t, workDir)
	defer os.RemoveAll(tempDir)

	createdPath := filepath.Join(tempDir, "a-created.txt")
	modifiedPath := filepath.Join(tempDir, "b-modified.txt")
	failingPath := filepath.Join(tempDir, "c-failing.txt")

	writeTestFile(t, modifiedPath, "original-modified\n")
	writeTestFile(t, failingPath, "original-failing\n")

	originalWriter := batchWriteFileWriter
	originalRemover := batchWriteFileRemover
	defer func() {
		batchWriteFileWriter = originalWriter
		batchWriteFileRemover = originalRemover
	}()

	var writeOrder []string
	forcedFailureUsed := false
	batchWriteFileWriter = func(ctx context.Context, filename string, data []byte, perm os.FileMode) error {
		writeOrder = append(writeOrder, filepath.Base(filename))
		if filepath.Clean(filename) == filepath.Clean(failingPath) && !forcedFailureUsed {
			forcedFailureUsed = true
			return errors.New("forced write failure for rollback test")
		}
		return writeFileWithRetry(ctx, filename, data, perm)
	}
	batchWriteFileRemover = func(path string) error {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	batchTool := NewBatchWriteTool()
	response, err := batchTool.Run(context.Background(), ToolCall{
		ID:   "batch-rollback",
		Name: BatchWriteToolName,
		Input: mustJSON(t, BatchWriteParams{Files: map[string]string{
			createdPath:  "created-content\n",
			modifiedPath: "modified-content\n",
			failingPath:  "failing-content\n",
		}}),
	})
	if err != nil {
		t.Fatalf("batch_write returned Go error: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected batch_write tool error, got success: %s", response.Content)
	}
	if !strings.Contains(response.Content, "failed to write batch") {
		t.Fatalf("unexpected batch_write error response: %s", response.Content)
	}

	assertPathNotExists(t, createdPath)
	assertFileContent(t, modifiedPath, "original-modified\n")
	assertFileContent(t, failingPath, "original-failing\n")
	if !slices.Equal(writeOrder, []string{"a-created.txt", "b-modified.txt", "c-failing.txt", "b-modified.txt", "c-failing.txt"}) {
		t.Fatalf("unexpected write order / rollback sequence: %v", writeOrder)
	}
}

func runMixedStressWorker(t *testing.T, worker int, targetPath, patchTargetPath string, editTool BaseTool, patchTool BaseTool, batchTool BaseTool) mixedStressResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = SetContextValues(ctx, fmt.Sprintf("stress-session-%d", worker%3), fmt.Sprintf("stress-message-%d", worker))

	toolKind := []string{EditToolName, PatchToolName, BatchWriteToolName}[worker%3]
	start := time.Now()

	snapshot, err := os.ReadFile(targetPath)
	if err != nil {
		return mixedStressResult{tool: toolKind, err: err}
	}

	var call ToolCall
	switch toolKind {
	case EditToolName:
		recordFileRead(targetPath)
		call = ToolCall{
			ID:   fmt.Sprintf("edit-%d", worker),
			Name: EditToolName,
			Input: mustJSON(t, EditParams{
				FilePath:  targetPath,
				OldString: "",
				NewString: string(snapshot) + fmt.Sprintf("edit-worker-%d\n", worker),
			}),
		}
	case PatchToolName:
		recordFileRead(targetPath)
		patchLine := fmt.Sprintf("patch-worker-%d", worker)
		call = ToolCall{
			ID:    fmt.Sprintf("patch-%d", worker),
			Name:  PatchToolName,
			Input: mustJSON(t, PatchParams{PatchText: buildApplyPatchText(patchTargetPath, string(snapshot), patchLine)}),
		}
	default:
		call = ToolCall{
			ID:   fmt.Sprintf("batch-%d", worker),
			Name: BatchWriteToolName,
			Input: mustJSON(t, BatchWriteParams{Files: map[string]string{
				targetPath: string(snapshot) + fmt.Sprintf("batch-worker-%d\n", worker),
			}}),
		}
	}

	var response ToolResponse
	switch toolKind {
	case EditToolName:
		response, err = editTool.Run(ctx, call)
	case PatchToolName:
		response, err = patchTool.Run(ctx, call)
	default:
		response, err = batchTool.Run(ctx, call)
	}

	return mixedStressResult{
		tool:     toolKind,
		response: response,
		err:      err,
		elapsed:  time.Since(start),
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal test payload: %v", err)
	}
	return string(data)
}

func makeStressTempDir(t *testing.T, workDir string) string {
	t.Helper()
	stressRoot := filepath.Join(workDir, "workspace", "tmp")
	if err := os.MkdirAll(stressRoot, 0o755); err != nil {
		t.Fatalf("failed to create stress root: %v", err)
	}
	tempDir, err := os.MkdirTemp(stressRoot, "mixed-write-stress-")
	if err != nil {
		t.Fatalf("failed to create stress temp dir: %v", err)
	}
	return tempDir
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create parent directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file %s: %v", path, err)
	}
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file %s: %v", path, err)
	}
	if string(content) != expected {
		t.Fatalf("unexpected file content for %s:\nexpected: %q\nactual:   %q", path, expected, string(content))
	}
}

func assertPathNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected path to be removed: %s", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("failed to stat %s: %v", path, err)
	}
}

func mustRelativeSlashPath(t *testing.T, workDir, path string) string {
	t.Helper()
	relPath, err := filepath.Rel(workDir, path)
	if err != nil {
		t.Fatalf("failed to compute relative path for %s: %v", path, err)
	}
	return filepath.ToSlash(relPath)
}

func buildMultiFilePatchText(actions []string) string {
	var builder strings.Builder
	builder.WriteString("*** Begin Patch\n")
	for _, action := range actions {
		builder.WriteString(strings.TrimSuffix(action, "\n"))
		builder.WriteByte('\n')
	}
	builder.WriteString("*** End Patch")
	return builder.String()
}

func buildAppendPatchText(path, before, appendedLine string) string {
	var builder strings.Builder
	builder.WriteString("*** Update File: ")
	builder.WriteString(path)
	builder.WriteString("\n@@\n")
	beforeLines := splitPatchLines(before)
	for _, line := range beforeLines {
		builder.WriteByte(' ')
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	builder.WriteByte('+')
	builder.WriteString(appendedLine)
	builder.WriteByte('\n')
	return builder.String()
}

func buildMovePatchText(fromPath, toPath string) string {
	var builder strings.Builder
	builder.WriteString("*** Update File: ")
	builder.WriteString(fromPath)
	builder.WriteByte('\n')
	builder.WriteString("*** Move to: ")
	builder.WriteString(toPath)
	builder.WriteByte('\n')
	return builder.String()
}

func buildApplyPatchText(path, before, appendedLine string) string {
	return buildMultiFilePatchText([]string{buildAppendPatchText(path, before, appendedLine)})
}

func splitPatchLines(content string) []string {
	trimmed := strings.TrimSuffix(content, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
