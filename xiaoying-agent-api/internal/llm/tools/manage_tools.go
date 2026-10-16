package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ManageToolsToolName    = "manage_tools"
	manageToolsDescription = `Manages available tools and skills.
Usage:
- action: "load", "list", "inspect".
- tool_name: Name of tool (required for load/inspect). Please use the exact name from the 'list' output.
Actions:
- list: Shows unloaded tools.
- load: Activates a tool.
- inspect: Shows tool documentation.
`
)

type ManageToolsParams struct {
	Action   string `json:"action"` // "load", "list", "inspect"
	ToolName string `json:"tool_name,omitempty"`
}

type ToolRegistry interface {
	LoadTool(name string) error
	GetTool(name string) (BaseTool, bool)
	ListAvailableTools() []ToolInfo
	InspectTool(name string) (string, error)
	IsLoaded(name string) bool
}

type SessionScopedToolRegistry interface {
	LoadToolForSession(sessionID, name string) error
	ListAvailableToolsForSession(sessionID string) []ToolInfo
	InspectToolForSession(sessionID, name string) (string, error)
	IsLoadedForSession(sessionID, name string) bool
}

type manageToolsTool struct {
	registry ToolRegistry
}

func NewManageToolsTool(registry ToolRegistry) BaseTool {
	return &manageToolsTool{
		registry: registry,
	}
}

func (t *manageToolsTool) Info() ToolInfo {
	return ToolInfo{
		Name:        ManageToolsToolName,
		Description: manageToolsDescription,
		Parameters: map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "The action to perform: 'load' (activate a tool), 'list' (show available unloaded tools), 'inspect' (show detailed documentation)",
				"enum":        []string{"load", "list", "inspect"},
			},
			"tool_name": map[string]any{
				"type":        "string",
				"description": "The name of the tool to load or inspect (required for 'load' and 'inspect')",
			},
		},
		Required: []string{"action"},
	}
}

func (t *manageToolsTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var rawParams map[string]interface{}
	if err := json.Unmarshal([]byte(call.Input), &rawParams); err != nil {
		return NewTextErrorResponse("invalid parameters"), nil
	}

	sessionID, _ := GetContextValues(ctx)

	loadTool := t.registry.LoadTool
	listTools := t.registry.ListAvailableTools
	inspectTool := t.registry.InspectTool
	isLoaded := t.registry.IsLoaded
	if scopedRegistry, ok := t.registry.(SessionScopedToolRegistry); ok && sessionID != "" {
		loadTool = func(name string) error { return scopedRegistry.LoadToolForSession(sessionID, name) }
		listTools = func() []ToolInfo { return scopedRegistry.ListAvailableToolsForSession(sessionID) }
		inspectTool = func(name string) (string, error) { return scopedRegistry.InspectToolForSession(sessionID, name) }
		isLoaded = func(name string) bool { return scopedRegistry.IsLoadedForSession(sessionID, name) }
	}

	action, _ := rawParams["action"].(string)
	toolName, _ := rawParams["tool_name"].(string)

	// 1. 参数归一化与别名兼容
	if toolName == "" {
		if name, ok := rawParams["name"].(string); ok {
			toolName = name
		} else if tool, ok := rawParams["tool"].(string); ok {
			toolName = tool
		}
	}

	// 2. 字符串清理与格式化
	toolName = strings.TrimSpace(toolName)

	switch action {
	case "load":
		if toolName == "" {
			return NewTextErrorResponse("tool_name is required for load action"), nil
		}
		if isLoaded(toolName) {
			return NewTextResponse(fmt.Sprintf("Tool '%s' is already loaded.", toolName)), nil
		}
		if err := loadTool(toolName); err != nil {
			// 3. 友好的错误提示与模糊匹配
			availableTools := listTools()
			var suggestions []string
			lowerToolName := strings.ToLower(toolName)

			for _, tool := range availableTools {
				lowerName := strings.ToLower(tool.Name)
				if strings.Contains(lowerName, lowerToolName) || strings.Contains(lowerToolName, lowerName) {
					suggestions = append(suggestions, tool.Name)
				}
			}

			if len(suggestions) > 0 {
				return NewTextErrorResponse(fmt.Sprintf("Failed to load tool '%s': %v. Did you mean one of these? %s", toolName, err, strings.Join(suggestions, ", "))), nil
			}
			return NewTextErrorResponse(fmt.Sprintf("Failed to load tool '%s': %v. Use action 'list' to see available tools.", toolName, err)), nil
		}
		return NewTextResponse(fmt.Sprintf("Tool '%s' successfully loaded. You can now use it.", toolName)), nil

	case "inspect":
		if toolName == "" {
			return NewTextErrorResponse("tool_name is required for inspect action"), nil
		}

		info, err := inspectTool(toolName)
		if err != nil {
			// 3. 友好的错误提示与模糊匹配
			availableTools := listTools()
			var suggestions []string
			lowerToolName := strings.ToLower(toolName)

			for _, tool := range availableTools {
				lowerName := strings.ToLower(tool.Name)
				if strings.Contains(lowerName, lowerToolName) || strings.Contains(lowerToolName, lowerName) {
					suggestions = append(suggestions, tool.Name)
				}
			}

			if len(suggestions) > 0 {
				return NewTextErrorResponse(fmt.Sprintf("Failed to inspect tool '%s': %v. Did you mean one of these? %s", toolName, err, strings.Join(suggestions, ", "))), nil
			}
			return NewTextErrorResponse(fmt.Sprintf("Failed to inspect tool '%s': %v. Use action 'list' to see available tools.", toolName, err)), nil
		}

		// TODO: Upgrade agent.InspectTool to read SKILL.md for skills.

		return NewTextResponse(info), nil

	case "list":
		tools := listTools()
		var sb strings.Builder

		// 1. List Active Tools (Names only to save tokens)
		var activeTools []string
		var inactiveTools []ToolInfo

		for _, tool := range tools {
			if isLoaded(tool.Name) {
				activeTools = append(activeTools, tool.Name)
			} else {
				inactiveTools = append(inactiveTools, tool)
			}
		}

		if len(activeTools) > 0 {
			sb.WriteString("Currently Active Tools (Ready to use):\n")
			sb.WriteString(strings.Join(activeTools, ", "))
			sb.WriteString("\n\n")
		}

		// 2. List Unloaded Tools (Details - Truncated for Token Efficiency)
		if len(inactiveTools) > 0 {
			sb.WriteString("Available Unloaded Tools/Skills (Load with 'load' action):\n")
			// Highlight skill-generator if present
			for _, tool := range inactiveTools {
				if tool.Name == "skill-generator" {
					sb.WriteString(fmt.Sprintf("- **%s** (META SKILL): %s\n", tool.Name, "Create/Update other skills. USE THIS to create new tools."))
				}
			}
			for _, tool := range inactiveTools {
				if tool.Name == "skill-generator" {
					continue // Already shown
				}
				// Progressive Disclosure: Only show first line or first 200 chars of description
				desc := tool.Description
				if idx := strings.Index(desc, "\n"); idx != -1 {
					desc = desc[:idx]
				}
				if len(desc) > 200 {
					desc = desc[:197] + "..."
				}
				sb.WriteString(fmt.Sprintf("- **%s**: %s\n", tool.Name, desc))
			}
		} else {
			sb.WriteString("No unloaded tools available.\n")
		}

		return NewTextResponse(sb.String()), nil

	default:
		return NewTextErrorResponse("Invalid action. Must be 'load', 'list', or 'inspect'."), nil
	}
}
