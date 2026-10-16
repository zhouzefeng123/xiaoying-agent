package agent

import (
	"context"
	"database/sql"
	"runtime"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/history"
	"github.com/auramate/auramate/internal/llm/embedding"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/llm/vision"
	"github.com/auramate/auramate/internal/lsp"

	"github.com/auramate/auramate/internal/mcp"
	"github.com/auramate/auramate/internal/memory"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/permission"
	"github.com/auramate/auramate/internal/session"
	"github.com/auramate/auramate/internal/skills"
)

func CoderAgentTools(
	permissions permission.Service,
	sessions session.Service,
	messages message.Service,
	memories memory.Service,
	history history.Service,
	lspRegistry *lsp.Registry,
	mcpService mcp.Service,
	skillsManager *skills.Manager,
	q *db.Queries,
	conn *sql.DB,
	cfg *config.Config,
	orchestrator *Orchestrator,
	enableSubagents bool,
	embedder embedding.Embedder,
) []tools.BaseTool {
	ctx := context.Background()

	otherTools := GetMcpTools(ctx, permissions, mcpService)

	baseTools := []tools.BaseTool{
		tools.NewEditTool(lspRegistry, permissions, history),
		tools.NewFetchTool(permissions),
		// tools.NewGlobTool(), // Deprecated
		tools.NewGrepTool(),
		tools.NewLsTool(),
		tools.NewRepoMapTool(),
		tools.NewSourcegraphTool(),
		tools.NewReadTool(), // Added ReadTool
		tools.NewWordReaderTool(),
		tools.NewWordCopilotTool(),
		tools.NewPatchTool(lspRegistry, permissions, history),
		tools.NewWriteTool(lspRegistry, permissions, history),
		tools.NewBatchWriteTool(), // Added BatchWriteTool
		tools.NewDeleteTool(permissions),
		// tools.NewWriteScriptTool(permissions), // Deprecated
		// tools.NewRunScriptTool(permissions), // Deprecated
		tools.NewScheduleTaskTool(q, conn),
		tools.NewManageScheduleTool(q, conn),
		tools.NewDailyInfoTool(permissions),
		tools.NewReloadEnvTool(),
		tools.NewRunCommandTool(permissions),
		tools.NewCheckCommandStatusTool(),
		tools.NewStopCommandTool(),
		tools.NewMemoryTool(memories),
		tools.NewManagePlanTool(memories),
		tools.NewFileHistoryTool(history),      // Added FileHistoryTool
		tools.NewCleanupTool(),                 // Added CleanupTool
		tools.NewDownloadFileTool(permissions), // Added DownloadFileTool
		tools.NewLookupSymbolTool(lspRegistry), // Added LookupSymbolTool
		tools.NewDiagnosticsTool(lspRegistry),
		tools.NewManageMcpTool(),   // Added Manage MCP Tool
		tools.NewAppLauncherTool(), // 应用启动器
	}

	if enableSubagents {
		baseTools = append(baseTools, NewAgentTool(sessions, messages, memories, permissions, lspRegistry, q, conn, skillsManager, embedder))
	}

	if enableSubagents && orchestrator != nil {
		wd := ""
		if cfg != nil {
			wd = cfg.WorkingDir
		}
		baseTools = append(baseTools, NewDelegateTaskTool(orchestrator, wd))
		baseTools = append(baseTools, NewBatchDelegateTaskTool(orchestrator, wd))
		baseTools = append(baseTools, NewManageWorkerTool(orchestrator))
		baseTools = append(baseTools, NewWaitForWorkersTool(orchestrator))
	}

	// Add Visual Analysis Tool
	if cfg != nil {
		if analyzer, err := vision.NewAnalyzer(""); err == nil {
			if vaTool, err := tools.NewVisualAnalysisTool(analyzer); err == nil {
				baseTools = append(baseTools, vaTool)
			}
		}
	}

	// Add user skills
	if skillsManager != nil {
		// Use skill manager as a registry for dynamic tool loading
		baseTools = append(baseTools, tools.NewManageToolsTool(skillsManager))
	}

	// Add other tools (MCP, Diagnostics)
	// Only add bash tool on non-Windows platforms
	if runtime.GOOS != "windows" {
		baseTools = append([]tools.BaseTool{tools.NewBashTool(permissions)}, baseTools...)
	}

	return append(baseTools, otherTools...)
}

func NormalAgentTools(
	sessions session.Service,
	messages message.Service,
	q *db.Queries,
	conn *sql.DB,
	memories memory.Service,
	permissions permission.Service,
	lspRegistry *lsp.Registry,
	history history.Service,
	skillsManager *skills.Manager,
	cfg *config.Config,
	orchestrator *Orchestrator,
	mcpService mcp.Service,
	enableSubagents bool,
	embedder embedding.Embedder,
) []tools.BaseTool {
	ctx := context.Background()
	otherTools := GetMcpTools(ctx, permissions, mcpService)

	// UNIFIED: Normal Agent now has the SAME tools as Coder Agent
	baseTools := []tools.BaseTool{
		tools.NewEditTool(lspRegistry, permissions, history),
		tools.NewFetchTool(permissions),
		// tools.NewGlobTool(),
		tools.NewGrepTool(),
		tools.NewLsTool(),
		tools.NewRepoMapTool(),
		tools.NewSourcegraphTool(),
		tools.NewReadTool(),
		tools.NewWordReaderTool(),
		tools.NewWordCopilotTool(),
		tools.NewPatchTool(lspRegistry, permissions, history),
		tools.NewWriteTool(lspRegistry, permissions, history),
		tools.NewBatchWriteTool(),
		tools.NewDeleteTool(permissions),
		// tools.NewWriteScriptTool(permissions),
		// tools.NewRunScriptTool(permissions),
		tools.NewMemoryTool(memories),
		tools.NewScheduleTaskTool(q, conn),
		tools.NewManageScheduleTool(q, conn),
		tools.NewDailyInfoTool(permissions),
		tools.NewReloadEnvTool(),
		tools.NewRunCommandTool(permissions),
		tools.NewCheckCommandStatusTool(),
		tools.NewStopCommandTool(),
		tools.NewManagePlanTool(memories),
		tools.NewFileHistoryTool(history),      // Added FileHistoryTool
		tools.NewDownloadFileTool(permissions), // Added DownloadFileTool
		tools.NewCleanupTool(),                 // Added CleanupTool
		tools.NewLookupSymbolTool(lspRegistry), // Added LookupSymbolTool
		tools.NewDiagnosticsTool(lspRegistry),
		tools.NewManageMcpTool(),   // Added Manage MCP Tool
		tools.NewAppLauncherTool(), // 应用启动器
	}

	if enableSubagents {
		baseTools = append(baseTools, NewAgentTool(sessions, messages, memories, permissions, lspRegistry, q, conn, skillsManager, embedder))
	}

	if enableSubagents && orchestrator != nil {
		wd := ""
		if cfg != nil {
			wd = cfg.WorkingDir
		}
		baseTools = append(baseTools, NewDelegateTaskTool(orchestrator, wd))
		baseTools = append(baseTools, NewBatchDelegateTaskTool(orchestrator, wd))
		baseTools = append(baseTools, NewManageWorkerTool(orchestrator))
		baseTools = append(baseTools, NewWaitForWorkersTool(orchestrator))
	}

	// Add Visual Analysis Tool
	if cfg != nil {
		if analyzer, err := vision.NewAnalyzer(""); err == nil {
			if vaTool, err := tools.NewVisualAnalysisTool(analyzer); err == nil {
				baseTools = append(baseTools, vaTool)
			}
		}
	}

	// Add user skills
	if skillsManager != nil {
		baseTools = append(baseTools, tools.NewManageToolsTool(skillsManager))
	}

	// Add MCP tools
	if len(otherTools) > 0 {
		baseTools = append(baseTools, otherTools...)
	}

	if runtime.GOOS != "windows" {
		baseTools = append([]tools.BaseTool{tools.NewBashTool(permissions)}, baseTools...)
	}
	return baseTools
}

func TaskAgentTools(q *db.Queries, conn *sql.DB, memories memory.Service, permissions permission.Service, lspRegistry *lsp.Registry, skillsManager *skills.Manager, mcpService mcp.Service, embedder embedding.Embedder) []tools.BaseTool {
	// UNIFIED: Task Agent (Sub-Agent) now also has FULL capabilities
	// It mirrors the toolset of Normal/Coder Agent but without the session/message services which aren't passed to this constructor.
	// This ensures sub-agents don't fail when trying to do complex tasks like editing files.

	// Note: We use a local history service here since one isn't passed in.
	// Task Agents are transient, so a fresh history service is acceptable,
	// or we could update the signature to accept it.
	// For minimal disruption, we create a new History service.
	h := history.NewService(q, conn)
	ctx := context.Background()
	otherTools := GetMcpTools(ctx, permissions, mcpService)

	baseTools := []tools.BaseTool{
		tools.NewEditTool(lspRegistry, permissions, h), // Added Edit
		tools.NewFetchTool(permissions),
		// tools.NewGlobTool(),
		tools.NewGrepTool(),
		tools.NewLsTool(),
		tools.NewRepoMapTool(),
		tools.NewSourcegraphTool(),
		tools.NewReadTool(), // Added Read
		tools.NewWordReaderTool(),
		tools.NewWordCopilotTool(),
		tools.NewPatchTool(lspRegistry, permissions, h), // Added Patch
		tools.NewWriteTool(lspRegistry, permissions, h), // Added Write
		tools.NewBatchWriteTool(),
		tools.NewDeleteTool(permissions), // Added Delete
		// tools.NewWriteScriptTool(permissions),
		// tools.NewRunScriptTool(permissions),
		// AgentTool skipped (recursion limit / dependency missing)
		tools.NewMemoryTool(memories),
		tools.NewScheduleTaskTool(q, conn),
		tools.NewManageScheduleTool(q, conn), // Added ManageTasks
		tools.NewDailyInfoTool(permissions),
		tools.NewReloadEnvTool(),
		tools.NewRunCommandTool(permissions),
		tools.NewCheckCommandStatusTool(),
		tools.NewStopCommandTool(),
		tools.NewManagePlanTool(memories),
		// tools.NewSearchCodebaseTool(),          // Added SearchCodebase
		tools.NewDownloadFileTool(permissions), // Added DownloadFileTool
		tools.NewSendNotificationTool(),        // Added SendNotificationTool
		tools.NewDiagnosticsTool(lspRegistry),
		tools.NewManageMcpTool(),   // Added Manage MCP Tool
		tools.NewAppLauncherTool(), // 应用启动器
	}

	if skillsManager != nil {
		// Only expose manage_tools; skills load on-demand
		baseTools = append(baseTools, tools.NewManageToolsTool(skillsManager))
	}

	// Add MCP tools
	if len(otherTools) > 0 {
		baseTools = append(baseTools, otherTools...)
	}

	if runtime.GOOS != "windows" {
		baseTools = append([]tools.BaseTool{tools.NewBashTool(permissions)}, baseTools...)
	}

	return baseTools
}

func LifeAgentTools(
	sessions session.Service,
	messages message.Service,
	q *db.Queries,
	conn *sql.DB,
	memories memory.Service,
	permissions permission.Service,
	lspRegistry *lsp.Registry,
	history history.Service,
	skillsManager *skills.Manager,
	cfg *config.Config,
	mcpService mcp.Service,
	embedder embedding.Embedder,
) []tools.BaseTool {
	ctx := context.Background()
	otherTools := GetMcpTools(ctx, permissions, mcpService)

	baseTools := []tools.BaseTool{
		// UNIFIED: Life Agent now has the SAME tools as Normal/Coder Agent
		tools.NewEditTool(lspRegistry, permissions, history),
		tools.NewFetchTool(permissions),
		// tools.NewGlobTool(),
		tools.NewGrepTool(),
		tools.NewLsTool(),
		tools.NewRepoMapTool(),
		tools.NewReadTool(),
		tools.NewSourcegraphTool(),
		tools.NewWordReaderTool(),
		tools.NewWordCopilotTool(),
		tools.NewWriteTool(lspRegistry, permissions, history),
		tools.NewPatchTool(lspRegistry, permissions, history),
		tools.NewDeleteTool(permissions),
		tools.NewBatchWriteTool(),
		// tools.NewRunScriptTool(permissions),
		// tools.NewWriteScriptTool(permissions),
		NewAgentTool(sessions, messages, memories, permissions, lspRegistry, q, conn, skillsManager, embedder),
		tools.NewMemoryTool(memories),
		tools.NewScheduleTaskTool(q, conn),
		tools.NewManageScheduleTool(q, conn),
		tools.NewRunCommandTool(permissions),
		tools.NewDailyInfoTool(permissions),
		tools.NewReloadEnvTool(),
		tools.NewCheckCommandStatusTool(),
		tools.NewStopCommandTool(),
		// tools.NewSearchCodebaseTool(),
		tools.NewManagePlanTool(memories),
		tools.NewFileHistoryTool(history),
		tools.NewDownloadFileTool(permissions),
		tools.NewCleanupTool(),
		tools.NewLookupSymbolTool(lspRegistry),
		tools.NewDiagnosticsTool(lspRegistry),
		tools.NewSendNotificationTool(),
		tools.NewManageMcpTool(),
		tools.NewAppLauncherTool(), // 应用启动器
	}

	// Add Visual Analysis Tool
	if cfg != nil {
		if analyzer, err := vision.NewAnalyzer(""); err == nil {
			if vaTool, err := tools.NewVisualAnalysisTool(analyzer); err == nil {
				baseTools = append(baseTools, vaTool)
			}
		}
	}

	if skillsManager != nil {
		baseTools = append(baseTools, tools.NewManageToolsTool(skillsManager))
	}

	// Add MCP tools
	if len(otherTools) > 0 {
		baseTools = append(baseTools, otherTools...)
	}

	if runtime.GOOS != "windows" {
		baseTools = append([]tools.BaseTool{tools.NewBashTool(permissions)}, baseTools...)
	}

	return baseTools
}
