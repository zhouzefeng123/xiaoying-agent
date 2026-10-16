package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/embedding"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/lsp"
	"github.com/auramate/auramate/internal/memory"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/permission"
	"github.com/auramate/auramate/internal/session"
	"github.com/auramate/auramate/internal/skills"
)

type agentTool struct {
	sessions      session.Service
	messages      message.Service
	memories      memory.Service
	permissions   permission.Service
	lspRegistry   *lsp.Registry
	skillsManager *skills.Manager
	q             *db.Queries
	conn          *sql.DB
	embedder      embedding.Embedder
}

const (
	AgentToolName = "agent"
)

type AgentParams struct {
	Prompt string `json:"prompt"`
}

func (b *agentTool) Info() tools.ToolInfo {
	return tools.ToolInfo{
		Name:        AgentToolName,
		Description: fmt.Sprintf("Launch a fully capable Sub-Agent (Task Agent) to execute a complex task autonomously. The Sub-Agent has access to the FULL toolset (`%s`, `%s`, `%s`, `%s`, `%s`, etc.) and operates in an isolated session.\n\nUse this tool when:\n1. You have a complex sub-task that requires multiple steps (e.g., \"Refactor this entire module\", \"Research this topic and write a summary\", \"Fix this bug which involves multiple files\").\n2. You want to perform a task without polluting your main conversation history with intermediate steps.\n3. You need to verify something (like running a command) in a safe, isolated context.\n\nUsage notes:\n- The Sub-Agent will plan and EXECUTE the task autonomously.\n- It will return a FINAL REPORT to you when done.\n- You cannot interact with it while it runs; give it clear, comprehensive instructions in the 'prompt'.\n- It can read/write files and run commands just like you.", tools.EditToolName, tools.PatchToolName, tools.WriteToolName, tools.FetchToolName, tools.RunCommandToolName),
		Parameters: map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "The detailed task description for the Sub-Agent to execute. Be specific about goals and constraints.",
			},
		},
		Required: []string{"prompt"},
	}
}

func (b *agentTool) Run(ctx context.Context, call tools.ToolCall) (tools.ToolResponse, error) {
	var params AgentParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}
	if params.Prompt == "" {
		return tools.NewTextErrorResponse("prompt is required"), nil
	}

	sessionID, messageID := tools.GetContextValues(ctx)
	if sessionID == "" || messageID == "" {
		return tools.ToolResponse{}, fmt.Errorf("session_id and message_id are required")
	}

	agent, err := NewAgent(config.AgentTask, b.sessions, b.messages, b.memories, TaskAgentTools(b.q, b.conn, b.memories, b.permissions, b.lspRegistry, b.skillsManager, nil, b.embedder), b.q, b.skillsManager, nil, b.embedder)
	if err != nil {
		return tools.ToolResponse{}, fmt.Errorf("error creating agent: %s", err)
	}

	session, err := b.sessions.CreateTaskSession(ctx, call.ID, sessionID, "New Agent Session")
	if err != nil {
		return tools.ToolResponse{}, fmt.Errorf("error creating session: %s", err)
	}

	done, err := agent.Run(ctx, session.ID, params.Prompt, "coding")
	if err != nil {
		return tools.ToolResponse{}, fmt.Errorf("error generating agent: %s", err)
	}
	result := <-done
	if result.Error != nil {
		return tools.ToolResponse{}, fmt.Errorf("error generating agent: %s", result.Error)
	}

	response := result.Message
	if response.Role != message.Assistant {
		return tools.NewTextErrorResponse("no response"), nil
	}

	updatedSession, err := b.sessions.Get(ctx, session.ID)
	if err != nil {
		return tools.ToolResponse{}, fmt.Errorf("error getting session: %s", err)
	}
	parentSession, err := b.sessions.Get(ctx, sessionID)
	if err != nil {
		return tools.ToolResponse{}, fmt.Errorf("error getting parent session: %s", err)
	}

	parentSession.Cost += updatedSession.Cost

	_, err = b.sessions.Save(ctx, parentSession)
	if err != nil {
		return tools.ToolResponse{}, fmt.Errorf("error saving parent session: %s", err)
	}
	return tools.NewTextResponse(response.Content().String()), nil
}

func NewAgentTool(
	Sessions session.Service,
	Messages message.Service,
	Memories memory.Service,
	Permissions permission.Service,
	LSPRegistry *lsp.Registry,
	q *db.Queries,
	conn *sql.DB,
	skillsManager *skills.Manager,
	embedder embedding.Embedder,
) tools.BaseTool {
	return &agentTool{
		sessions:      Sessions,
		messages:      Messages,
		memories:      Memories,
		permissions:   Permissions,
		lspRegistry:   LSPRegistry,
		skillsManager: skillsManager,
		q:             q,
		conn:          conn,
		embedder:      embedder,
	}
}
