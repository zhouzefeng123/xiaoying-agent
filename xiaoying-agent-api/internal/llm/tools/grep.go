package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/fileutil"
)

const (
	GrepToolName    = "grep"
	grepDescription = `Fast content search tool. Use this to find code by keywords or patterns.
Usage:
- pattern: regex or text to search (e.g. "func .*Handler", "TODO:", "error")
- path: starting directory (default: .)
- include: file pattern (e.g. "*.go", "*.{ts,tsx}")
- literal_text: set to true for non-regex, exact string search (e.g. searching for "(*User)")
- context_lines: number of lines to show before and after each match (e.g. 2 for 2 lines above and below)
Limitations:
- Returns max 3000 files (newest first)
- Skips hidden/binary files
- Output may be truncated for extremely large result sets
`
)

type GrepParams struct {
	Pattern      string `json:"pattern"`
	Path         string `json:"path"`
	Include      string `json:"include"`
	LiteralText  bool   `json:"literal_text"`
	ContextLines int    `json:"context_lines"` // Number of lines to show before and after match
}

type grepMatch struct {
	path     string
	modTime  time.Time
	lineNum  int
	lineText string
}

type GrepResponseMetadata struct {
	NumberOfMatches int  `json:"number_of_matches"`
	Truncated       bool `json:"truncated"`
}

type grepTool struct{}

func NewGrepTool() BaseTool {
	return &grepTool{}
}

func (g *grepTool) Info() ToolInfo {
	return ToolInfo{
		Name:        GrepToolName,
		Description: grepDescription,
		Parameters: map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "The regex pattern to search for in file contents",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "The directory to search in. Defaults to the current working directory.",
			},
			"include": map[string]any{
				"type":        "string",
				"description": "File pattern to include in the search (e.g. \"*.js\", \"*.{ts,tsx}\")",
			},
			"literal_text": map[string]any{
				"type":        "boolean",
				"description": "If true, the pattern will be treated as literal text with special regex characters escaped. Default is false.",
			},
			"context_lines": map[string]any{
				"type":        "integer",
				"description": "Number of lines to show before and after each match (e.g., 2). Helps understand the surrounding code.",
			},
		},
		Required: []string{"pattern"},
	}
}

// escapeRegexPattern escapes special regex characters so they're treated as literal characters
func escapeRegexPattern(pattern string) string {
	specialChars := []string{"\\", ".", "+", "*", "?", "(", ")", "[", "]", "{", "}", "^", "$", "|"}
	escaped := pattern

	for _, char := range specialChars {
		escaped = strings.ReplaceAll(escaped, char, "\\"+char)
	}

	return escaped
}

func (g *grepTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params GrepParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	if params.Pattern == "" {
		return NewTextErrorResponse("pattern is required"), nil
	}

	// If literal_text is true, escape the pattern
	searchPattern := params.Pattern
	if params.LiteralText {
		searchPattern = escapeRegexPattern(params.Pattern)
	}

	searchPath := params.Path
	if searchPath == "" {
		searchPath = config.WorkingDirectory()
	} else {
		searchPath = ResolvePath(searchPath)
	}

	// Set a timeout for the search operation
	searchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Limit matches to 3000 to reduce repeated searches on large repositories
	matches, truncated, err := searchFiles(searchCtx, searchPattern, searchPath, params.Include, params.ContextLines, 3000)
	if err != nil {
		if err == context.DeadlineExceeded {
			return ToolResponse{}, fmt.Errorf("search timed out after 30 seconds")
		}
		return ToolResponse{}, fmt.Errorf("error searching files: %w", err)
	}

	var output string
	if len(matches) == 0 {
		output = "No files found"
	} else {
		output = fmt.Sprintf("Found %d matches\n", len(matches))

		currentFile := ""
		for _, match := range matches {
			// Increase hard truncation limit to 2MB chars for large codebase searches
			if len(output) > 2*1024*1024 {
				truncated = true
				break
			}

			if currentFile != match.path {
				if currentFile != "" {
					output += "\n"
				}
				currentFile = match.path
				output += fmt.Sprintf("%s:\n", match.path)
			}

			// Truncate long lines (fallback for non-ripgrep or if max-columns didn't work as expected)
			lineText := match.lineText
			if len(lineText) > 4000 {
				lineText = lineText[:4000] + "... [truncated]"
			}

			if match.lineNum > 0 {
				output += fmt.Sprintf("  Line %d: %s\n", match.lineNum, lineText)
			} else {
				output += fmt.Sprintf("  %s\n", match.path)
			}
		}

		if truncated {
			output += "\n(Results are truncated. Consider using a more specific path or pattern.)"
		}
	}

	return WithResponseMetadata(
		NewTextResponse(output),
		GrepResponseMetadata{
			NumberOfMatches: len(matches),
			Truncated:       truncated,
		},
	), nil
}

func searchFiles(ctx context.Context, pattern, rootPath, include string, contextLines, limit int) ([]grepMatch, bool, error) {
	matches, err := searchWithRipgrep(ctx, pattern, rootPath, include, contextLines)
	if err != nil {
		// Only fallback if ripgrep is missing, not if it timed out or failed
		if strings.Contains(err.Error(), "ripgrep not found") {
			matches, err = searchFilesWithRegex(ctx, pattern, rootPath, include, contextLines)
			if err != nil {
				return nil, false, err
			}
		} else {
			return nil, false, err
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].modTime.After(matches[j].modTime)
	})

	truncated := len(matches) > limit
	if truncated {
		matches = matches[:limit]
	}

	return matches, truncated, nil
}

func searchWithRipgrep(ctx context.Context, pattern, path, include string, contextLines int) ([]grepMatch, error) {
	_, err := exec.LookPath("rg")
	if err != nil {
		return nil, fmt.Errorf("ripgrep not found: %w", err)
	}

	// Use -n to show line numbers and include the matched line
	// Use --max-columns to limit line length (prevent huge minified lines)
	// Use --no-heading to ensure consistent parsing even with context lines
	args := []string{"-H", "-n", "--max-columns", "4000", "--no-heading", pattern}
	if contextLines > 0 {
		args = append(args, "-C", strconv.Itoa(contextLines))
	}
	if include != "" {
		args = append(args, "--glob", include)
	}
	args = append(args, path)

	cmd := exec.CommandContext(ctx, "rg", args...)
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("search timed out")
		}
		if ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("search canceled")
		}
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return []grepMatch{}, nil
		}
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	matches := make([]grepMatch, 0, len(lines))

	// Safety limit: process up to 5000 matches from rg to reduce repeated queries
	if len(lines) > 5000 {
		lines = lines[:5000]
	}

	for _, line := range lines {
		// Check for context cancellation during processing large results
		if err := CheckCancellation(ctx); err != nil {
			return nil, err
		}

		if line == "" || line == "--" {
			continue
		}

		// Parse ripgrep output format: file:line:content or file-line-content (for context lines)
		// A bit tricky because Windows paths have C:\
		// rg uses '-' for context lines and ':' for matching lines

		// Robust parsing handling Windows paths (e.g. C:\path\to\file.go:10:content or C:\path\to\file.go-10-content)
		var filePath string
		var lineNumStr string
		var lineText string
		var isContextLine bool

		// Find the first separator after the drive letter if it's a Windows path
		sepIdx := -1
		if len(line) > 2 && line[1] == ':' && (line[2] == '\\' || line[2] == '/') {
			// Windows path, skip the drive letter colon
			sepIdxMatch := strings.Index(line[3:], ":")
			sepIdxCtx := strings.Index(line[3:], "-")

			if sepIdxMatch != -1 && (sepIdxCtx == -1 || sepIdxMatch < sepIdxCtx) {
				sepIdx = sepIdxMatch + 3
				isContextLine = false
			} else if sepIdxCtx != -1 {
				sepIdx = sepIdxCtx + 3
				isContextLine = true
			}
		} else {
			sepIdxMatch := strings.Index(line, ":")
			sepIdxCtx := strings.Index(line, "-")

			if sepIdxMatch != -1 && (sepIdxCtx == -1 || sepIdxMatch < sepIdxCtx) {
				sepIdx = sepIdxMatch
				isContextLine = false
			} else if sepIdxCtx != -1 {
				sepIdx = sepIdxCtx
				isContextLine = true
			}
		}

		if sepIdx == -1 {
			continue
		}

		filePath = line[:sepIdx]
		remainder := line[sepIdx+1:]

		// Now find the second separator (line number to content)
		sep2Idx := -1
		if isContextLine {
			sep2Idx = strings.Index(remainder, "-")
		} else {
			sep2Idx = strings.Index(remainder, ":")
		}

		if sep2Idx == -1 {
			continue
		}

		lineNumStr = remainder[:sep2Idx]
		lineText = remainder[sep2Idx+1:]

		lineNum, err := strconv.Atoi(lineNumStr)
		if err != nil {
			continue
		}

		fileInfo, err := os.Stat(filePath)
		if err != nil {
			continue // Skip files we can't access
		}

		// Optional: Mark context lines slightly differently if needed, here we just output them
		matches = append(matches, grepMatch{
			path:     filePath,
			modTime:  fileInfo.ModTime(),
			lineNum:  lineNum,
			lineText: lineText,
		})
	}

	return matches, nil
}

func searchFilesWithRegex(ctx context.Context, pattern, rootPath, include string, contextLines int) ([]grepMatch, error) {
	matches := []grepMatch{}

	regex, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}

	var includePattern *regexp.Regexp
	if include != "" {
		regexPattern := globToRegex(include)
		includePattern, err = regexp.Compile(regexPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid include pattern: %w", err)
		}
	}

	err = filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err := CheckCancellation(ctx); err != nil {
			return err
		}

		if err != nil {
			return nil // Skip errors
		}

		if info.IsDir() {
			return nil // Skip directories
		}

		if fileutil.SkipHidden(path) {
			return nil
		}

		if includePattern != nil && !includePattern.MatchString(path) {
			return nil
		}

		fileMatches, err := fileContainsPatternWithContext(ctx, path, regex, contextLines)
		if err != nil {
			return nil // Skip files we can't read
		}

		if len(fileMatches) > 0 {
			for _, fm := range fileMatches {
				matches = append(matches, grepMatch{
					path:     path,
					modTime:  info.ModTime(),
					lineNum:  fm.lineNum,
					lineText: fm.lineText,
				})
			}

			if len(matches) >= 200 {
				return filepath.SkipAll
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return matches, nil
}

type fileMatchResult struct {
	lineNum  int
	lineText string
}

func fileContainsPatternWithContext(ctx context.Context, filePath string, pattern *regexp.Regexp, contextLines int) ([]fileMatchResult, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := NewContextAwareScanner(ctx, file)
	var allLines []string
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var results []fileMatchResult
	matchedLines := make(map[int]bool) // track added lines to avoid duplicates when contexts overlap

	for i, line := range allLines {
		if pattern.MatchString(line) {
			// Add preceding context
			startIdx := i - contextLines
			if startIdx < 0 {
				startIdx = 0
			}

			// Add succeeding context
			endIdx := i + contextLines
			if endIdx >= len(allLines) {
				endIdx = len(allLines) - 1
			}

			for j := startIdx; j <= endIdx; j++ {
				if !matchedLines[j] {
					results = append(results, fileMatchResult{
						lineNum:  j + 1,
						lineText: allLines[j],
					})
					matchedLines[j] = true
				}
			}
		}
	}

	return results, nil
}

func globToRegex(glob string) string {
	regexPattern := strings.ReplaceAll(glob, ".", "\\.")
	regexPattern = strings.ReplaceAll(regexPattern, "*", ".*")
	regexPattern = strings.ReplaceAll(regexPattern, "?", ".")

	re := regexp.MustCompile(`\{([^}]+)\}`)
	regexPattern = re.ReplaceAllStringFunc(regexPattern, func(match string) string {
		inner := match[1 : len(match)-1]
		return "(" + strings.ReplaceAll(inner, ",", "|") + ")"
	})

	return regexPattern
}
