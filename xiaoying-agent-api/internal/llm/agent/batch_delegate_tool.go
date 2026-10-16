package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/llm/prompt"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
	"github.com/google/uuid"
)

type batchDelegateTaskTool struct {
	orchestrator *Orchestrator
	workingDir   string
}

func NewBatchDelegateTaskTool(orchestrator *Orchestrator, workingDir string) tools.BaseTool {
	return &batchDelegateTaskTool{
		orchestrator: orchestrator,
		workingDir:   workingDir,
	}
}

type batchTaskItem struct {
	ID           string              `json:"id"`
	Task         string              `json:"task"`
	Role         string              `json:"role"`
	SystemPrompt string              `json:"system_prompt"`
	Timeout      int                 `json:"timeout"`
	DependsOn    batchDependencyList `json:"depends_on"`
}

// batchDependencySpec 描述下游任务要如何消费上游依赖。
// Artifacts 为空或包含 "*" 表示全部；IncludeSummary / IncludeEvidence 为 nil 按默认 true 处理。
type batchDependencySpec struct {
	TaskID          string   `json:"task_id"`
	Artifacts       []string `json:"artifacts,omitempty"`
	IncludeSummary  *bool    `json:"include_summary,omitempty"`
	IncludeEvidence *bool    `json:"include_evidence,omitempty"`
}

// batchDependencyList 支持 depends_on 同时接受字符串和对象两种形式：
//   - "task_1"                                         => 注入该依赖全部 summary/artifacts/evidence
//   - {"task_id":"task_1","artifacts":["spec.md"]}     => 仅注入匹配的 artifact
type batchDependencyList []batchDependencySpec

func (l *batchDependencyList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*l = nil
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	specs := make([]batchDependencySpec, 0, len(raw))
	for i, item := range raw {
		itemTrimmed := strings.TrimSpace(string(item))
		if itemTrimmed == "" || itemTrimmed == "null" {
			continue
		}
		if itemTrimmed[0] == '"' {
			var s string
			if err := json.Unmarshal(item, &s); err != nil {
				return fmt.Errorf("depends_on[%d]: %w", i, err)
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			specs = append(specs, batchDependencySpec{TaskID: s})
			continue
		}
		var spec batchDependencySpec
		if err := json.Unmarshal(item, &spec); err != nil {
			return fmt.Errorf("depends_on[%d]: %w", i, err)
		}
		spec.TaskID = strings.TrimSpace(spec.TaskID)
		if spec.TaskID == "" {
			return fmt.Errorf("depends_on[%d]: task_id is required", i)
		}
		specs = append(specs, spec)
	}
	*l = specs
	return nil
}

type batchDependencyResult struct {
	TaskID       string
	Status       string
	Summary      string
	Artifacts    []string
	EvidenceRefs []string
	RawResult    string
}

type BatchDelegateParams struct {
	Tasks []batchTaskItem `json:"tasks"`
	Async *bool           `json:"async"`
}

type normalizedBatchPlan struct {
	Tasks  []batchTaskItem
	Layers [][]string
}

func normalizeBatchTasks(tasks []batchTaskItem) ([]batchTaskItem, error) {
	if len(tasks) == 0 {
		return nil, fmt.Errorf("tasks is required and cannot be empty")
	}
	if len(tasks) > 16 {
		return nil, fmt.Errorf("tasks cannot contain more than 16 items")
	}
	normalized := make([]batchTaskItem, len(tasks))
	for i, item := range tasks {
		item.ID = strings.TrimSpace(item.ID)
		item.Task = strings.TrimSpace(item.Task)
		item.Role = strings.TrimSpace(item.Role)
		item.SystemPrompt = strings.TrimSpace(item.SystemPrompt)
		if item.Task == "" {
			return nil, fmt.Errorf("tasks[%d].task is required", i)
		}
		if item.ID == "" {
			item.ID = fmt.Sprintf("task_%d", i+1)
		}
		if item.Role == "" {
			item.Role = "worker"
		}
		if item.Timeout <= 0 {
			item.Timeout = 300
		}
		if item.Timeout < 30 {
			item.Timeout = 30
		}
		if item.Timeout > 3600 {
			item.Timeout = 3600
		}
		for j := range item.DependsOn {
			item.DependsOn[j].TaskID = strings.TrimSpace(item.DependsOn[j].TaskID)
			if item.DependsOn[j].TaskID == "" {
				return nil, fmt.Errorf("tasks[%d].depends_on[%d].task_id is required", i, j)
			}
		}
		normalized[i] = item
	}
	return normalized, nil
}

func validateBatchDAG(tasks []batchTaskItem) error {
	seen := make(map[string]bool, len(tasks))
	for _, item := range tasks {
		if seen[item.ID] {
			return fmt.Errorf("duplicate task id: %s", item.ID)
		}
		seen[item.ID] = true
	}
	for _, item := range tasks {
		for _, dep := range item.DependsOn {
			if dep.TaskID == item.ID {
				return fmt.Errorf("task %s cannot depend on itself", item.ID)
			}
			if !seen[dep.TaskID] {
				return fmt.Errorf("task %s depends_on unknown id %s", item.ID, dep.TaskID)
			}
		}
	}
	if _, err := topologicalLayers(tasks); err != nil {
		return err
	}
	return nil
}

func topologicalLayers(tasks []batchTaskItem) ([][]string, error) {
	indegree := make(map[string]int, len(tasks))
	children := make(map[string][]string, len(tasks))
	ids := make([]string, 0, len(tasks))
	for _, item := range tasks {
		ids = append(ids, item.ID)
		if _, ok := indegree[item.ID]; !ok {
			indegree[item.ID] = 0
		}
		for _, dep := range item.DependsOn {
			indegree[item.ID]++
			children[dep.TaskID] = append(children[dep.TaskID], item.ID)
		}
	}
	processed := 0
	layers := make([][]string, 0)
	for processed < len(tasks) {
		layer := make([]string, 0)
		for _, id := range ids {
			if indegree[id] == 0 {
				layer = append(layer, id)
				indegree[id] = -1
			}
		}
		if len(layer) == 0 {
			return nil, fmt.Errorf("batch dependency cycle detected")
		}
		layers = append(layers, layer)
		processed += len(layer)
		for _, id := range layer {
			for _, child := range children[id] {
				indegree[child]--
			}
		}
	}
	return layers, nil
}

func buildNormalizedBatchPlan(tasks []batchTaskItem) (normalizedBatchPlan, error) {
	normalized, err := normalizeBatchTasks(tasks)
	if err != nil {
		return normalizedBatchPlan{}, err
	}
	if err := validateBatchDAG(normalized); err != nil {
		return normalizedBatchPlan{}, err
	}
	layers, err := topologicalLayers(normalized)
	if err != nil {
		return normalizedBatchPlan{}, err
	}
	return normalizedBatchPlan{Tasks: normalized, Layers: layers}, nil
}

func (t *batchDelegateTaskTool) Info() tools.ToolInfo {
	return tools.ToolInfo{
		Name:        "batch_delegate_task",
		Description: "Dispatch multiple sub-tasks to workers simultaneously. 'tasks' MUST be an array of objects. Use this for real parallel execution to save time instead of serial delegate_task calls. Use wait_for_workers after this to collect all results.",
		Parameters: map[string]any{
			"tasks": map[string]any{
				"type":        "array",
				"description": "List of task objects to dispatch in parallel.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "string",
							"description": "Unique logical task ID within this batch.",
						},
						"task": map[string]any{
							"type":        "string",
							"description": "Self-contained task content.",
						},
						"role": map[string]any{
							"type":        "string",
							"description": "Worker role, default worker.",
						},
						"system_prompt": map[string]any{
							"type":        "string",
							"description": "Optional worker prompt extension.",
						},
						"timeout": map[string]any{
							"type":        "integer",
							"description": "Timeout seconds for this task, default 300.",
						},
						"depends_on": map[string]any{
							"type": "array",
							"items": map[string]any{
								"anyOf": []any{
									map[string]any{
										"type":        "string",
										"description": "上游任务 id，注入其全部 summary/artifacts/evidence_refs。",
									},
									map[string]any{
										"type": "object",
										"properties": map[string]any{
											"task_id":          map[string]any{"type": "string", "description": "上游任务 id。"},
											"artifacts":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "要消费的 artifact 完整路径或 basename；为空或含 '*' 视为全部。"},
											"include_summary":  map[string]any{"type": "boolean", "description": "是否保留上游 summary，默认 true。"},
											"include_evidence": map[string]any{"type": "boolean", "description": "是否保留上游 evidence_refs，默认 true。"},
										},
										"required": []string{"task_id"},
									},
								},
							},
							"description": "上游依赖列表。字符串表示注入全部上游产物；对象形式 {task_id, artifacts?, include_summary?, include_evidence?} 可精细选择要消费哪些 artifact。",
						},
					},
					"required": []string{"task"},
				},
			},
			"async": map[string]any{
				"type":        "boolean",
				"description": "If true, return immediately and execute in background. Default true.",
			},
		},
		Required: []string{"tasks"},
	}
}

func (t *batchDelegateTaskTool) Run(ctx context.Context, call tools.ToolCall) (tools.ToolResponse, error) {
	var params BatchDelegateParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}
	plan, err := buildNormalizedBatchPlan(params.Tasks)
	if err != nil {
		return tools.NewTextErrorResponse(err.Error()), nil
	}
	params.Tasks = plan.Tasks
	asyncMode := true
	if params.Async != nil {
		asyncMode = *params.Async
	}
	batchID := "batch-" + uuid.NewString()
	parentSessionID, _ := tools.GetContextValues(ctx)

	workerByTaskID := make(map[string]string, len(params.Tasks))
	timeoutByTaskID := make(map[string]int, len(params.Tasks))
	for _, item := range params.Tasks {
		timeoutByTaskID[item.ID] = item.Timeout
	}

	for _, item := range params.Tasks {
		taskID := batchID + ":" + item.ID
		sysPrompt := t.buildWorkerPrompt(item.SystemPrompt)
		workerID, err := t.orchestrator.SpawnAgentWithTaskContext(ctx, item.Role, sysPrompt, parentSessionID, taskID, time.Duration(item.Timeout)*time.Second)
		if err != nil {
			return tools.NewTextErrorResponse(fmt.Sprintf("failed to spawn worker for %s: %s", item.ID, err)), nil
		}
		workerByTaskID[item.ID] = workerID
	}

	dispatchOne := func(runCtx context.Context, item batchTaskItem) {
		dependencyResults := make([]batchDependencyResult, 0, len(item.DependsOn))
		for _, depSpec := range item.DependsOn {
			depID := depSpec.TaskID
			depWorkerID := workerByTaskID[depID]
			waitTimeout := timeoutByTaskID[depID] + 120
			if err := t.orchestrator.WaitWorker(runCtx, depWorkerID, waitTimeout); err != nil {
				t.orchestrator.SendTask(workerByTaskID[item.ID], batchID+":"+item.ID, fmt.Sprintf("上游依赖失败，跳过执行。依赖任务=%s 错误=%v", depID, err))
				return
			}
			depResult := t.waitForDependencyResult(runCtx, depID, depWorkerID)
			if depResult.Status == "failed" {
				t.orchestrator.SendTask(workerByTaskID[item.ID], batchID+":"+item.ID, fmt.Sprintf("上游依赖失败，跳过执行。依赖任务=%s 摘要=%s", depID, depResult.Summary))
				return
			}
			dependencyResults = append(dependencyResults, applyDependencySpec(depResult, depSpec))
		}
		t.orchestrator.SendTask(workerByTaskID[item.ID], batchID+":"+item.ID, prependBatchDependencyContext(item.Task, dependencyResults))
	}

	startDispatch := func(runCtx context.Context) {
		var wg sync.WaitGroup
		wg.Add(len(params.Tasks))
		for _, item := range params.Tasks {
			taskItem := item
			go func() {
				defer wg.Done()
				dispatchOne(runCtx, taskItem)
			}()
		}
		wg.Wait()
	}

	if asyncMode {
		go startDispatch(context.Background())
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Batch dispatched (ASYNC). Batch ID: %s\n", batchID))
		sb.WriteString("Workers:\n")
		workerIDs := make([]string, 0, len(params.Tasks))
		for _, item := range params.Tasks {
			workerID := workerByTaskID[item.ID]
			workerIDs = append(workerIDs, workerID)
			sb.WriteString(fmt.Sprintf("- %s => worker=%s task_id=%s\n", item.ID, workerID, batchID+":"+item.ID))
		}
		workerIDsJSON, _ := json.Marshal(workerIDs)
		sb.WriteString(fmt.Sprintf("worker_ids_json=%s\n", string(workerIDsJSON)))
		if len(workerIDs) > 0 {
			sb.WriteString(fmt.Sprintf(`Next call: wait_for_workers({"worker_ids": %s, "wait_mode": "all_finished"})`, string(workerIDsJSON)))
		}
		return tools.NewTextResponse(sb.String()), nil
	}

	runCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	startDispatch(runCtx)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Batch completed (SYNC). Batch ID: %s\n", batchID))
	for _, item := range params.Tasks {
		workerID := workerByTaskID[item.ID]
		waitTimeout := item.Timeout + 120
		if err := t.orchestrator.WaitWorker(runCtx, workerID, waitTimeout); err != nil {
			sb.WriteString(fmt.Sprintf("\n--- %s (%s) ---\nError: %v\n", item.ID, workerID, err))
			continue
		}
		_, result, err := t.orchestrator.GetWorkerState(workerID)
		if err != nil {
			sb.WriteString(fmt.Sprintf("\n--- %s (%s) ---\nError: %v\n", item.ID, workerID, err))
			continue
		}
		sb.WriteString(fmt.Sprintf("\n--- %s (%s) ---\n%s\n", item.ID, workerID, result))
	}
	return tools.NewTextResponse(sb.String()), nil
}

func (t *batchDelegateTaskTool) buildWorkerPrompt(customPrompt string) string {
	prefix := prompt.GetBaseSystemPrompt()
	prefix += "\n\n" + prompt.ToolingAndSafety
	prefix += "\n\n" + prompt.CodingStandards
	prefix += "\n\n" + prompt.VerificationProtocol
	prefix += "\n\n" + prompt.DebuggingProtocol
	prefix += "\n\n" + prompt.GetLSPInformation()
	if customPrompt == "" {
		customPrompt = "You are a helpful assistant. Complete the task and provide a concise result."
	}
	fullPrompt := prefix + "\n\n" + customPrompt
	cwd := t.workingDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	projectRoot := workerProjectRoot(cwd)
	processCwd, _ := os.Getwd()
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
	return fullPrompt + "\n\n<env>\n" + osInfo + "\n</env>"
}

func latestBatchWorkerResult(history []message.Message, fallback string) string {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != message.Assistant {
			continue
		}
		content := extractBatchWorkerText(msg)
		if strings.TrimSpace(content) != "" {
			return content
		}
	}
	return strings.TrimSpace(fallback)
}

func batchDependencyResultFromContent(taskID string, content string) batchDependencyResult {
	result := batchDependencyResult{
		TaskID:    taskID,
		RawResult: strings.TrimSpace(content),
		Status:    "completed",
	}
	if structured, ok := parseWorkerResultEnvelope(result.RawResult); ok {
		result.Status = structured.Status
		result.Summary = structured.Summary
		result.Artifacts = append(result.Artifacts, structured.Artifacts...)
		result.EvidenceRefs = append(result.EvidenceRefs, structured.EvidenceRefs...)
		if result.Status == "" {
			result.Status = "completed"
		}
		if result.Summary == "" {
			result.Summary = compactWorkerResult(result.RawResult)
		}
		return result
	}
	if isWorkerFailureResult(result.RawResult) {
		result.Status = "failed"
	} else if result.RawResult == "" {
		result.Status = "completed"
	} else {
		result.Status = "completed"
	}
	result.Summary = compactWorkerResult(result.RawResult)
	return result
}

func renderBatchDependencyContext(results []batchDependencyResult) string {
	if len(results) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("上游依赖已完成。执行当前任务前，请先复用以下依赖结果：\n")
	builder.WriteString("<dependency_context>\n")
	for _, result := range results {
		builder.WriteString(fmt.Sprintf("- task_id: %s\n", result.TaskID))
		builder.WriteString(fmt.Sprintf("  status: %s\n", result.Status))
		if result.Summary != "" {
			builder.WriteString(fmt.Sprintf("  summary: %s\n", result.Summary))
		}
		if len(result.Artifacts) > 0 {
			builder.WriteString("  artifacts:\n")
			for _, artifact := range result.Artifacts {
				builder.WriteString(fmt.Sprintf("    - %s\n", artifact))
			}
		}
		if len(result.EvidenceRefs) > 0 {
			builder.WriteString("  evidence_refs:\n")
			for _, ref := range result.EvidenceRefs {
				builder.WriteString(fmt.Sprintf("    - %s\n", ref))
			}
		}
	}
	builder.WriteString("</dependency_context>")
	return builder.String()
}

func prependBatchDependencyContext(task string, results []batchDependencyResult) string {
	contextBlock := renderBatchDependencyContext(results)
	if contextBlock == "" {
		return task
	}
	return contextBlock + "\n\n当前任务：\n" + task
}

// applyDependencySpec 根据下游任务声明，过滤上游依赖结果，只保留要消费的 artifact/summary/evidence。
func applyDependencySpec(dep batchDependencyResult, spec batchDependencySpec) batchDependencyResult {
	if spec.IncludeSummary != nil && !*spec.IncludeSummary {
		dep.Summary = ""
	}
	if spec.IncludeEvidence != nil && !*spec.IncludeEvidence {
		dep.EvidenceRefs = nil
	}
	if len(spec.Artifacts) == 0 {
		return dep
	}
	wants := make(map[string]struct{}, len(spec.Artifacts))
	wantAll := false
	for _, a := range spec.Artifacts {
		trimmed := strings.TrimSpace(a)
		if trimmed == "" {
			continue
		}
		if trimmed == "*" {
			wantAll = true
			continue
		}
		wants[trimmed] = struct{}{}
	}
	if wantAll || len(wants) == 0 {
		return dep
	}
	filtered := make([]string, 0, len(dep.Artifacts))
	for _, art := range dep.Artifacts {
		if _, ok := wants[art]; ok {
			filtered = append(filtered, art)
			continue
		}
		if _, ok := wants[artifactBasename(art)]; ok {
			filtered = append(filtered, art)
		}
	}
	dep.Artifacts = filtered
	return dep
}

func artifactBasename(p string) string {
	normalized := strings.ReplaceAll(p, "\\", "/")
	idx := strings.LastIndex(normalized, "/")
	if idx == -1 {
		return normalized
	}
	return normalized[idx+1:]
}

func (t *batchDelegateTaskTool) waitForDependencyResult(ctx context.Context, taskID string, workerID string) batchDependencyResult {
	deadline := time.Now().Add(2 * time.Second)
	for {
		history, historyErr := t.orchestrator.GetWorkerHistory(workerID)
		fallback := ""
		if _, lastResult, stateErr := t.orchestrator.GetWorkerState(workerID); stateErr == nil {
			fallback = lastResult
		}
		if historyErr == nil {
			content := latestBatchWorkerResult(history, fallback)
			if strings.TrimSpace(content) != "" {
				return batchDependencyResultFromContent(taskID, content)
			}
		} else if strings.TrimSpace(fallback) != "" {
			return batchDependencyResultFromContent(taskID, fallback)
		}

		if time.Now().After(deadline) {
			return batchDependencyResultFromContent(taskID, fallback)
		}

		select {
		case <-ctx.Done():
			return batchDependencyResultFromContent(taskID, fallback)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
