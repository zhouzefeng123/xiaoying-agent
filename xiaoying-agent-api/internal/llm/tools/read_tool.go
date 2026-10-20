package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/auramate/auramate/internal/goword"
	"github.com/auramate/auramate/internal/logging"
)

const (
	ReadToolName     = "read"
	MaxReadFileSize  = 10 * 1024 * 1024 // 10MB limit to reduce repeated chunked reads
	DefaultReadLines = 5000             // Higher default for large source files
	MaxLineLength    = 10000            // Allow longer lines before truncation
	readDescription  = `Reads a file from the local filesystem.
Usage:
- file_path: The absolute path to the file.
- limit: Number of lines to read (default: 5000). Set to a higher value (e.g. 50000) for large files.
- offset: Line number to start reading from (1-based, optional).
- search_term: Optional regex pattern. If provided, reads lines starting from the first match.
- mode: 'full' (default) or 'skeleton' (extracts only function signatures, classes, etc.).
Features:
- Returns file contents with line numbers.
- For .docx files, uses the built-in go-word parser to extract readable body, table, header, and footer text.
- If session_id is provided, .docx content is read from the current go-word session snapshot before any on-disk file path.
- For .docx understanding, rough search, and full-text review, prefer this tool; if you need precise Word paragraph/table/cell IDs, page-level disambiguation, or session-scoped locator evidence, use auramate-word-reader.
- Handles huge files via offset/limit/search_term if needed.
- Truncates lines > 10000 chars.
- Auto-corrects 'path'/'file_name' -> 'file_path'.
`
)

var (
	readToolGoWordMu      sync.RWMutex
	readToolGoWordService *goword.Service
)

type ReadParams struct {
	FilePath   string `json:"file_path"`
	SessionID  string `json:"session_id"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	SearchTerm string `json:"search_term"`
	Mode       string `json:"mode"`
	// Aliases
	Path     string `json:"path"`
	FileName string `json:"file_name"`
}

type readTool struct{}

func SetReadToolGoWordService(service *goword.Service) {
	readToolGoWordMu.Lock()
	defer readToolGoWordMu.Unlock()
	readToolGoWordService = service
}

func getReadToolGoWordService() *goword.Service {
	readToolGoWordMu.RLock()
	defer readToolGoWordMu.RUnlock()
	return readToolGoWordService
}

func NewReadTool() BaseTool {
	return &readTool{}
}

func (r *readTool) Info() ToolInfo {
	return ToolInfo{
		Name:        ReadToolName,
		Description: readDescription,
		Parameters: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to read. Required unless session_id is provided.",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Optional go-word session ID. For .docx reads, this takes priority and reads the current in-memory session snapshot instead of disk.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "The number of lines to read (must be at least 1).",
				"minimum":     1,
				"maximum":     50000,
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "The line number to start reading from (must be at least 1).",
				"minimum":     1,
			},
			"search_term": map[string]any{
				"type":        "string",
				"description": "Optional regex pattern. If provided, the read will start from the first line matching this pattern. Ignores 'offset' if matched.",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"full", "skeleton", "definitions"},
				"description": "Read mode. 'full' reads content (default). 'skeleton' (or 'definitions') only reads function signatures, types, and docstrings (outline).",
			},
		},
		Required: []string{},
	}
}

func (r *readTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params ReadParams
	logging.Debug("read tool params", "params", call.Input)
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	// Parameter Normalization
	if params.FilePath == "" {
		if params.Path != "" {
			params.FilePath = params.Path
		} else if params.FileName != "" {
			params.FilePath = params.FileName
		}
	}

	params.SessionID = strings.TrimSpace(params.SessionID)
	if params.FilePath == "" && params.SessionID == "" {
		return NewTextErrorResponse("file_path is required unless session_id is provided (or use 'path'/'file_name')"), nil
	}

	if params.Limit <= 0 {
		params.Limit = DefaultReadLines
	}

	if params.Mode == "" {
		params.Mode = "full"
	}
	// Alias definitions to skeleton
	if params.Mode == "definitions" {
		params.Mode = "skeleton"
	}

	filePath := ""
	var fileInfo os.FileInfo
	var err error
	useSessionSnapshot := params.SessionID != ""
	if params.FilePath != "" {
		filePath = ResolvePathFor(ctx, params.FilePath)
	}

	if !useSessionSnapshot || filePath != "" {
		if filePath == "" {
			return NewTextErrorResponse("file_path is required for non-session reads"), nil
		}
		fileInfo, err = os.Stat(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				if !useSessionSnapshot {
					return NewTextErrorResponse(fmt.Sprintf("File not found: %s", filePath)), nil
				}
			} else {
				return ToolResponse{}, fmt.Errorf("error accessing file: %w", err)
			}
		}
		if err == nil && fileInfo.IsDir() {
			return NewTextErrorResponse(fmt.Sprintf("Path is a directory, not a file: %s", filePath)), nil
		}
	}

	isDOCX := useSessionSnapshot
	if !isDOCX && filePath != "" {
		isDOCX = isDOCXFile(filePath)
	}

	if !useSessionSnapshot && fileInfo != nil && fileInfo.Size() > MaxReadFileSize {
		// Warn but allow reading with offset or search_term
		if !isDOCX && params.Offset <= 1 && params.SearchTerm == "" && params.Mode != "skeleton" {
			return NewTextErrorResponse(fmt.Sprintf("File is too large (%d bytes). Use 'offset', 'search_term' to read specific parts or use 'mode': 'skeleton' to read outline.", fileInfo.Size())), nil
		}
	}

	// Adjust offset to 0-based for internal processing if provided (User provides 1-based)
	internalOffset := 0
	if params.Offset > 0 {
		internalOffset = params.Offset - 1
	}

	var content string
	var lineCount int
	resolvedReadPath := filePath

	if isDOCX {
		if useSessionSnapshot {
			content, lineCount, resolvedReadPath, err = readDOCXSession(ctx, params.SessionID, filePath, internalOffset, params.Limit, params.SearchTerm, params.Mode)
		} else {
			content, lineCount, err = readDOCXFile(ctx, filePath, internalOffset, params.Limit, params.SearchTerm, params.Mode)
		}
	} else {
		if params.SearchTerm != "" {
			re, err := regexp.Compile(params.SearchTerm)
			if err == nil {
				matchOffset, err := findLineOffset(ctx, filePath, re)
				if err == nil && matchOffset >= 0 {
					internalOffset = matchOffset
				}
			}
		}

		if params.Mode == "skeleton" {
			content, lineCount, err = readSkeleton(ctx, filePath)
		} else {
			content, lineCount, err = readTextFile(ctx, filePath, internalOffset, params.Limit)
		}
	}

	if err != nil {
		return ToolResponse{}, fmt.Errorf("error reading file: %w", err)
	}

	// Record file read for permission/tracking
	if resolvedReadPath != "" {
		if info, statErr := os.Stat(resolvedReadPath); statErr == nil && !info.IsDir() {
			recordFileRead(resolvedReadPath)
		}
	}

	output := "<file_content>\n"
	if params.Mode == "skeleton" {
		output = fmt.Sprintf("<file_skeleton path=\"%s\">\n%s\n</file_skeleton>", resolvedReadPath, content)
		return NewTextResponse(output), nil
	}

	output += addLineNumbers(content, internalOffset+1)
	output += "\n</file_content>"
	if isDOCX {
		output += buildDOCXLocatorHint(params)
	}

	// 添加读取范围提示，避免重复读取
	startLine := internalOffset + 1
	endLine := startLine + len(strings.Split(content, "\n")) - 1
	output += fmt.Sprintf("\n\n[读取范围: 第 %d-%d 行]", startLine, endLine)

	if lineCount > internalOffset+len(strings.Split(content, "\n")) {
		output += fmt.Sprintf("\n(File has more lines. Use 'offset' parameter to read beyond line %d)", internalOffset+params.Limit)
	}

	// Token Safety Warning for Huge Reads
	// If content is very large (> 100KB), append a system tip to guide future usage.
	if len(output) > 100*1024 {
		output += fmt.Sprintf("\n\n<system_tip>WARNING: Read %d bytes (~%d tokens). This is a very large file. For better performance and lower cost, consider using 'grep' or 'read' with 'mode': 'skeleton' if you only need specific parts.</system_tip>", len(output), len(output)/4)
	}

	return NewTextResponse(output), nil
}

func buildDOCXLocatorHint(params ReadParams) string {
	if params.Mode != "full" {
		return ""
	}

	contextRef := "同一个 file_path"
	if params.SessionID != "" {
		contextRef = fmt.Sprintf("同一个 session_id=%q", params.SessionID)
	}

	queryHint := "上面命中的原文短语"
	if strings.TrimSpace(params.SearchTerm) != "" {
		queryHint = "上面匹配结果里的原文短语"
	}

	return fmt.Sprintf("\n<word_locator_hint>\n如果下一步需要精确落到 Word 段落、表格或单元格，请改用 auramate-word-reader，并沿用%s。\n建议先用 query=%q 做精确检索，再按需要补 focus_hit_index；如果目标在表格里，再继续补 focus_table_id、focus_row_index、focus_cell_index；如果存在分页歧义，再加 include_layout=true 或 focus_page_index。\n</word_locator_hint>", contextRef, queryHint)
}

// readSkeleton reads the file and extracts function signatures, type definitions, and docstrings.
// This is a heuristic-based approach for common languages.
func readSkeleton(ctx context.Context, filePath string) (string, int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(filePath))
	scanner := NewContextAwareScanner(ctx, file)
	var lines []string
	var commentBuffer []string
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			commentBuffer = nil // Reset comments on empty lines to avoid attaching unrelated comments
			continue
		}

		// Check if it's a comment
		isComment := false
		switch ext {
		case ".go", ".js", ".ts", ".jsx", ".tsx", ".java", ".c", ".cpp", ".cs", ".rs":
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
				isComment = true
			}
		case ".py":
			if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "\"\"\"") || strings.HasPrefix(trimmed, "'''") {
				isComment = true
			}
		}

		if isComment {
			commentBuffer = append(commentBuffer, fmt.Sprintf("%d| %s", lineNum, line))
			continue
		}

		// Simple heuristics for definitions
		isDef := false

		switch ext {
		case ".go":
			if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "package ") || strings.HasPrefix(trimmed, "const ") || strings.HasPrefix(trimmed, "var ") {
				isDef = true
			}
		case ".py":
			if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class ") || strings.HasPrefix(trimmed, "@") {
				isDef = true
			}
		case ".js", ".ts", ".jsx", ".tsx":
			if strings.HasPrefix(trimmed, "function ") || strings.HasPrefix(trimmed, "class ") || strings.HasPrefix(trimmed, "interface ") || strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "export ") || strings.HasPrefix(trimmed, "const ") {
				// JS/TS is trickier, but let's catch basic exports and definitions
				isDef = true
			}
		case ".java", ".c", ".cpp", ".cs":
			// Very basic C-style heuristic: check for typical start words
			if strings.HasPrefix(trimmed, "public ") || strings.HasPrefix(trimmed, "private ") || strings.HasPrefix(trimmed, "protected ") || strings.HasPrefix(trimmed, "class ") || strings.HasPrefix(trimmed, "interface ") || strings.HasPrefix(trimmed, "struct ") || strings.HasPrefix(trimmed, "enum ") {
				isDef = true
			}
		case ".rs":
			if strings.HasPrefix(trimmed, "fn ") || strings.HasPrefix(trimmed, "struct ") || strings.HasPrefix(trimmed, "enum ") || strings.HasPrefix(trimmed, "impl ") || strings.HasPrefix(trimmed, "trait ") || strings.HasPrefix(trimmed, "pub ") {
				isDef = true
			}
		default:
			// Fallback: Read first 100 lines for unknown types
			if lineNum <= 100 {
				isDef = true
			}
		}

		if isDef {
			// Flush buffered comments
			if len(commentBuffer) > 0 {
				lines = append(lines, commentBuffer...)
				commentBuffer = nil
			}
			// Add definition line
			lines = append(lines, fmt.Sprintf("%d| %s", lineNum, line))
		} else {
			// Not a definition, discard comments
			commentBuffer = nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", 0, err
	}

	return strings.Join(lines, "\n"), lineNum, nil
}

func findLineOffset(ctx context.Context, filePath string, pattern *regexp.Regexp) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return -1, err
	}
	defer file.Close()

	scanner := NewContextAwareScanner(ctx, file)
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Text()
		if pattern.MatchString(line) {
			return lineNum, nil // 0-based offset internally
		}
		lineNum++
	}
	return -1, scanner.Err()
}

func addLineNumbers(content string, startLine int) string {
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")

	var result []string
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")

		lineNum := i + startLine
		numStr := fmt.Sprintf("%d", lineNum)

		if len(numStr) >= 6 {
			result = append(result, fmt.Sprintf("%s|%s", numStr, line))
		} else {
			paddedNum := fmt.Sprintf("%6s", numStr)
			result = append(result, fmt.Sprintf("%s|%s", paddedNum, line))
		}
	}

	return strings.Join(result, "\n")
}

func isDOCXFile(filePath string) bool {
	return strings.EqualFold(filepath.Ext(filePath), ".docx")
}

func readDOCXSession(ctx context.Context, sessionID, fallbackPath string, offset, limit int, searchTerm, mode string) (string, int, string, error) {
	service := getReadToolGoWordService()
	if service == nil {
		return "", 0, "", fmt.Errorf("go-word session service is unavailable for session_id=%s", sessionID)
	}

	session, err := service.Snapshot(sessionID)
	if err != nil {
		return "", 0, "", err
	}
	if session == nil || session.Document == nil {
		return "", 0, "", fmt.Errorf("session %s has no document snapshot", sessionID)
	}

	displayPath := strings.TrimSpace(session.Document.SourcePath)
	if displayPath == "" {
		displayPath = fallbackPath
	}
	if displayPath == "" {
		displayPath = fmt.Sprintf("goword-session:%s", sessionID)
	}

	if mode == "skeleton" {
		lines := renderDOCXSkeletonLines(session.Document)
		return strings.Join(lines, "\n"), len(lines), displayPath, nil
	}

	lines := renderDOCXContentLines(session.Document)
	if searchTerm != "" {
		re, compileErr := regexp.Compile(searchTerm)
		if compileErr == nil {
			if matchOffset := findLineOffsetInLines(lines, re); matchOffset >= 0 {
				offset = matchOffset
			}
		}
	}

	content, err := readRenderedLines(ctx, lines, offset, limit)
	if err != nil {
		return "", 0, "", err
	}
	return content, len(lines), displayPath, nil
}

func readDOCXFile(ctx context.Context, filePath string, offset, limit int, searchTerm, mode string) (string, int, error) {
	document, err := goword.OpenDOCX(filePath)
	if err != nil {
		return "", 0, err
	}

	if mode == "skeleton" {
		lines := renderDOCXSkeletonLines(document)
		return strings.Join(lines, "\n"), len(lines), nil
	}

	lines := renderDOCXContentLines(document)
	if searchTerm != "" {
		re, err := regexp.Compile(searchTerm)
		if err == nil {
			if matchOffset := findLineOffsetInLines(lines, re); matchOffset >= 0 {
				offset = matchOffset
			}
		}
	}

	content, err := readRenderedLines(ctx, lines, offset, limit)
	if err != nil {
		return "", 0, err
	}
	return content, len(lines), nil
}

func renderDOCXContentLines(document *goword.Document) []string {
	if document == nil {
		return nil
	}
	var lines []string
	if len(document.Blocks) == 0 {
		lines = make([]string, 0, len(document.Paragraphs))
		for _, paragraph := range document.Paragraphs {
			lines = append(lines, splitDOCXParagraphLines(paragraph.PlainText())...)
		}
	} else {
		for _, block := range document.Blocks {
			lines = append(lines, renderDOCXBlockContentLines(block)...)
		}
	}
	lines = append(lines, renderDOCXHeaderFooterContentLines(document.Meta.HeaderFooters)...)
	return lines
}

func renderDOCXHeaderFooterContentLines(parts []goword.HeaderFooterPart) []string {
	if len(parts) == 0 {
		return nil
	}
	lines := make([]string, 0)
	for _, part := range parts {
		partLines := renderDOCXHeaderFooterPartContentLines(part)
		if len(partLines) == 0 {
			continue
		}
		lines = append(lines, partLines...)
	}
	return lines
}

func renderDOCXSkeletonLines(document *goword.Document) []string {
	if document == nil {
		return nil
	}
	var lines []string
	if len(document.Blocks) == 0 {
		for _, paragraph := range document.Paragraphs {
			text := strings.TrimSpace(paragraph.PlainText())
			if text == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("paragraph style=%s text=%s", paragraph.Style.StyleID, previewDOCXText(text, 120)))
		}
		lines = append(lines, renderDOCXHeaderFooterSkeletonLines(document.Meta.HeaderFooters)...)
		return lines
	}
	for _, block := range document.Blocks {
		switch block.Type {
		case goword.BlockTypeTable:
			if block.Table == nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("table id=%s rows=%d cols=%d", block.Table.ID, len(block.Table.Rows), docxTableColumnCount(*block.Table)))
		case goword.BlockTypeParagraph:
			if block.Paragraph == nil {
				continue
			}
			text := strings.TrimSpace(block.Paragraph.PlainText())
			if text == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("paragraph style=%s text=%s", block.Paragraph.Style.StyleID, previewDOCXText(text, 120)))
		}
	}
	lines = append(lines, renderDOCXHeaderFooterSkeletonLines(document.Meta.HeaderFooters)...)
	return lines
}

func renderDOCXHeaderFooterSkeletonLines(parts []goword.HeaderFooterPart) []string {
	if len(parts) == 0 {
		return nil
	}
	lines := make([]string, 0)
	for _, part := range parts {
		label := describeDOCXHeaderFooterPart(part)
		appended := false
		if len(part.Blocks) == 0 {
			for _, paragraph := range part.Paragraphs {
				text := strings.TrimSpace(paragraph.PlainText())
				if text == "" {
					continue
				}
				lines = append(lines, fmt.Sprintf("%s paragraph style=%s text=%s", label, paragraph.Style.StyleID, previewDOCXText(text, 120)))
				appended = true
			}
		}
		for _, block := range part.Blocks {
			switch block.Type {
			case goword.BlockTypeTable:
				if block.Table == nil {
					continue
				}
				lines = append(lines, fmt.Sprintf("%s table id=%s rows=%d cols=%d", label, block.Table.ID, len(block.Table.Rows), docxTableColumnCount(*block.Table)))
				appended = true
			case goword.BlockTypeParagraph:
				if block.Paragraph == nil {
					continue
				}
				text := strings.TrimSpace(block.Paragraph.PlainText())
				if text == "" {
					continue
				}
				lines = append(lines, fmt.Sprintf("%s paragraph style=%s text=%s", label, block.Paragraph.Style.StyleID, previewDOCXText(text, 120)))
				appended = true
			}
		}
		if !appended {
			lines = append(lines, label)
		}
	}
	return lines
}

func renderDOCXHeaderFooterPartContentLines(part goword.HeaderFooterPart) []string {
	label := fmt.Sprintf("[%s]", describeDOCXHeaderFooterPart(part))
	lines := []string{label}
	if len(part.Blocks) == 0 {
		for _, paragraph := range part.Paragraphs {
			lines = append(lines, splitDOCXParagraphLines(paragraph.PlainText())...)
		}
		return lines
	}
	for _, block := range part.Blocks {
		lines = append(lines, renderDOCXBlockContentLines(block)...)
	}
	return lines
}

func describeDOCXHeaderFooterPart(part goword.HeaderFooterPart) string {
	kind := strings.TrimSpace(strings.ToLower(part.Kind))
	if kind == "" {
		kind = "header_footer"
	}
	variant := strings.TrimSpace(strings.ToLower(part.Variant))
	if variant == "" {
		variant = "default"
	}
	return fmt.Sprintf("%s %s", kind, variant)
}

func renderDOCXBlockContentLines(block goword.Block) []string {
	switch block.Type {
	case goword.BlockTypeTable:
		if block.Table == nil {
			return nil
		}
		tableID := strings.TrimSpace(block.Table.ID)
		if tableID == "" {
			tableID = "table"
		}
		lines := []string{fmt.Sprintf("[table %s]", tableID)}
		for _, row := range block.Table.Rows {
			cellTexts := make([]string, 0, len(row.Cells))
			for _, cell := range row.Cells {
				cellTexts = append(cellTexts, normalizeDOCXInlineText(docxTableCellPlainText(cell)))
			}
			lines = append(lines, strings.Join(cellTexts, " | "))
		}
		return lines
	case goword.BlockTypeParagraph:
		if block.Paragraph == nil {
			return nil
		}
		return splitDOCXParagraphLines(block.Paragraph.PlainText())
	default:
		return nil
	}
}

func splitDOCXParagraphLines(text string) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		normalized := normalizeDOCXInlineText(part)
		if normalized != "" {
			lines = append(lines, normalized)
		}
	}
	return lines
}

func normalizeDOCXInlineText(text string) string {
	replaced := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", " / "), "\n", " / ")
	return strings.TrimSpace(replaced)
}

func previewDOCXText(text string, limit int) string {
	normalized := normalizeDOCXInlineText(text)
	if len(normalized) <= limit {
		return normalized
	}
	return normalized[:limit] + "..."
}

func docxTableCellPlainText(cell goword.TableCell) string {
	parts := make([]string, 0)
	for _, block := range cell.Blocks {
		switch block.Type {
		case goword.BlockTypeTable:
			if block.Table != nil {
				parts = append(parts, normalizeDOCXInlineText(docxTablePlainText(*block.Table)))
			}
		case goword.BlockTypeParagraph:
			if block.Paragraph != nil {
				text := strings.TrimSpace(block.Paragraph.PlainText())
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func docxTablePlainText(table goword.Table) string {
	rowTexts := make([]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		cellTexts := make([]string, 0, len(row.Cells))
		for _, cell := range row.Cells {
			cellTexts = append(cellTexts, normalizeDOCXInlineText(docxTableCellPlainText(cell)))
		}
		rowTexts = append(rowTexts, strings.Join(cellTexts, " | "))
	}
	return strings.TrimSpace(strings.Join(rowTexts, "\n"))
}

func docxTableColumnCount(table goword.Table) int {
	maxColumns := 0
	for _, row := range table.Rows {
		if len(row.Cells) > maxColumns {
			maxColumns = len(row.Cells)
		}
	}
	return maxColumns
}

func findLineOffsetInLines(lines []string, pattern *regexp.Regexp) int {
	for index, line := range lines {
		if pattern.MatchString(line) {
			return index
		}
	}
	return -1
}

func readRenderedLines(ctx context.Context, lines []string, offset, limit int) (string, error) {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(lines) {
		return "", nil
	}
	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}
	selected := make([]string, 0, end-offset)
	for index := offset; index < end; index++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		selected = append(selected, lines[index])
	}
	return strings.Join(selected, "\n"), nil
}

func readTextFile(ctx context.Context, filePath string, offset, limit int) (string, int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	lineCount := 0

	scanner := NewLineScanner(file)
	if offset > 0 {
		for lineCount < offset && scanner.Scan() {
			lineCount++
		}
		if err = scanner.Err(); err != nil {
			return "", 0, err
		}
	}

	if offset == 0 {
		_, err = file.Seek(0, io.SeekStart)
		if err != nil {
			return "", 0, err
		}
	}

	var lines []string
	lineCount = offset

	for scanner.Scan() && len(lines) < limit {
		// Check context
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		default:
		}

		lineCount++
		lineText := scanner.Text()
		if len(lineText) > MaxLineLength {
			// Token Optimization: Explicitly mark truncated lines so LLM doesn't hallucinate content
			lineText = lineText[:MaxLineLength] + "... [TRUNCATED]"
		}
		lines = append(lines, lineText)
	}

	// Continue scanning to get total line count
	for scanner.Scan() {
		lineCount++
	}

	if err := scanner.Err(); err != nil {
		return "", 0, err
	}

	return strings.Join(lines, "\n"), lineCount, nil
}

// NewLineScanner creates a scanner that handles large lines by increasing the buffer size
func NewLineScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	const maxCapacity = 10 * 1024 * 1024 // 10MB
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)
	return scanner
}
