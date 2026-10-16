package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/auramate/auramate/internal/config"
)

const (
	ManageMcpToolName    = "manage_mcp"
	manageMcpDescription = `Manages Model Context Protocol (MCP) servers configuration.
Use this tool to add, update, or remove external MCP servers in the dedicated MCP config file.
Actions:
- add: Add a new MCP server.
- update: Update an existing MCP server.
- remove: Remove an MCP server.
- list: List all configured MCP servers.

Changes are saved to .auramate.mcp.json and support hot reload.
`
)

type manageMcpTool struct{}

func NewManageMcpTool() BaseTool {
	return &manageMcpTool{}
}

func (t *manageMcpTool) Info() ToolInfo {
	return ToolInfo{
		Name:        ManageMcpToolName,
		Description: manageMcpDescription,
		Parameters: map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action to perform: 'add', 'update', 'remove', 'list'",
				"enum":        []string{"add", "update", "remove", "list"},
			},
			"server_id": map[string]any{
				"type":        "string",
				"description": "Unique identifier for the MCP server (e.g., 'python-tools', 'github-mcp'). Required for add, update, and remove.",
			},
			"type": map[string]any{
				"type":        "string",
				"description": "The connection type: 'stdio' or 'sse'. Required for add.",
				"enum":        []string{"stdio", "sse"},
			},
			"command": map[string]any{
				"type":        "string",
				"description": "The command to execute (e.g., 'npx', 'python'). Used when type is 'stdio'.",
			},
			"args": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Arguments to pass to the command. Used when type is 'stdio'.",
			},
			"env": map[string]any{
				"type": "object",
				"additionalProperties": map[string]any{
					"type": "string",
				},
				"description": "Environment variables (key-value pairs) for the server process. Used when type is 'stdio'.",
			},
			"url": map[string]any{
				"type":        "string",
				"description": "The SSE endpoint URL. Used when type is 'sse'.",
			},
			"disabled": map[string]any{
				"type":        "boolean",
				"description": "Set to true to disable this server from loading.",
			},
		},
		Required: []string{"action"},
	}
}

func (t *manageMcpTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params struct {
		Action   string            `json:"action"`
		ServerID string            `json:"server_id"`
		Type     string            `json:"type"`
		Command  string            `json:"command"`
		Args     []string          `json:"args"`
		Env      map[string]string `json:"env"`
		URL      string            `json:"url"`
		Disabled bool              `json:"disabled"`
	}

	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("failed to parse parameters: %v", err)), nil
	}

	cfg := config.Get()
	if cfg == nil {
		return NewTextErrorResponse("configuration not available"), nil
	}

	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]config.MCPServer)
	}
	mcpServers := make(map[string]config.MCPServer, len(cfg.MCPServers))
	for k, v := range cfg.MCPServers {
		mcpServers[k] = v
	}

	switch params.Action {
	case "list":
		bytes, _ := json.MarshalIndent(mcpServers, "", "  ")
		return NewTextResponse(fmt.Sprintf("Current MCP Servers:\n```json\n%s\n```", string(bytes))), nil

	case "remove":
		if params.ServerID == "" {
			return NewTextErrorResponse("server_id is required for remove action"), nil
		}
		if _, ok := mcpServers[params.ServerID]; !ok {
			return NewTextErrorResponse(fmt.Sprintf("server '%s' not found", params.ServerID)), nil
		}
		delete(mcpServers, params.ServerID)

	case "add", "update":
		if params.ServerID == "" {
			return NewTextErrorResponse("server_id is required for add/update action"), nil
		}
		if params.Action == "add" && params.Type == "" {
			return NewTextErrorResponse("type ('stdio' or 'sse') is required for add action"), nil
		}

		serverCfg := config.MCPServer{}
		if params.Action == "update" {
			existing, ok := mcpServers[params.ServerID]
			if !ok {
				return NewTextErrorResponse(fmt.Sprintf("server '%s' not found to update", params.ServerID)), nil
			}
			serverCfg = existing
		}

		if params.Type != "" {
			serverCfg.Type = config.MCPType(params.Type)
		}
		if params.Command != "" {
			serverCfg.Command = params.Command
		}
		if len(params.Args) > 0 {
			serverCfg.Args = params.Args
		}
		if len(params.Env) > 0 {
			serverCfg.Env = params.Env
		}
		if params.URL != "" {
			serverCfg.URL = params.URL
		}

		var inputMap map[string]interface{}
		_ = json.Unmarshal([]byte(call.Input), &inputMap)
		if _, ok := inputMap["disabled"]; ok {
			serverCfg.Disabled = params.Disabled
		}
		if serverCfg.Type == "" {
			serverCfg.Type = config.MCPStdio
		}

		mcpServers[params.ServerID] = serverCfg

	default:
		return NewTextErrorResponse("invalid action"), nil
	}

	if err := config.SaveMCPServers(mcpServers); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("failed to save MCP config file: %v", err)), nil
	}

	return NewTextResponse(fmt.Sprintf("Successfully executed '%s' for MCP server '%s'. Changes saved to .auramate.mcp.json and will be hot-reloaded.", params.Action, params.ServerID)), nil
}
