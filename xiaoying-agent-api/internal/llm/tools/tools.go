package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/utils"
)

type ToolInfo struct {
	Name               string         `json:"name"`
	Description        string         `json:"description"`
	Parameters         map[string]any `json:"parameters"`
	Required           []string       `json:"required"`
	Active             bool           `json:"active,omitempty"`
	DisableAutoSummary bool           `json:"-"`
}

type toolResponseType string

type (
	sessionIDContextKey               string
	messageIDContextKey               string
	planScopeIDContextKey             string
	streamOutputContextKey            string
	progressContextKey                string
	requestKindContextKey             string
	implicitPlanReuseContextKey       string
	modelOverrideContextKey           string
	modelOverrideCandidatesContextKey string
)

const (
	ToolResponseTypeText  toolResponseType = "text"
	ToolResponseTypeImage toolResponseType = "image"

	SessionIDContextKey               sessionIDContextKey               = "session_id"
	MessageIDContextKey               messageIDContextKey               = "message_id"
	PlanScopeIDContextKey             planScopeIDContextKey             = "plan_scope_id"
	StreamOutputContextKey            streamOutputContextKey            = "stream_output"
	ProgressContextKey                progressContextKey                = "progress"
	RequestKindContextKey             requestKindContextKey             = "request_kind"
	ImplicitPlanReuseContextKey       implicitPlanReuseContextKey       = "implicit_plan_reuse"
	ModelOverrideContextKey           modelOverrideContextKey           = "model_override"
	ModelOverrideCandidatesContextKey modelOverrideCandidatesContextKey = "model_override_candidates"

	DefaultTimeout = 5 * 60 * 1000  // 5 minutes in milliseconds
	MaxTimeout     = 60 * 60 * 1000 // 60 minutes in milliseconds

	// MaxOutputLength is the maximum length of a tool's output.
	// Increased to reduce repeated chunked reads on large code files.
	MaxOutputLength = 500 * 1024 // 500KB

	// RefOutputThreshold is the threshold to switch to reference-based output.
	// If output > 500KB, we save to file and return a reference.
	RefOutputThreshold = 500 * 1024
)

type StreamOutputCallback func(content string)
type ProgressCallback func(percentage float64, details string)

type ToolResponse struct {
	Type     toolResponseType `json:"type"`
	Content  string           `json:"content"`
	Metadata string           `json:"metadata,omitempty"`
	IsError  bool             `json:"is_error"`
}

func NewTextResponse(content string) ToolResponse {
	sanitized := utils.SanitizeUTF8(content)
	return ToolResponse{
		Type:    ToolResponseTypeText,
		Content: sanitized.Content,
	}
}

func NewReferenceResponse(summary, refPath, preview string) ToolResponse {
	content := fmt.Sprintf("%s\n[System: Output truncated because it exceeds the length limit. To read the rest, DO NOT increase 'limit'. Instead, use a new 'read' call with 'offset' set to the next line number, or decrease 'limit' to read smaller chunks. Content saved to %s. Preview: \"%s\"]", summary, refPath, preview)
	return ToolResponse{
		Type:    ToolResponseTypeText,
		Content: content,
	}
}

func WithResponseMetadata(response ToolResponse, metadata any) ToolResponse {
	if metadata != nil {
		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			return response
		}
		response.Metadata = string(metadataBytes)
	}
	return response
}

func NewTextErrorResponse(content string) ToolResponse {
	return ToolResponse{
		Type:    ToolResponseTypeText,
		Content: content,
		IsError: true,
	}
}

type ToolCall struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

type BaseTool interface {
	Info() ToolInfo
	Run(ctx context.Context, params ToolCall) (ToolResponse, error)
}

func GetContextValues(ctx context.Context) (string, string) {
	sessionID := ctx.Value(SessionIDContextKey)
	messageID := ctx.Value(MessageIDContextKey)
	if sessionID == nil {
		return "", ""
	}
	if messageID == nil {
		return sessionID.(string), ""
	}
	return sessionID.(string), messageID.(string)
}

func SetContextValues(ctx context.Context, sessionID, messageID string) context.Context {
	ctx = context.WithValue(ctx, SessionIDContextKey, sessionID)
	ctx = context.WithValue(ctx, MessageIDContextKey, messageID)
	return ctx
}

func GetPlanScopeID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	planScopeID, _ := ctx.Value(PlanScopeIDContextKey).(string)
	return strings.TrimSpace(planScopeID)
}

func SetPlanScopeID(ctx context.Context, planScopeID string) context.Context {
	planScopeID = strings.TrimSpace(planScopeID)
	if ctx == nil || planScopeID == "" {
		return ctx
	}
	return context.WithValue(ctx, PlanScopeIDContextKey, planScopeID)
}

func GetRequestKind(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestKind, _ := ctx.Value(RequestKindContextKey).(string)
	return strings.TrimSpace(requestKind)
}

func SetRequestKind(ctx context.Context, requestKind string) context.Context {
	requestKind = strings.TrimSpace(requestKind)
	if ctx == nil || requestKind == "" {
		return ctx
	}
	return context.WithValue(ctx, RequestKindContextKey, requestKind)
}

func AllowsImplicitPlanReuse(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, _ := ctx.Value(ImplicitPlanReuseContextKey).(bool)
	return allowed
}

func SetImplicitPlanReuse(ctx context.Context, allowed bool) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, ImplicitPlanReuseContextKey, allowed)
}

func ResolvePath(path string) string {
	return ResolvePathFor(context.Background(), path)
}

func ResolvePathFor(ctx context.Context, path string) string {
	// 1. Handle Absolute Paths
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	// 2. Try CWD Resolution
	cwd := config.WorkingDirectoryFor(ctx)
	absPath := filepath.Join(cwd, path)
	if _, err := os.Stat(absPath); err == nil {
		return absPath
	}

	// 3. Try stripping overlapping prefix from relative path
	// When cwd ends with "workspace" and path starts with "workspace/",
	// joining them produces "workspace/workspace/..." which doesn't exist.
	// We detect this by checking if the first component of path matches
	// the last component of cwd, and try without the overlapping part.
	if deduped := stripOverlappingPathPrefix(cwd, path); deduped != "" {
		if _, err := os.Stat(deduped); err == nil {
			return deduped
		}
	}

	// 4. Try ProjectRoot Fallback only for the shared/global workspace.
	if config.WorkspaceFromContext(ctx) == "" {
		if cfg := config.Get(); cfg != nil {
		projectRoot := cfg.ProjectRoot
		if projectRoot != "" && projectRoot != cwd {
			altPath := filepath.Join(projectRoot, path)
			if _, err := os.Stat(altPath); err == nil {
				return altPath
			}
		}
		}
	}

	// 5. Default to CWD
	return absPath
}

// stripOverlappingPathPrefix detects when the first component of relPath
// matches the last component of cwd, causing a duplicated path when joined.
// e.g. cwd="C:\project\workspace", relPath="workspace/file.js"
//
//	→ joins to "C:\project\workspace\workspace/file.js" (wrong)
//	→ should be "C:\project\workspace/file.js" (correct)
func stripOverlappingPathPrefix(cwd, relPath string) string {
	// Normalize separators
	relPath = filepath.ToSlash(relPath)
	parts := strings.SplitN(relPath, "/", 2)
	if len(parts) < 2 {
		return ""
	}

	firstComponent := parts[0]
	rest := parts[1]

	// Check if cwd's last directory name matches the first component of relPath
	cwdBase := filepath.Base(cwd)
	if strings.EqualFold(cwdBase, firstComponent) {
		candidate := filepath.Join(cwd, rest)
		return candidate
	}

	return ""
}

func TruncateOutput(content string) string {
	// Smart Compression for Directory Listings (ls)
	if strings.Contains(content, "\"type\": \"directory\"") || strings.Contains(content, "├──") {
		lines := strings.Split(content, "\n")
		if len(lines) > 50 { // Allow more lines for immediate tool output than history
			head := strings.Join(lines[:40], "\n")
			tail := strings.Join(lines[len(lines)-10:], "\n")
			return fmt.Sprintf("%s\n... [Directory listing compressed: %d lines hidden] ...\n%s", head, len(lines)-50, tail)
		}
		return content
	}

	// P0 Optimization: Reference-based Tool Output (Token Saving)
	// If output is massive (> RefOutputThreshold), save to file and return reference
	// NOTE: This requires file system access. Since tools package is low-level, we assume .auramate/logs exists or we create it?
	// But tools package shouldn't depend on high-level logic.
	// Actually, TruncateOutput is a pure function. It can't write files easily without context.
	// So we keep TruncateOutput as is for pure truncation, but we add a helper 'SaveAndReference' for tools to call.
	// However, existing tools call NewTextResponse directly.
	// We should update the specific tools (Read, RunCommand) to use the new pattern.
	// For now, let's keep TruncateOutput as a fallback safety net.

	if len(content) <= MaxOutputLength {
		return content
	}

	halfLength := MaxOutputLength / 2
	start := content[:halfLength]
	end := content[len(content)-halfLength:]

	truncatedLinesCount := CountLines(content[halfLength : len(content)-halfLength])
	return fmt.Sprintf("%s\n\n... [%d lines truncated] ...\n\n%s", start, truncatedLinesCount, end)
}

func CountLines(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// === GENERIC CANCELLATION HELPERS (Phase 5) ===

// CheckCancellation returns an error if the context has been cancelled.
// Use this in long-running loops or before expensive operations.
func CheckCancellation(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// ContextAwareScanner wraps a bufio.Scanner and checks for context cancellation on every scan.
type ContextAwareScanner struct {
	*bufio.Scanner
	ctx context.Context
}

func NewContextAwareScanner(ctx context.Context, r io.Reader) *ContextAwareScanner {
	return &ContextAwareScanner{
		Scanner: bufio.NewScanner(r),
		ctx:     ctx,
	}
}

func (s *ContextAwareScanner) Scan() bool {
	if err := CheckCancellation(s.ctx); err != nil {
		return false
	}
	return s.Scanner.Scan()
}

// ContextAwareWriter wraps an io.Writer and checks for context cancellation before every write.
type ContextAwareWriter struct {
	w   io.Writer
	ctx context.Context
}

func NewContextAwareWriter(ctx context.Context, w io.Writer) *ContextAwareWriter {
	return &ContextAwareWriter{
		w:   w,
		ctx: ctx,
	}
}

func (cw *ContextAwareWriter) Write(p []byte) (n int, err error) {
	if err := CheckCancellation(cw.ctx); err != nil {
		return 0, err
	}
	return cw.w.Write(p)
}
