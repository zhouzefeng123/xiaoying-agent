package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/auramate/auramate/internal/message"
)

func (a *agent) scanForCodeIssues(toolCalls []message.ToolCall) string {
	for _, tc := range toolCalls {
		name := strings.ToLower(tc.Name)
		if name == "write_file" || name == "write" {
			var args struct {
				Content  string `json:"content"`
				FilePath string `json:"file_path"`
			}
			// Try to unmarshal, ignore errors as we can't do anything if it fails
			if err := json.Unmarshal([]byte(tc.Input), &args); err != nil {
				continue
			}

			// 1. Check for 'pass' in Python files
			if strings.HasSuffix(args.FilePath, ".py") {
				lines := strings.Split(args.Content, "\n")
				for i, line := range lines {
					if strings.TrimSpace(line) == "pass" {
						return fmt.Sprintf("CRITICAL QUALITY ISSUE: Detected 'pass' statement in '%s' at line %d. **RULE**: You must NOT leave 'pass' as a placeholder in critical logic (especially loops/locks). It causes deadlocks and incomplete implementation. **ACTION**: Replace 'pass' with actual logic or 'await asyncio.sleep()' if waiting.", args.FilePath, i+1)
					}
				}
			}

			// 2. Check for TODOs (heuristic)
			if strings.Contains(args.Content, "TODO") {
				return fmt.Sprintf("QUALITY ISSUE: Detected 'TODO' in '%s'. **RULE**: In this environment, you must implement the FULL solution. Do not leave TODOs.", args.FilePath)
			}
		}
	}
	return ""
}
