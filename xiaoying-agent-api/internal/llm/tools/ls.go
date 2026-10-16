package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/auramate/auramate/internal/config"
)

const (
	LSToolName    = "ls"
	MaxLSFiles    = 1000
	lsDescription = `Lists files and directories.
USAGE:
- path: directory path (default: .) OR specific file path to check existence.
- recursive: true to list subdirectories.
- pattern: glob pattern to filter (e.g. "*.go", "src/**/*.ts").
- depth: max depth to traverse (default: 1).
NOTES:
- Returns a tree structure.
- Limited to 1000 files.
- Use this to explore project structure or find files by name.
`
)

type LSParams struct {
	Path      string   `json:"path"`
	Ignore    []string `json:"ignore"`
	Depth     int      `json:"depth"`
	Recursive bool     `json:"recursive"`
	Pattern   string   `json:"pattern"`
	// Aliases
	FilePath string `json:"file_path"`
	Dir      string `json:"dir"`
}

type TreeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Type     string      `json:"type"` // "file" or "directory"
	Children []*TreeNode `json:"children,omitempty"`
}

type LSResponseMetadata struct {
	NumberOfFiles int  `json:"number_of_files"`
	Truncated     bool `json:"truncated"`
}

type lsTool struct{}

func NewLsTool() BaseTool {
	return &lsTool{}
}

func (l *lsTool) Info() ToolInfo {
	return ToolInfo{
		Name:        LSToolName,
		Description: lsDescription,
		Parameters: map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The path to the directory to list (defaults to current working directory)",
			},
			"ignore": map[string]any{
				"type":        "array",
				"description": "List of glob patterns to ignore",
				"items": map[string]any{
					"type": "string",
				},
			},
			"depth": map[string]any{
				"type":        "integer",
				"description": "Max depth to traverse (default: 1 if recursive=false, unlimited if recursive=true)",
			},
			"recursive": map[string]any{
				"type":        "boolean",
				"description": "Whether to list subdirectories recursively (default: false)",
			},
			"pattern": map[string]any{
				"type":        "string",
				"description": "Glob pattern to filter files (e.g. *.go)",
			},
		},
		Required: []string{"path"},
	}
}

func (l *lsTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params LSParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	// Parameter Normalization
	if params.Path == "" {
		if params.FilePath != "" {
			params.Path = params.FilePath
		} else if params.Dir != "" {
			params.Path = params.Dir
		}
	}

	searchPath := params.Path
	if searchPath == "" {
		searchPath = config.WorkingDirectory()
	} else {
		searchPath = ResolvePath(searchPath)
	}

	if _, err := os.Stat(searchPath); os.IsNotExist(err) {
		return NewTextErrorResponse(fmt.Sprintf("path does not exist: %s", searchPath)), nil
	}

	// Default depth logic
	maxDepth := params.Depth
	if maxDepth == 0 && !params.Recursive {
		maxDepth = 1
	}

	files, truncated, err := listDirectory(ctx, searchPath, params.Ignore, params.Pattern, MaxLSFiles, maxDepth)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("error listing directory: %w", err)
	}

	tree := createFileTree(files)
	output := printTree(tree, searchPath)

	if truncated {
		output = fmt.Sprintf("There are more than %d files in the directory. Use a more specific path or use the Glob tool to find specific files. The first %d files and directories are included below:\n\n%s", MaxLSFiles, MaxLSFiles, output)
	}

	return WithResponseMetadata(
		NewTextResponse(output),
		LSResponseMetadata{
			NumberOfFiles: len(files),
			Truncated:     truncated,
		},
	), nil
}

func listDirectory(ctx context.Context, initialPath string, ignorePatterns []string, includePattern string, limit int, maxDepth int) ([]string, bool, error) {
	var results []string
	truncated := false

	err := filepath.Walk(initialPath, func(path string, info os.FileInfo, err error) error {
		// Check for context cancellation
		if err := CheckCancellation(ctx); err != nil {
			return err
		}

		if err != nil {
			return nil // Skip files we don't have permission to access
		}

		if maxDepth > 0 {
			rel, err := filepath.Rel(initialPath, path)
			if err == nil && rel != "." {
				depth := strings.Count(rel, string(filepath.Separator)) + 1
				if depth > maxDepth {
					if info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
		}

		if shouldSkip(path, ignorePatterns) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Filter by pattern (only for files)
		if includePattern != "" && !info.IsDir() {
			matched, err := filepath.Match(includePattern, filepath.Base(path))
			if err != nil || !matched {
				return nil
			}
		}

		if path != initialPath {
			if info.IsDir() {
				path = path + string(filepath.Separator)
			}
			results = append(results, path)
		}

		if len(results) >= limit {
			truncated = true
			return filepath.SkipAll
		}

		return nil
	})
	if err != nil {
		return nil, truncated, err
	}

	return results, truncated, nil
}

func shouldSkip(path string, ignorePatterns []string) bool {
	base := filepath.Base(path)

	if base != "." && strings.HasPrefix(base, ".") {
		return true
	}

	commonIgnored := []string{
		"__pycache__",
		"node_modules",
		"dist",
		"build",
		"target",
		"vendor",
		"bin",
		"obj",
		".git",
		".idea",
		".vscode",
		".DS_Store",
		"*.pyc",
		"*.pyo",
		"*.pyd",
		"*.so",
		"*.dll",
		"*.exe",
		"conversation.json",
	}

	if strings.Contains(path, filepath.Join("__pycache__", "")) {
		return true
	}

	for _, ignored := range commonIgnored {
		if strings.HasSuffix(ignored, "/") {
			if strings.Contains(path, filepath.Join(ignored[:len(ignored)-1], "")) {
				return true
			}
		} else if strings.HasPrefix(ignored, "*.") {
			if strings.HasSuffix(base, ignored[1:]) {
				return true
			}
		} else {
			if base == ignored {
				return true
			}
		}
	}

	for _, pattern := range ignorePatterns {
		matched, err := filepath.Match(pattern, base)
		if err == nil && matched {
			return true
		}
	}

	return false
}

func createFileTree(sortedPaths []string) []*TreeNode {
	root := []*TreeNode{}
	pathMap := make(map[string]*TreeNode)

	for _, path := range sortedPaths {
		parts := strings.Split(path, string(filepath.Separator))
		currentPath := ""
		var parentPath string

		var cleanParts []string
		for _, part := range parts {
			if part != "" {
				cleanParts = append(cleanParts, part)
			}
		}
		parts = cleanParts

		if len(parts) == 0 {
			continue
		}

		for i, part := range parts {
			if currentPath == "" {
				currentPath = part
			} else {
				currentPath = filepath.Join(currentPath, part)
			}

			if _, exists := pathMap[currentPath]; exists {
				parentPath = currentPath
				continue
			}

			isLastPart := i == len(parts)-1
			isDir := !isLastPart || strings.HasSuffix(path, string(filepath.Separator))
			nodeType := "file"
			if isDir {
				nodeType = "directory"
			}
			newNode := &TreeNode{
				Name:     part,
				Path:     currentPath,
				Type:     nodeType,
				Children: []*TreeNode{},
			}

			pathMap[currentPath] = newNode

			if i > 0 && parentPath != "" {
				if parent, ok := pathMap[parentPath]; ok {
					parent.Children = append(parent.Children, newNode)
				}
			} else {
				root = append(root, newNode)
			}

			parentPath = currentPath
		}
	}

	return root
}

func printTree(tree []*TreeNode, rootPath string) string {
	var result strings.Builder

	result.WriteString(fmt.Sprintf("- %s%s\n", rootPath, string(filepath.Separator)))

	for _, node := range tree {
		printNode(&result, node, 1)
	}

	return result.String()
}

func printNode(builder *strings.Builder, node *TreeNode, level int) {
	indent := strings.Repeat("  ", level)

	nodeName := node.Name
	if node.Type == "directory" {
		nodeName += string(filepath.Separator)
	}

	fmt.Fprintf(builder, "%s- %s\n", indent, nodeName)

	if node.Type == "directory" && len(node.Children) > 0 {
		for _, child := range node.Children {
			printNode(builder, child, level+1)
		}
	}
}
