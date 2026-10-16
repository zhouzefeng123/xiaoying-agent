package cognitive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/auramate/auramate/internal/llm/tools"
)

type contextKey string

const (
	ContextKeyAutonomous contextKey = "autonomous_mode"
)

// IsAutonomous checks if the current context is marked as autonomous execution
func IsAutonomous(ctx context.Context) bool {
	val, ok := ctx.Value(ContextKeyAutonomous).(bool)
	return ok && val
}

// ToolGuard wraps a BaseTool to enforce laws
type ToolGuard struct {
	tool   tools.BaseTool
	engine *Engine
}

func (g *ToolGuard) Info() tools.ToolInfo {
	return g.tool.Info()
}

func (g *ToolGuard) Run(ctx context.Context, call tools.ToolCall) (tools.ToolResponse, error) {
	// Law 3: Token Budget (Check before execution)
	// If budget is exhausted, we should probably deny execution to stop the loop
	// But only if in Autonomous mode? Or always?
	// Plan says "In Autonomous Mode... check DailyUsage".
	if IsAutonomous(ctx) {
		ok, err := g.engine.tokenBucket.CheckBudget(ctx)
		if err != nil {
			return tools.NewTextErrorResponse(fmt.Sprintf("failed to check token budget: %v", err)), nil
		}
		if !ok {
			return tools.NewTextErrorResponse("Daily token budget exceeded. Operations suspended."), nil
		}
	}

	// Law 2: Zero Intrusion (Autonomous Mode only)
	if IsAutonomous(ctx) {
		if err := g.checkIntrusion(call); err != nil {
			return tools.NewTextErrorResponse(fmt.Sprintf("Law 2 Violation: %v", err)), nil
		}
	}

	// Execute original tool
	return g.tool.Run(ctx, call)
}

func (g *ToolGuard) checkIntrusion(call tools.ToolCall) error {
	name := g.tool.Info().Name

	// Allow read-only tools
	safeTools := []string{
		"ls", "glob", "read_file", "view", "grep", "repo_map",
		"daily_info", "scan_desktop", "fetch", "web_search",
		"manage_core_memory", // Internal memory is safe
	}
	for _, safe := range safeTools {
		if name == safe {
			return nil
		}
	}

	// Dangerous tools: write_file, edit_file, delete_file, run_command, etc.
	// Allow if target is in memory_scratchpad OR creative_zone
	if name == "write_file" || name == "edit_file" || name == "delete_file" {
		// Parse params to check path
		// We need to peek into JSON input.
		var params struct {
			FilePath  string   `json:"file_path"`
			FilePaths []string `json:"file_paths"`
		}
		if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
			return fmt.Errorf("failed to parse params for safety check")
		}

		paths := []string{}
		if params.FilePath != "" {
			paths = append(paths, params.FilePath)
		}
		paths = append(paths, params.FilePaths...)

		for _, p := range paths {
			// Normalize path separators
			p = strings.ReplaceAll(p, "\\", "/")
			// Allow standard user directories if they match specific patterns
			// We trust the OS permissions if the app is running as Admin, but we still want to prevent
			// accidental system file modification.
			// Allowed:
			// 1. workspace/memory_scratchpad/creative_zone (Sandbox)
			// 2. Desktop (User Request)
			// 3. Downloads (User Request)
			// 4. Documents (Common Use)
			
			lowerP := strings.ToLower(p)
			if strings.Contains(lowerP, "memory_scratchpad") || 
			   strings.Contains(lowerP, "creative_zone") ||
			   strings.Contains(lowerP, "/desktop/") || strings.Contains(lowerP, "/downloads/") || strings.Contains(lowerP, "/documents/") {
				continue
			}
			
			// Fallback: If path is absolute and contains "Users", allow it?
			// This is risky but "Autonomous Mode" implies some trust if we have guards elsewhere.
			// For now, stick to specific folders.
			
			return fmt.Errorf("autonomous write/delete restricted to 'memory_scratchpad', 'creative_zone', 'Desktop', 'Downloads' or 'Documents': %s", p)
		}
		return nil
	}

	// Block others by default in autonomous mode
	return fmt.Errorf("tool '%s' is disabled in autonomous mode", name)
}

// WrapToolsWithGuard wraps a list of tools with the guard
func WrapToolsWithGuard(rawTools []tools.BaseTool, engine *Engine) []tools.BaseTool {
	wrapped := make([]tools.BaseTool, len(rawTools))
	for i, t := range rawTools {
		wrapped[i] = &ToolGuard{
			tool:   t,
			engine: engine,
		}
	}
	return wrapped
}
