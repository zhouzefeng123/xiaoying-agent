package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auramate/auramate/internal/config"
)

const (
	RepoMapToolName    = "repo_map"
	repoMapDescription = `Generates a token-efficient map of the repository structure.
Use this tool FIRST when you need to understand the codebase layout, especially for large projects.
It prioritizes important files and limits depth to fit in context.
`
)

type RepoMapParams struct {
	Path     string   `json:"path"`
	MaxDepth int      `json:"max_depth"` // Default 3
	Ignore   []string `json:"ignore"`    // List of names to ignore
}

type repoMapTool struct{}

func NewRepoMapTool() BaseTool {
	return &repoMapTool{}
}

func (t *repoMapTool) Info() ToolInfo {
	return ToolInfo{
		Name:               RepoMapToolName,
		Description:        repoMapDescription,
		DisableAutoSummary: true,
		Parameters: map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Root directory (default: current working directory)",
			},
			"max_depth": map[string]any{
				"type":        "integer",
				"description": "Maximum depth to traverse (default: 3)",
			},
			"ignore": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "List of file/directory names to ignore",
			},
		},
	}
}

func (t *repoMapTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params RepoMapParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	searchPath := params.Path
	if searchPath == "" {
		searchPath = config.WorkingDirectoryFor(ctx)
	}

	if !filepath.IsAbs(searchPath) {
		searchPath = filepath.Join(config.WorkingDirectoryFor(ctx), searchPath)
	}

	if _, err := os.Stat(searchPath); os.IsNotExist(err) {
		return NewTextErrorResponse(fmt.Sprintf("path does not exist: %s", searchPath)), nil
	}

	maxDepth := params.MaxDepth
	if maxDepth == 0 {
		maxDepth = 3 // Default
	}

	tree, err := generateRepoTree(ctx, searchPath, maxDepth, params.Ignore)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("error generating repo map: %w", err)
	}

	return NewTextResponse(tree), nil
}

func generateRepoTree(ctx context.Context, root string, maxDepth int, userIgnore []string) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Repository Map (Depth: %d, Root: %s)\n", maxDepth, filepath.Base(root)))

	ignoreMap := make(map[string]bool)
	for _, name := range userIgnore {
		ignoreMap[name] = true
	}

	err := walkDir(ctx, root, "", 0, maxDepth, &sb, ignoreMap)
	if err != nil {
		return "", err
	}
	return sb.String(), nil
}

func walkDir(ctx context.Context, path string, prefix string, currentDepth int, maxDepth int, sb *strings.Builder, ignoreMap map[string]bool) error {
	if err := CheckCancellation(ctx); err != nil {
		return err
	}

	if currentDepth > maxDepth {
		return nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil // Skip unreadable
	}

	// Filter and Sort
	var dirs, files []os.DirEntry
	for _, e := range entries {
		if shouldIgnore(e.Name(), ignoreMap) {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}

	// Sort: Important files first
	sort.Slice(files, func(i, j int) bool {
		return rankFile(files[i].Name()) > rankFile(files[j].Name())
	})

	all := append(dirs, files...)
	count := len(all)
	limit := 50 // Hard limit per folder to avoid spam

	for i, entry := range all {
		if err := CheckCancellation(ctx); err != nil {
			return err
		}

		if i >= limit {
			sb.WriteString(fmt.Sprintf("%s└── ... (%d more items)\n", prefix, count-limit))
			break
		}

		isLast := i == count-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}

		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}

		sb.WriteString(fmt.Sprintf("%s%s%s\n", prefix, connector, name))

		if entry.IsDir() {
			newPrefix := prefix + "│   "
			if isLast {
				newPrefix = prefix + "    "
			}
			if err := walkDir(ctx, filepath.Join(path, entry.Name()), newPrefix, currentDepth+1, maxDepth, sb, ignoreMap); err != nil {
				return err
			}
		}
	}
	return nil
}

func shouldIgnore(name string, ignoreMap map[string]bool) bool {
	if ignoreMap[name] {
		return true
	}
	// Common large directories and build artifacts to skip entirely
	ignored := map[string]bool{
		"node_modules": true, "vendor": true, "target": true, "build": true, "dist": true,
		"bin": true, "obj": true, "__pycache__": true, ".git": true, ".idea": true,
		".vscode": true, ".next": true, ".nuxt": true, "out": true, "venv": true,
		".venv": true, "env": true, ".env": true, "coverage": true, "docs": true,
		"public": true, "static": true, "temp": true, "tmp": true,
	}
	if ignored[strings.ToLower(name)] {
		return true
	}

	// Skip hidden files/directories except for key project meta files
	if strings.HasPrefix(name, ".") && name != ".gitignore" && name != ".github" && name != ".trae" {
		return true
	}

	return false
}

func rankFile(name string) int {
	// Higher score = earlier in list
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "readme") {
		return 100
	}
	if name == "package.json" || name == "go.mod" || name == "requirements.txt" || name == "pom.xml" {
		return 90
	}
	if name == "main.go" || name == "index.js" || name == "app.py" {
		return 80
	}
	if strings.HasPrefix(name, "dockerfile") {
		return 70
	}
	return 0
}
