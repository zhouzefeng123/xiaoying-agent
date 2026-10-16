package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/auramate/auramate/internal/llm/prompt"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/google/uuid"
)

type delegateTaskTool struct {
	orchestrator *Orchestrator
	workingDir   string
}

func NewDelegateTaskTool(orchestrator *Orchestrator, workingDir string) tools.BaseTool {
	return &delegateTaskTool{
		orchestrator: orchestrator,
		workingDir:   workingDir,
	}
}

func (t *delegateTaskTool) Info() tools.ToolInfo {
	return tools.ToolInfo{
		Name:        "delegate_task",
		Description: "Delegate a specific, isolated task to a temporary worker agent. The worker runs in its own context and returns only the final result. Use this for time-consuming tasks like 'search and summarize', 'analyze multiple files', or 'run long scripts' to save your own context window.\n\nExamples:\n- \"Search the web for X and summarize the top 5 results.\"\n- \"Read all files in directory Y and list the ones that import Z.\"\n\nNote: The worker does NOT have access to your conversation history. Provide ALL necessary context in the task description. ALWAYS use absolute paths for files to avoid CWD mismatches.",
		Parameters: map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The detailed task description. Be self-contained as the worker has no access to your conversation history.",
			},
			"role": map[string]any{
				"type":        "string",
				"description": "The role of the worker (e.g., 'researcher', 'coder', 'analyst'). Defaults to 'worker'.",
			},
			"system_prompt": map[string]any{
				"type":        "string",
				"description": "Optional system prompt to define the worker's behavior/persona.",
			},
			"async": map[string]any{
				"type":        "boolean",
				"description": "If true, starts the worker in background and returns an ID immediately. Recommended default for non-trivial work.",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Max runtime in seconds for this task. Default 300 seconds.",
			},
			"task_id": map[string]any{
				"type":        "string",
				"description": "Optional task identifier for tracking and correlation.",
			},
			"worker_id": map[string]any{
				"type":        "string",
				"description": "Optional. If provided, sends the task to an EXISTING worker (context is preserved) instead of creating a new one. Use this for multi-turn tasks or retries.",
			},
		},
		Required: []string{"task"},
	}
}

type DelegateTaskParams struct {
	Task         string `json:"task"`
	Content      string `json:"content"` // Alias for task
	Prompt       string `json:"prompt"`  // Alias for task
	Role         string `json:"role"`
	SystemPrompt string `json:"system_prompt"`
	Async        bool   `json:"async"`
	Timeout      int    `json:"timeout"`
	TaskID       string `json:"task_id"`
	WorkerID     string `json:"worker_id"`
}

func (t *delegateTaskTool) Run(ctx context.Context, call tools.ToolCall) (tools.ToolResponse, error) {
	var params DelegateTaskParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	// Handle aliases
	if params.Task == "" {
		if params.Content != "" {
			params.Task = params.Content
		} else if params.Prompt != "" {
			params.Task = params.Prompt
		}
	}

	if params.Task == "" {
		return tools.NewTextErrorResponse("task (or content/prompt) is required"), nil
	}
	if params.TaskID == "" {
		params.TaskID = "task-" + uuid.New().String()
	}
	if params.Timeout <= 0 {
		params.Timeout = 300
	}
	if params.Timeout < 30 {
		params.Timeout = 30
	}
	if params.Timeout > 3600 {
		params.Timeout = 3600
	}

	// Reuse existing worker if ID provided
	if params.WorkerID != "" {
		// Verify if worker exists (optional, SendAndAwait might fail if not found)
		// But Orchestrator.SendAndAwait doesn't explicitly check existence before sub.
		// However, if the worker doesn't exist, it won't reply, and we will timeout.
		// So checking first is better UX.
		workers := t.orchestrator.GetWorkers()
		if _, exists := workers[params.WorkerID]; !exists {
			return tools.NewTextErrorResponse(fmt.Sprintf("worker not found: %s", params.WorkerID)), nil
		}

		if params.Async {
			t.orchestrator.SendTask(params.WorkerID, params.TaskID, params.Task)
			return tools.NewTextResponse(fmt.Sprintf("Task sent to existing worker: %s\nTask ID: %s", params.WorkerID, params.TaskID)), nil
		}

		taskCtx, cancel := context.WithTimeout(ctx, time.Duration(params.Timeout)*time.Second)
		defer cancel()

		result, err := t.orchestrator.SendAndAwaitTask(taskCtx, params.WorkerID, params.TaskID, params.Task)
		if err != nil {
			return tools.ToolResponse{}, fmt.Errorf("worker execution failed: %w", err)
		}
		return tools.NewTextResponse(result), nil
	}

	if params.Role == "" {
		params.Role = "worker"
	}
	// Shared KV Cache Optimization: Construct a prefix that matches CoderPrompt
	prefix := prompt.GetBaseSystemPrompt()
	prefix += "\n\n" + prompt.ToolingAndSafety
	prefix += "\n\n" + prompt.CodingStandards
	// Add critical protocols for worker reliability
	prefix += "\n\n" + prompt.VerificationProtocol
	prefix += "\n\n" + prompt.DebuggingProtocol
	prefix += "\n\n" + prompt.GetLSPInformation()

	if params.SystemPrompt == "" {
		params.SystemPrompt = prefix + "\n\n" + "You are a helpful assistant. Complete the task and provide a concise result."
	} else {
		// Prepend StandardSystemPrompt to user's custom prompt to enable shared KV Cache
		params.SystemPrompt = prefix + "\n\n" + params.SystemPrompt
	}

	// Append environment info
	cwd := t.workingDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	projectRoot := workerProjectRoot(cwd)
	processCwd, _ := os.Getwd()

	// Provide explicit instructions about paths and error recovery
	osInfo := fmt.Sprintf(`Operating system: %s
Today's date: %s
Process Working Directory: %s
Project Root (Absolute): %s
Workspace Directory (Scratch/Artifacts): %s

CRITICAL INSTRUCTIONS:
1. The system process is running in the directory: %s
2. For existing repository/source files, resolve relative paths under "Project Root (Absolute)" first.
3. "Workspace Directory" is for scratch files and generated artifacts. Do not prepend it to repo paths like backend/... or client/...
4. ALWAYS use absolute paths for file operations after resolving the intended root.
5. On Windows PowerShell, separate commands with ';' instead of '&&'.
6. If edit/patch/write says "file changed after it was read", read the target file again before retrying the edit.
7. If a file is not found, DO NOT give up. Use 'ls' or 'glob' under Project Root to find it.
8. You are running in a restricted context. If you cannot complete the task, report exactly what failed.
9. RESULT SUMMARIZATION: Your final response MUST be a concise summary of your work.
   - If you retrieved data, summarize the key findings.
   - If you modified files, list the files and the nature of changes.
   - If the output is large (e.g. > 100 lines of log/data), WRITE IT TO A FILE and just report the filename in your summary.
	- OPTIONAL: You may wrap your summary in <summary>...</summary> tags. If you do, ONLY the content inside these tags will be returned to the main agent, which helps reduce token usage.%s`, runtime.GOOS, time.Now().Format("2006-01-02"), processCwd, projectRoot, cwd, processCwd, workerResultProtocolInstructions())
	params.SystemPrompt += "\n\n<env>\n" + osInfo + "\n</env>"

	// 1. Spawn the worker
	// We pass the current session ID as parent ID for cascading cancellation
	parentSessionID, _ := tools.GetContextValues(ctx)
	id, err := t.orchestrator.SpawnAgentWithTaskContext(ctx, params.Role, params.SystemPrompt, parentSessionID, params.TaskID, time.Duration(params.Timeout)*time.Second)
	if err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("Failed to spawn worker: %s", err)), nil
	}

	// 2. Async Mode: Return ID immediately
	if params.Async {
		// IMPORTANT: We MUST send the task to the worker even in async mode!
		t.orchestrator.SendTask(id, params.TaskID, params.Task)
		return tools.NewTextResponse(fmt.Sprintf("Worker spawned (ASYNC). ID: %s\nTask ID: %s\nUse 'wait_for_workers' to wait for batch completion or 'manage_worker' for per-worker operations.", id, params.TaskID)), nil
	}

	// 3. Sync Mode: Send task and await result
	// Note: Sync mode is blocking, so if parent is cancelled, this context is cancelled,
	// and SendAndAwait returns error. The Orchestrator's KillWorkersByParent will handle cleanup.
	taskCtx, cancel := context.WithTimeout(ctx, time.Duration(params.Timeout)*time.Second)
	defer cancel()
	result, err := t.orchestrator.SendAndAwaitTask(taskCtx, id, params.TaskID, params.Task)
	if err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("Worker execution failed: %s", err)), nil
	}

	// Cleanup synchronous worker immediately
	t.orchestrator.KillAgent(id)

	return tools.NewTextResponse(result), nil
}
