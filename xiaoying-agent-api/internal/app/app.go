package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/cognitive"
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/goword"
	"github.com/auramate/auramate/internal/history"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/embedding"
	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/llm/routing"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/lsp"
	"github.com/auramate/auramate/internal/mcp"
	"github.com/auramate/auramate/internal/memory"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/permission"
	"github.com/auramate/auramate/internal/scheduler"
	"github.com/auramate/auramate/internal/session"
	"github.com/auramate/auramate/internal/skills"
)

type App struct {
	Sessions     session.Service
	Messages     message.Service
	History      history.Service
	Permissions  permission.Service
	MCP          mcp.Service
	Memories     memory.Service
	Skills       *skills.Manager
	Scheduler    *scheduler.Scheduler
	Cognitive    *cognitive.Engine
	Orchestrator *agent.Orchestrator
	Config       *config.Config
	Queries      *db.Queries
	GoWord       *goword.Service

	CoderAgent             agent.Service
	CoderAgentNoSubagents  agent.Service
	LifeAgent              agent.Service
	NormalAgent            agent.Service
	NormalAgentNoSubagents agent.Service

	lspRootCtx  context.Context
	LSPRegistry *lsp.Registry

	watcherCancelFuncs []context.CancelFunc
	cancelFuncsMutex   sync.Mutex
	watcherWG          sync.WaitGroup
}

func New(ctx context.Context, conn *sql.DB) (*App, error) {
	cfg, err := config.Load("", false)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	q := db.New(conn)
	sessions := session.NewService(q)
	files := history.NewService(q, conn)

	// Initialize Embedder
	embedder, err := embedding.NewConfiguredEmbedder(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize embedder: %w", err)
	}
	if embedder != nil {
		if localEmbedder, ok := embedder.(*embedding.LocalCLIEmbedder); ok {
			logging.Info("Initialized local OpenVINO Embedder",
				"provider", embedding.FixedEmbeddingProvider,
				"model", embedding.FixedEmbeddingModel,
				"modelDir", localEmbedder.ModelDir(),
				"binaryPath", localEmbedder.BinaryPath())
		} else {
			logging.Info("Initialized embedder",
				"provider", embedding.FixedEmbeddingProvider,
				"model", embedding.FixedEmbeddingModel)
		}
	}

	messages := message.NewService(q, embedder)
	memories := memory.NewService(q, conn, embedder)

	app := &App{
		Sessions:    sessions,
		Messages:    messages,
		History:     files,
		Memories:    memories,
		Permissions: permission.NewPermissionService(),
		MCP:         mcp.NewService(),
		lspRootCtx:  ctx,
		LSPRegistry: lsp.NewRegistry(),
		Config:      cfg,
		Queries:     q,
		GoWord:      goword.NewService(cfg.WorkingDir),
	}
	agent.SetRuntimeMCPToolsDependencies(app.Permissions, app.MCP)
	tools.SetReadToolGoWordService(app.GoWord)
	tools.SetWordReaderToolGoWordService(app.GoWord)

	// Initialize Skills Manager
	// We no longer hardcode specific paths here as NewManager now has smart defaults
	// But we can pass extra paths if needed.
	// The skills.NewManager now automatically adds:
	// 1. .trae/skills (in user home/project)
	// 2. workspace/custom_skills
	app.Skills = skills.NewManager()

	// Initialize Multi-Agent Orchestrator
	// We need a worker factory that can create providers based on config
	workerFactory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []tools.BaseTool, error) {
		buildWorkerTools := func() []tools.BaseTool {
			return agent.TaskAgentTools(q, conn, app.Memories, app.Permissions, app.LSPRegistry, app.Skills, app.MCP, embedder)
		}

		explicitModel := strings.TrimSpace(os.Getenv("WORKER_MODEL"))
		if explicitModel != "" {
			p, _, err := routing.BuildProviderForAgent(config.AgentTask, explicitModel, systemPrompt, 0)
			if err != nil {
				return nil, nil, err
			}
			return p, buildWorkerTools(), nil
		}

		if candidateModels, ok := ctx.Value(tools.ModelOverrideCandidatesContextKey).([]string); ok && len(candidateModels) > 0 {
			agentCfg := cfg.Agents[config.AgentTask]
			p, _, err := routing.BuildProviderChain(config.AgentTask, candidateModels, systemPrompt, agentCfg.MaxTokens, agentCfg.ReasoningEffort)
			if err != nil {
				return nil, nil, err
			}
			return p, buildWorkerTools(), nil
		}

		if explicitRequestModel, ok := ctx.Value(tools.ModelOverrideContextKey).(string); ok {
			explicitRequestModel = strings.TrimSpace(explicitRequestModel)
			if explicitRequestModel != "" {
				agentCfg := cfg.Agents[config.AgentTask]
				p, _, err := routing.BuildProviderChain(config.AgentTask, []string{explicitRequestModel}, systemPrompt, agentCfg.MaxTokens, agentCfg.ReasoningEffort)
				if err != nil {
					return nil, nil, err
				}
				return p, buildWorkerTools(), nil
			}
		}

		if _, ok := cfg.Agents[config.AgentTask]; !ok {
			logging.Warn("AgentTask config not found. Relying on default unified routing chain.")
		}

		p, _, err := routing.BuildProviderForAgent(config.AgentTask, "", systemPrompt, 0)
		if err != nil {
			return nil, nil, err
		}
		return p, buildWorkerTools(), nil
	}

	app.Orchestrator = agent.NewOrchestrator(workerFactory, app.Sessions)
	if err := app.Orchestrator.Start(ctx); err != nil {
		logging.Error("Failed to start orchestrator", "error", err)
		return nil, err
	}

	app.Skills.StartWatching(ctx)

	// Initialize Scheduler
	app.Scheduler = scheduler.New(conn, sessions, messages, memories, app.Permissions, app.MCP, app.LSPRegistry, app.Skills, embedder)

	// Initialize Cognitive Engine
	app.Cognitive = cognitive.NewEngine(conn, memories)
	app.Cognitive.Start(ctx)

	// Wire Scheduler with Cognitive Engine for Auto-Reflection
	app.Scheduler.SetReflectionHandler(app.Cognitive.Reflect)

	// Start Scheduler after wiring
	app.Scheduler.Start()

	// Initialize LSP clients in the background
	go app.initLSPClients(ctx)

	// Create Coder Agent Tools
	coderTools := agent.CoderAgentTools(
		app.Permissions,
		app.Sessions,
		app.Messages,
		app.Memories,
		app.History,
		app.LSPRegistry,
		app.MCP,
		app.Skills,
		q,
		conn,
		app.Config,
		app.Orchestrator,
		true,
		embedder,
	)
	// Wrap tools with Cognitive Guard
	coderTools = cognitive.WrapToolsWithGuard(coderTools, app.Cognitive)

	app.CoderAgent, err = agent.NewAgent(
		config.AgentCoder,
		app.Sessions,
		app.Messages,
		app.Memories,
		coderTools,
		q,
		app.Skills,
		app.Orchestrator,
		embedder,
	)
	app.CoderAgent, err = createStartupAgent(config.AgentCoder, app.CoderAgent, err, func() (agent.Service, error) {
		return agent.NewAgent(
			config.AgentCoder,
			app.Sessions,
			app.Messages,
			app.Memories,
			coderTools,
			q,
			app.Skills,
			app.Orchestrator,
			embedder,
		)
	})
	if err != nil {
		logging.Error("Failed to create coder agent", err)
		return nil, err
	}

	coderToolsNoSubagents := agent.CoderAgentTools(
		app.Permissions,
		app.Sessions,
		app.Messages,
		app.Memories,
		app.History,
		app.LSPRegistry,
		app.MCP,
		app.Skills,
		q,
		conn,
		app.Config,
		nil,
		false,
		embedder,
	)
	coderToolsNoSubagents = cognitive.WrapToolsWithGuard(coderToolsNoSubagents, app.Cognitive)

	app.CoderAgentNoSubagents, err = agent.NewAgent(
		config.AgentCoder,
		app.Sessions,
		app.Messages,
		app.Memories,
		coderToolsNoSubagents,
		q,
		app.Skills,
		nil,
		embedder,
	)
	app.CoderAgentNoSubagents, err = createStartupAgent(config.AgentCoder, app.CoderAgentNoSubagents, err, func() (agent.Service, error) {
		return agent.NewAgent(
			config.AgentCoder,
			app.Sessions,
			app.Messages,
			app.Memories,
			coderToolsNoSubagents,
			q,
			app.Skills,
			nil,
			embedder,
		)
	})
	if err != nil {
		logging.Error("Failed to create coder agent without subagents", err)
		return nil, err
	}

	// Create Life Agent Tools (Memory Only + Self-Install Capabilities)
	lifeTools := agent.LifeAgentTools(
		app.Sessions,
		app.Messages,
		q,
		conn,
		app.Memories,
		app.Permissions,
		app.LSPRegistry,
		app.History,
		app.Skills,
		app.Config,
		app.MCP,
		embedder,
	)

	// Add Cognitive State Tool
	lifeTools = append(lifeTools, tools.NewManageCognitiveStateTool(app.Cognitive))

	app.LifeAgent, err = agent.NewAgent(
		config.AgentLife,
		app.Sessions,
		app.Messages,
		app.Memories,
		lifeTools,
		q,
		app.Skills,
		nil, // Life Agent typically doesn't spawn workers, but could. Passing nil for now or app.Orchestrator if we want to enable it.
		embedder,
	)
	app.LifeAgent, err = createStartupAgent(config.AgentLife, app.LifeAgent, err, func() (agent.Service, error) {
		return agent.NewAgent(
			config.AgentLife,
			app.Sessions,
			app.Messages,
			app.Memories,
			lifeTools,
			q,
			app.Skills,
			nil,
			embedder,
		)
	})
	if err != nil {
		logging.Error("Failed to create life agent", err)
		return nil, err
	}

	// Link Agent to Cognitive Engine
	// Use LifeAgent for internal cognitive processes (Reflection, Exploration, Dreaming)
	app.Cognitive.SetAgent(app.LifeAgent)

	// Create Normal Agent Tools (General purpose + Self-Install)
	normalTools := agent.NormalAgentTools(app.Sessions, app.Messages, q, conn, app.Memories, app.Permissions, app.LSPRegistry, app.History, app.Skills, app.Config, app.Orchestrator, app.MCP, true, embedder)
	app.NormalAgent, err = agent.NewAgent(
		config.AgentNormal,
		app.Sessions,
		app.Messages,
		app.Memories,
		normalTools,
		q,
		app.Skills,
		app.Orchestrator,
		embedder,
	)
	app.NormalAgent, err = createStartupAgent(config.AgentNormal, app.NormalAgent, err, func() (agent.Service, error) {
		return agent.NewAgent(
			config.AgentNormal,
			app.Sessions,
			app.Messages,
			app.Memories,
			normalTools,
			q,
			app.Skills,
			app.Orchestrator,
			embedder,
		)
	})
	if err != nil {
		logging.Error("Failed to create normal agent", err)
		return nil, err
	}

	normalToolsNoSubagents := agent.NormalAgentTools(app.Sessions, app.Messages, q, conn, app.Memories, app.Permissions, app.LSPRegistry, app.History, app.Skills, app.Config, nil, app.MCP, false, embedder)
	app.NormalAgentNoSubagents, err = agent.NewAgent(
		config.AgentNormal,
		app.Sessions,
		app.Messages,
		app.Memories,
		normalToolsNoSubagents,
		q,
		app.Skills,
		nil,
		embedder,
	)
	app.NormalAgentNoSubagents, err = createStartupAgent(config.AgentNormal, app.NormalAgentNoSubagents, err, func() (agent.Service, error) {
		return agent.NewAgent(
			config.AgentNormal,
			app.Sessions,
			app.Messages,
			app.Memories,
			normalToolsNoSubagents,
			q,
			app.Skills,
			nil,
			embedder,
		)
	})
	if err != nil {
		logging.Error("Failed to create normal agent without subagents", err)
		return nil, err
	}

	return app, nil
}

// Shutdown cleans up resources
func (app *App) Shutdown() error {
	if app.Scheduler != nil {
		app.Scheduler.Stop()
	}
	if app.Cognitive != nil {
		if err := app.Cognitive.Close(); err != nil {
			logging.Error("Failed to close Cognitive Engine", "error", err)
		}
	}
	if app.Orchestrator != nil {
		app.Orchestrator.Shutdown()
	}

	// Clean up LSP clients
	for _, client := range app.LSPRegistry.Snapshot() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := client.Shutdown(ctx); err != nil {
			logging.Warn("Failed to stop LSP client", "error", err)
		}
		// Send Exit notification
		client.Exit(ctx)
		cancel()
	}

	return nil
}

// RunNonInteractive is a stub for the CLI mode
func (app *App) RunNonInteractive(ctx context.Context, input string, model string, stream bool) error {
	return fmt.Errorf("RunNonInteractive is not supported in this version")
}

// Run is a stub for the TUI mode
func (app *App) Run(ctx context.Context) error {
	return fmt.Errorf("TUI mode is not supported in this version. Please use 'server' command.")
}
