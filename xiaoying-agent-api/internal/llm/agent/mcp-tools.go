package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/mcp"
	"github.com/auramate/auramate/internal/permission"

	mcp_protocol "github.com/mark3labs/mcp-go/mcp"
)

var (
	mcpToolsDiscoveryTimeout = 10 * time.Second
	mcpToolsCacheTTL         = 30 * time.Second
	mcpToolsCacheMu          sync.Mutex
	mcpToolsCacheAt          time.Time
	mcpToolsCache            []tools.BaseTool

	runtimeMCPDepsMu      sync.RWMutex
	runtimeMCPPermissions permission.Service
	runtimeMCPService     mcp.Service
)

type mcpTool struct {
	mcpName       string
	tool          mcp_protocol.Tool
	mcpService    mcp.Service
	permissions   permission.Service
	sanitizedName string
}

type mcpAliasTool struct {
	alias  string
	target *mcpTool
}

var toolNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeMCPToolName(serverName, toolName string) string {
	base := strings.TrimSpace(serverName) + "_" + strings.TrimSpace(toolName)
	base = strings.ReplaceAll(base, " ", "_")
	base = toolNameSanitizer.ReplaceAllString(base, "_")
	base = strings.Trim(base, "_")
	if base == "" {
		return "mcp_tool"
	}
	if len(base) > 64 {
		base = base[:64]
	}
	return base
}

func (b *mcpTool) Info() tools.ToolInfo {
	required := b.tool.InputSchema.Required
	if required == nil {
		required = make([]string, 0)
	}
	return tools.ToolInfo{
		Name:        b.sanitizedName,
		Description: b.tool.Description,
		Parameters:  b.tool.InputSchema.Properties,
		Required:    required,
	}
}

func (b *mcpTool) mcpServerName() string {
	return b.mcpName
}

func (b *mcpTool) mcpOriginalToolName() string {
	return b.tool.Name
}

func (b *mcpTool) mcpCanonicalToolName() string {
	return b.sanitizedName
}

func (b *mcpAliasTool) Info() tools.ToolInfo {
	info := b.target.Info()
	info.Name = b.alias
	info.Description = fmt.Sprintf("%s\nAlias for MCP tool %s.", info.Description, b.target.Info().Name)
	return info
}

func (b *mcpAliasTool) Run(ctx context.Context, params tools.ToolCall) (tools.ToolResponse, error) {
	return b.target.Run(ctx, params)
}

func (b *mcpAliasTool) mcpServerName() string {
	return b.target.mcpServerName()
}

func (b *mcpAliasTool) mcpOriginalToolName() string {
	return b.target.mcpOriginalToolName()
}

func (b *mcpAliasTool) mcpCanonicalToolName() string {
	return b.target.mcpCanonicalToolName()
}

func (b *mcpTool) Run(ctx context.Context, params tools.ToolCall) (tools.ToolResponse, error) {
	sessionID, messageID := tools.GetContextValues(ctx)
	if sessionID == "" || messageID == "" {
		return tools.ToolResponse{}, fmt.Errorf("session ID and message ID are required for creating a new file")
	}
	permissionDescription := fmt.Sprintf("execute %s with the following parameters: %s", b.Info().Name, params.Input)
	p := b.permissions.Request(
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        config.WorkingDirectory(),
			ToolName:    b.Info().Name,
			Action:      "execute",
			Description: permissionDescription,
			Params:      params.Input,
		},
	)
	if !p {
		return tools.NewTextErrorResponse("permission denied"), nil
	}

	c, err := b.mcpService.GetClient(ctx, b.mcpName)
	if err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("failed to get mcp client: %v", err)), nil
	}
	// Note: We do NOT close the client here, as it is managed by the service.

	toolRequest := mcp_protocol.CallToolRequest{}
	toolRequest.Params.Name = b.tool.Name
	var args map[string]any
	if err = json.Unmarshal([]byte(params.Input), &args); err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}
	toolRequest.Params.Arguments = args

	logging.Info("Calling MCP tool", "server", b.mcpName, "tool", b.tool.Name, "args", args)
	result, err := c.CallTool(ctx, toolRequest)
	if err != nil && isMCPSessionNotFoundError(err) {
		logging.Warn("MCP tool call lost session; reconnecting and retrying", "server", b.mcpName, "tool", b.tool.Name, "error", err)
		b.mcpService.CloseAll()
		c, err = b.mcpService.GetClient(ctx, b.mcpName)
		if err == nil {
			result, err = c.CallTool(ctx, toolRequest)
		}
	}
	if err != nil {
		logging.Error("MCP tool call failed", "server", b.mcpName, "tool", b.tool.Name, "error", err)
		return tools.NewTextErrorResponse(err.Error()), nil
	}
	logging.Info("MCP tool call success", "server", b.mcpName, "tool", b.tool.Name)

	output := ""
	for _, v := range result.Content {
		if v, ok := v.(mcp_protocol.TextContent); ok {
			output += v.Text
		} else {
			output += fmt.Sprintf("%v", v)
		}
	}

	return tools.NewTextResponse(output), nil
}

func isMCPSessionNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "could not find session") || strings.Contains(message, "session not found")
}

func NewMcpTool(name string, tool mcp_protocol.Tool, permissions permission.Service, mcpService mcp.Service) tools.BaseTool {
	sanitized := sanitizeMCPToolName(name, tool.Name)
	return &mcpTool{
		mcpName:       name,
		tool:          tool,
		mcpService:    mcpService,
		permissions:   permissions,
		sanitizedName: sanitized,
	}
}

func newMCPAliasTool(alias string, target *mcpTool) tools.BaseTool {
	return &mcpAliasTool{alias: alias, target: target}
}

func buildMCPToolAliases(serverName string, tool mcp_protocol.Tool, canonicalName string) []string {
	seen := make(map[string]struct{})
	var aliases []string
	add := func(alias string) {
		alias = sanitizeMCPToolName("", alias)
		if alias == "" || alias == canonicalName {
			return
		}
		if _, ok := seen[alias]; ok {
			return
		}
		seen[alias] = struct{}{}
		aliases = append(aliases, alias)
	}

	add(tool.Name)
	if strings.Contains(strings.ToLower(tool.Description), "crm") && !strings.HasPrefix(strings.ToLower(tool.Name), "crm_") {
		add("crm_" + tool.Name)
	}
	if strings.TrimSpace(serverName) != "" {
		add(serverName + "_" + tool.Name)
	}
	return aliases
}

func GetMcpTools(ctx context.Context, permissions permission.Service, mcpService mcp.Service) []tools.BaseTool {
	var mcpTools []tools.BaseTool

	if mcpService == nil {
		return mcpTools
	}

	mcpToolsCacheMu.Lock()
	if len(mcpToolsCache) > 0 && time.Since(mcpToolsCacheAt) < mcpToolsCacheTTL {
		cached := append([]tools.BaseTool(nil), mcpToolsCache...)
		mcpToolsCacheMu.Unlock()
		return cached
	}
	if len(mcpToolsCache) == 0 && !mcpToolsCacheAt.IsZero() && time.Since(mcpToolsCacheAt) < mcpToolsCacheTTL {
		mcpToolsCacheMu.Unlock()
		return mcpTools
	}
	mcpToolsCacheMu.Unlock()

	discoveryCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		discoveryCtx, cancel = context.WithTimeout(ctx, mcpToolsDiscoveryTimeout)
		defer cancel()
	}

	toolsMap, err := mcpService.GetTools(discoveryCtx)
	if err != nil {
		logging.Error("Failed to get MCP tools", "error", err)
		mcpToolsCacheMu.Lock()
		mcpToolsCache = nil
		mcpToolsCacheAt = time.Time{}
		mcpToolsCacheMu.Unlock()
		return mcpTools
	}
	if len(toolsMap) == 0 {
		logging.Warn("MCP discovery returned no tools; skipping empty cache so the next request can retry")
		mcpToolsCacheMu.Lock()
		mcpToolsCache = nil
		mcpToolsCacheAt = time.Time{}
		mcpToolsCacheMu.Unlock()
		return mcpTools
	}

	type discoveredMCPTool struct {
		primary *mcpTool
		aliases []string
	}
	var discovered []discoveredMCPTool
	aliasCounts := make(map[string]int)

	for serverName, toolsList := range toolsMap {
		for _, t := range toolsList {
			tool := NewMcpTool(serverName, t, permissions, mcpService).(*mcpTool)
			aliases := buildMCPToolAliases(serverName, t, tool.Info().Name)
			for _, alias := range aliases {
				aliasCounts[alias]++
			}
			discovered = append(discovered, discoveredMCPTool{primary: tool, aliases: aliases})
		}
	}

	for _, item := range discovered {
		mcpTools = append(mcpTools, item.primary)
		logging.Info("Registered MCP tool", "server", item.primary.mcpServerName(), "original", item.primary.mcpOriginalToolName(), "sanitized", item.primary.Info().Name)
		for _, alias := range item.aliases {
			if aliasCounts[alias] != 1 {
				continue
			}
			aliasTool := newMCPAliasTool(alias, item.primary)
			mcpTools = append(mcpTools, aliasTool)
			logging.Info("Registered MCP tool alias", "server", item.primary.mcpServerName(), "original", item.primary.mcpOriginalToolName(), "alias", alias)
		}
	}

	logging.Info("MCP tools loaded", "count", len(mcpTools))
	mcpToolsCacheMu.Lock()
	mcpToolsCache = append([]tools.BaseTool(nil), mcpTools...)
	mcpToolsCacheAt = time.Now()
	mcpToolsCacheMu.Unlock()
	return mcpTools
}

func SetRuntimeMCPToolsDependencies(permissions permission.Service, mcpService mcp.Service) {
	runtimeMCPDepsMu.Lock()
	defer runtimeMCPDepsMu.Unlock()
	runtimeMCPPermissions = permissions
	runtimeMCPService = mcpService
}

func GetRuntimeMCPTools(ctx context.Context) []tools.BaseTool {
	runtimeMCPDepsMu.RLock()
	permissions := runtimeMCPPermissions
	mcpService := runtimeMCPService
	runtimeMCPDepsMu.RUnlock()
	return GetMcpTools(ctx, permissions, mcpService)
}
