package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
	"github.com/auramate/auramate/internal/session"
	"github.com/google/uuid"
)

type Orchestrator struct {
	bus                  *pubsub.Broker[MultiAgentEvent]
	workers              map[string]*worker
	workerParents        map[string][]string // parentSessionID -> []workerID
	workerMeta           map[string]*workerRuntime
	workerArchive        map[string]WorkerRuntimeSnapshot
	currentSnapshotScope string
	workerFactory        func(ctx context.Context, role, systemPrompt string) (provider.Provider, []tools.BaseTool, error)
	sessions             session.Service
	mu                   sync.RWMutex
	cancel               context.CancelFunc
}

type workerRuntime struct {
	TaskID            string
	ParentSessionID   string
	Status            string
	LastActivityAt    time.Time
	LastHeartbeatAt   time.Time
	LastProgressAt    time.Time
	CompletedAt       *time.Time
	TimeoutDuration   time.Duration
	DeadlineAt        *time.Time
	LastError         string
	FailureType       string
	Retryable         bool
	ProviderRetries   int
	OutputRetries     int
	ToolRetries       int
	LastResult        string
	StructuredStatus  string
	StructuredSummary string
	LastRecoveryNote  string
	Checkpoint        *WorkerCheckpoint
}

func isTerminalWorkerStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "partial", "stopped", "cancelled", "timed_out", "timeout":
		return true
	default:
		return false
	}
}

func buildArchivedWorkerStructuredSummary(snapshot WorkerRuntimeSnapshot) string {
	if summary := strings.TrimSpace(snapshot.StructuredSummary); summary != "" {
		return normalizeWorkerSummaryText(summary, 320)
	}
	if snapshot.Checkpoint != nil {
		if summary := strings.TrimSpace(snapshot.Checkpoint.Summary); summary != "" {
			return normalizeWorkerSummaryText(summary, 320)
		}
	}
	if preview := strings.TrimSpace(snapshot.LastResultPreview); preview != "" && preview != "..." {
		return normalizeWorkerSummaryText(preview, 320)
	}
	if errText := strings.TrimSpace(snapshot.LastError); errText != "" {
		if strings.EqualFold(snapshot.Status, "failed") || strings.EqualFold(snapshot.StructuredStatus, "failed") {
			return normalizeWorkerSummaryText(errText, 320)
		}
	}
	if snapshot.ArchivedReason == "killed" {
		if errText := strings.TrimSpace(snapshot.LastError); errText != "" {
			return normalizeWorkerSummaryText("Worker terminated before emitting a final result: "+errText, 320)
		}
		return "Worker terminated before emitting a final result."
	}
	if reason := strings.TrimSpace(snapshot.ArchivedReason); reason != "" {
		return normalizeWorkerSummaryText("Worker archived with reason: "+reason, 320)
	}
	return ""
}

func normalizeArchivedWorkerSnapshot(snapshot WorkerRuntimeSnapshot) WorkerRuntimeSnapshot {
	status := strings.ToLower(strings.TrimSpace(snapshot.Status))
	structuredStatus := strings.ToLower(strings.TrimSpace(snapshot.StructuredStatus))

	if status == "" && structuredStatus != "" {
		status = structuredStatus
	}
	if snapshot.CompletedUnix > 0 && !isTerminalWorkerStatus(status) {
		if structuredStatus != "" {
			status = structuredStatus
		} else if snapshot.LastError != "" || snapshot.FailureType != "" || snapshot.ArchivedReason == "killed" {
			status = "failed"
		} else {
			status = "completed"
		}
	}
	if status == "" {
		if snapshot.LastError != "" || snapshot.FailureType != "" || snapshot.ArchivedReason == "killed" {
			status = "failed"
		} else {
			status = "completed"
		}
	}
	snapshot.Status = status
	if snapshot.StructuredStatus == "" && isTerminalWorkerStatus(status) {
		snapshot.StructuredStatus = status
	}
	if snapshot.StructuredSummary == "" {
		snapshot.StructuredSummary = buildArchivedWorkerStructuredSummary(snapshot)
	}
	if snapshot.FailureType == "" && snapshot.Status == "failed" && snapshot.ArchivedReason == "killed" {
		snapshot.FailureType = "terminated"
	}
	return snapshot
}

func normalizeWorkerTaskScope(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	if split := strings.Index(taskID, ":"); split > 0 {
		prefix := taskID[:split]
		if strings.HasPrefix(prefix, "batch-") {
			return prefix
		}
	}
	return taskID
}

func buildWorkerSnapshotScope(parentSessionID, taskID string) string {
	parentSessionID = strings.TrimSpace(parentSessionID)
	if parentSessionID != "" {
		return parentSessionID
	}
	taskScope := normalizeWorkerTaskScope(taskID)
	if taskScope == "" {
		return taskScope
	}
	return taskScope
}

func (o *Orchestrator) scopeHasLiveWorkersLocked(scope string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return false
	}
	for _, meta := range o.workerMeta {
		if meta == nil {
			continue
		}
		if buildWorkerSnapshotScope(meta.ParentSessionID, meta.TaskID) == scope {
			return true
		}
	}
	return false
}

func (o *Orchestrator) scopeHasNonTerminalWorkersLocked(scope string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return false
	}
	for _, meta := range o.workerMeta {
		if meta == nil {
			continue
		}
		if buildWorkerSnapshotScope(meta.ParentSessionID, meta.TaskID) != scope {
			continue
		}
		if !isTerminalWorkerStatus(meta.Status) {
			return true
		}
	}
	return false
}

func (o *Orchestrator) BeginWorkerSnapshotRound(parentSessionID string) {
	parentSessionID = strings.TrimSpace(parentSessionID)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.currentSnapshotScope = parentSessionID
	o.workerArchive = make(map[string]WorkerRuntimeSnapshot)
}

func NewOrchestrator(workerFactory func(ctx context.Context, role, systemPrompt string) (provider.Provider, []tools.BaseTool, error), sessions session.Service) *Orchestrator {
	return &Orchestrator{
		bus:           pubsub.NewBroker[MultiAgentEvent](),
		workers:       make(map[string]*worker),
		workerParents: make(map[string][]string),
		workerMeta:    make(map[string]*workerRuntime),
		workerArchive: make(map[string]WorkerRuntimeSnapshot),
		workerFactory: workerFactory,
		sessions:      sessions,
	}
}

func (o *Orchestrator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	o.cancel = cancel
	go o.watchWorkerEvents(ctx)
	go o.cleanupWorkersLoop(ctx)
	return nil
}

func (o *Orchestrator) watchWorkerEvents(ctx context.Context) {
	sub := o.bus.Subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub:
			if !ok {
				return
			}
			o.touchWorker(event.Payload.From, event.Payload.Type)
		}
	}
}

func (o *Orchestrator) touchWorker(workerID string, eventType MultiAgentEventType) {
	if workerID == "" || workerID == "Orchestrator" {
		return
	}
	now := time.Now()
	o.mu.Lock()
	defer o.mu.Unlock()
	meta, exists := o.workerMeta[workerID]
	if !exists {
		return
	}
	meta.LastActivityAt = now
	if eventType == MAEventHeartbeat {
		meta.LastHeartbeatAt = now
	} else {
		meta.LastProgressAt = now
	}
	if eventType == MAEventTurnComplete {
		if meta.Status == "" || meta.Status == "running" || meta.Status == "pending" {
			meta.Status = "completed"
		}
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil {
			checkpoint.Status = meta.Status
			checkpoint.UpdatedAt = now.UnixMilli()
		}
		doneAt := now
		meta.CompletedAt = &doneAt
		meta.DeadlineAt = nil
	} else {
		if meta.Status != "failed" && !isTerminalWorkerStatus(meta.Status) {
			meta.Status = "running"
		}
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil {
			checkpoint.Status = meta.Status
			checkpoint.UpdatedAt = now.UnixMilli()
		}
	}
}

func (o *Orchestrator) cleanupWorkersLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.collectAndCleanupWorkers()
		}
	}
}

func (o *Orchestrator) collectAndCleanupWorkers() {
	now := time.Now()
	const (
		idleCompletedTTL  = 1 * time.Minute
		staleRunningTTL   = 2 * time.Minute
		heartbeatFreshTTL = 90 * time.Second
	)
	var toKill []string
	o.mu.RLock()
	for workerID, meta := range o.workerMeta {
		if meta == nil {
			continue
		}
		if meta.DeadlineAt != nil && now.After(*meta.DeadlineAt) {
			toKill = append(toKill, workerID)
			continue
		}
		progressAt := meta.LastProgressAt
		if progressAt.IsZero() {
			progressAt = meta.LastActivityAt
		}
		if meta.Status == "running" && now.Sub(progressAt) > staleRunningTTL {
			if meta.DeadlineAt != nil {
				continue
			}
			if !meta.LastHeartbeatAt.IsZero() && now.Sub(meta.LastHeartbeatAt) <= heartbeatFreshTTL {
				continue
			}
			toKill = append(toKill, workerID)
			continue
		}
		if meta.Status == "completed" && meta.CompletedAt != nil && now.Sub(*meta.CompletedAt) > idleCompletedTTL {
			toKill = append(toKill, workerID)
			continue
		}
	}
	o.mu.RUnlock()
	for _, workerID := range toKill {
		o.KillAgent(workerID)
	}
}

func (o *Orchestrator) SpawnAgent(role string, systemPrompt string, parentSessionID string) (string, error) {
	return o.SpawnAgentWithTask(role, systemPrompt, parentSessionID, "", 5*time.Minute)
}

func (o *Orchestrator) SpawnAgentWithTask(role string, systemPrompt string, parentSessionID string, taskID string, timeout time.Duration) (string, error) {
	return o.SpawnAgentWithTaskContext(context.Background(), role, systemPrompt, parentSessionID, taskID, timeout)
}

func (o *Orchestrator) SpawnAgentWithTaskContext(ctx context.Context, role string, systemPrompt string, parentSessionID string, taskID string, timeout time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	parentSessionID = strings.TrimSpace(parentSessionID)

	o.mu.Lock()
	defer o.mu.Unlock()

	if scope := buildWorkerSnapshotScope(parentSessionID, taskID); scope != "" {
		if o.currentSnapshotScope == "" || o.currentSnapshotScope == scope || !o.scopeHasNonTerminalWorkersLocked(o.currentSnapshotScope) {
			o.currentSnapshotScope = scope
		}
	}

	// logging.Info("Spawning agent", "role", role)
	// fmt.Printf("[Orchestrator] Spawning agent role=%s\n", role)

	// Worker sessions should stay attached to the parent conversation so they never leak into top-level history.
	workerTitle := "Worker: " + role
	var (
		sess session.Session
		err  error
	)
	if parentSessionID != "" {
		sess, err = o.sessions.CreateTaskSession(ctx, uuid.New().String(), parentSessionID, workerTitle)
	} else {
		sess, err = o.sessions.Create(ctx, workerTitle)
	}
	if err != nil {
		logging.Error("Orchestrator failed to create session", "error", err)
		return "", fmt.Errorf("failed to create session for worker: %w", err)
	}
	id := sess.ID
	logging.Info("Orchestrator session created", "id", id, "parent", parentSessionID)

	// Register relationship
	if parentSessionID != "" {
		o.workerParents[parentSessionID] = append(o.workerParents[parentSessionID], id)
	}

	// Create provider and tools for the agent
	prov, agentTools, err := o.workerFactory(ctx, role, systemPrompt)
	if err != nil {
		logging.Error("Orchestrator failed to create factory", "error", err)
		return "", err
	}

	w := newWorker(id, role, systemPrompt, prov, o.bus, agentTools)
	w.runtimeObserver = o
	o.workers[id] = w
	status := "idle"
	if strings.TrimSpace(taskID) != "" {
		status = "pending"
	}
	o.workerMeta[id] = &workerRuntime{
		TaskID:          taskID,
		ParentSessionID: parentSessionID,
		Status:          status,
		LastActivityAt:  time.Now(),
		LastProgressAt:  time.Now(),
		TimeoutDuration: timeout,
		Checkpoint:      newWorkerCheckpoint(role, systemPrompt, taskID, parentSessionID, status),
	}

	// Start worker synchronously to ensure subscription is active before returning
	// w.Run() is non-blocking (except for initial setup/subscription)
	if err := w.Run(context.Background()); err != nil {
		logging.Error("Worker failed to start", "id", id, "error", err)
		return "", err
	}

	return id, nil
}

func (o *Orchestrator) resetWorkerExecutionState(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if meta, ok := o.workerMeta[id]; ok && meta != nil {
		meta.LastError = ""
		meta.FailureType = ""
		meta.Retryable = false
		meta.ProviderRetries = 0
		meta.OutputRetries = 0
		meta.ToolRetries = 0
		meta.LastResult = ""
		meta.StructuredStatus = ""
		meta.StructuredSummary = ""
		meta.LastRecoveryNote = ""
		meta.CompletedAt = nil
		if meta.Status == "completed" || meta.Status == "failed" {
			meta.Status = "running"
		}
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil {
			checkpoint.Status = meta.Status
			checkpoint.LastError = ""
			checkpoint.FailureType = ""
			checkpoint.Retryable = false
			checkpoint.ProviderRetryCount = 0
			checkpoint.OutputRetryCount = 0
			checkpoint.ToolRetryCount = 0
			checkpoint.LastRecoveryNote = ""
			checkpoint.ResumeHint = ""
			checkpoint.UpdatedAt = time.Now().UnixMilli()
		}
	}
	delete(o.workerArchive, id)
}

func (o *Orchestrator) recordWorkerRetry(id, failureType, lastError, recoveryNote string, retryable bool, providerRetries, outputRetries int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if meta, ok := o.workerMeta[id]; ok && meta != nil {
		meta.Status = "running"
		meta.LastError = normalizeWorkerSummaryText(lastError, 320)
		meta.FailureType = strings.TrimSpace(failureType)
		meta.Retryable = retryable
		meta.ProviderRetries = providerRetries
		meta.OutputRetries = outputRetries
		meta.LastRecoveryNote = normalizeWorkerSummaryText(recoveryNote, 320)
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil {
			checkpoint.Status = meta.Status
			checkpoint.LastError = meta.LastError
			checkpoint.FailureType = meta.FailureType
			checkpoint.Retryable = retryable
			checkpoint.ProviderRetryCount = providerRetries
			checkpoint.OutputRetryCount = outputRetries
			checkpoint.LastRecoveryNote = meta.LastRecoveryNote
			checkpoint.PendingGoal = firstNonEmpty(strings.TrimSpace(checkpoint.PendingGoal), strings.TrimSpace(checkpoint.CurrentTask), strings.TrimSpace(checkpoint.OriginalTask))
			checkpoint.ResumeHint = firstNonEmpty(strings.TrimSpace(meta.LastRecoveryNote), strings.TrimSpace(meta.LastError), strings.TrimSpace(checkpoint.ResumeHint))
			checkpoint.UpdatedAt = time.Now().UnixMilli()
		}
	}
}

func (o *Orchestrator) recordWorkerResult(id string, result string) {
	structured, _ := parseWorkerResultEnvelope(result)
	o.mu.Lock()
	defer o.mu.Unlock()
	if meta, ok := o.workerMeta[id]; ok && meta != nil {
		meta.LastResult = compactWorkerResult(result)
		if structured.Status != "" {
			meta.StructuredStatus = structured.Status
			meta.StructuredSummary = structured.Summary
			meta.FailureType = structured.FailureType
			meta.Retryable = structured.Retryable
			if structured.Status == "completed" {
				meta.LastError = ""
				meta.FailureType = ""
				meta.Retryable = false
				meta.LastRecoveryNote = ""
			} else if structured.Status == "failed" {
				meta.Status = "failed"
				meta.LastError = normalizeWorkerSummaryText(structured.Summary, 320)
			}
		}
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil {
			checkpoint.Status = firstNonEmpty(structured.Status, meta.Status, checkpoint.Status)
			checkpoint.Summary = firstNonEmpty(structured.Summary, meta.StructuredSummary, meta.LastResult, checkpoint.Summary)
			checkpoint.FailureType = firstNonEmpty(structured.FailureType, meta.FailureType, checkpoint.FailureType)
			checkpoint.LastError = firstNonEmpty(meta.LastError, checkpoint.LastError)
			checkpoint.Retryable = structured.Retryable || meta.Retryable || checkpoint.Retryable
			checkpoint.ProviderRetryCount = maxInt(checkpoint.ProviderRetryCount, meta.ProviderRetries)
			checkpoint.OutputRetryCount = maxInt(checkpoint.OutputRetryCount, meta.OutputRetries)
			if len(structured.Artifacts) > 0 {
				checkpoint.Artifacts = append([]string(nil), structured.Artifacts...)
			}
			if len(structured.EvidenceRefs) > 0 {
				checkpoint.EvidenceRefs = append([]string(nil), structured.EvidenceRefs...)
			}
			switch structured.Status {
			case "completed":
				checkpoint.LastError = ""
				checkpoint.FailureType = ""
				checkpoint.Retryable = false
				checkpoint.LastRecoveryNote = ""
				checkpoint.PendingGoal = ""
				checkpoint.ResumeHint = ""
			case "failed", "partial":
				checkpoint.PendingGoal = firstNonEmpty(strings.TrimSpace(checkpoint.PendingGoal), strings.TrimSpace(checkpoint.CurrentTask), strings.TrimSpace(checkpoint.OriginalTask))
				checkpoint.ResumeHint = firstNonEmpty(strings.TrimSpace(checkpoint.LastRecoveryNote), strings.TrimSpace(structured.Summary), strings.TrimSpace(meta.LastError), strings.TrimSpace(checkpoint.ResumeHint))
			}
			checkpoint.UpdatedAt = time.Now().UnixMilli()
		}
	}
}

func (o *Orchestrator) markWorkerProgress(id string) {
	now := time.Now()
	o.mu.Lock()
	defer o.mu.Unlock()
	if meta, ok := o.workerMeta[id]; ok && meta != nil {
		meta.LastActivityAt = now
		meta.LastProgressAt = now
		if !isTerminalWorkerStatus(meta.Status) {
			meta.Status = "running"
		}
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil {
			checkpoint.Status = meta.Status
			checkpoint.UpdatedAt = now.UnixMilli()
		}
	}
}

func (o *Orchestrator) finishWorkerTask(id, result string) {
	if strings.TrimSpace(result) != "" {
		o.recordWorkerResult(id, result)
	}
	now := time.Now()
	o.mu.Lock()
	defer o.mu.Unlock()
	if meta, ok := o.workerMeta[id]; ok && meta != nil {
		status := firstNonEmpty(meta.StructuredStatus, meta.Status)
		if status == "" || status == "pending" || status == "running" {
			status = "completed"
		}
		meta.Status = status
		meta.LastActivityAt = now
		meta.LastProgressAt = now
		meta.CompletedAt = &now
		meta.DeadlineAt = nil
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil {
			checkpoint.Status = meta.Status
			checkpoint.UpdatedAt = now.UnixMilli()
		}
	}
}

func (o *Orchestrator) recordWorkerFailure(id, failureType, lastError string, retryable bool, providerRetries, outputRetries int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if meta, ok := o.workerMeta[id]; ok && meta != nil {
		meta.Status = "failed"
		meta.LastError = normalizeWorkerSummaryText(lastError, 320)
		meta.FailureType = strings.TrimSpace(failureType)
		meta.Retryable = retryable
		meta.ProviderRetries = providerRetries
		meta.OutputRetries = outputRetries
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil {
			checkpoint.Status = meta.Status
			checkpoint.LastError = meta.LastError
			checkpoint.FailureType = meta.FailureType
			checkpoint.Retryable = retryable
			checkpoint.ProviderRetryCount = providerRetries
			checkpoint.OutputRetryCount = outputRetries
			checkpoint.PendingGoal = firstNonEmpty(strings.TrimSpace(checkpoint.PendingGoal), strings.TrimSpace(checkpoint.CurrentTask), strings.TrimSpace(checkpoint.OriginalTask))
			checkpoint.ResumeHint = firstNonEmpty(strings.TrimSpace(checkpoint.LastRecoveryNote), strings.TrimSpace(meta.LastError), strings.TrimSpace(checkpoint.ResumeHint))
			checkpoint.UpdatedAt = time.Now().UnixMilli()
		}
	}
}

func (o *Orchestrator) recordWorkerToolRecovery(id, toolName, lastError string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if meta, ok := o.workerMeta[id]; ok && meta != nil {
		meta.Status = "running"
		meta.ToolRetries++
		meta.LastError = normalizeWorkerSummaryText(lastError, 320)
		meta.FailureType = "tool_error"
		meta.Retryable = true
		meta.LastRecoveryNote = normalizeWorkerSummaryText(
			fmt.Sprintf("Tool %s failed; continue the same task from the latest verified state and choose a different recovery action.", strings.TrimSpace(toolName)),
			320,
		)
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil {
			checkpoint.Status = meta.Status
			checkpoint.LastError = meta.LastError
			checkpoint.FailureType = meta.FailureType
			checkpoint.Retryable = true
			checkpoint.ToolRetryCount = meta.ToolRetries
			checkpoint.LastRecoveryNote = meta.LastRecoveryNote
			checkpoint.PendingGoal = firstNonEmpty(strings.TrimSpace(checkpoint.PendingGoal), strings.TrimSpace(checkpoint.CurrentTask), strings.TrimSpace(checkpoint.OriginalTask))
			checkpoint.ResumeHint = firstNonEmpty(strings.TrimSpace(meta.LastRecoveryNote), strings.TrimSpace(meta.LastError), strings.TrimSpace(checkpoint.ResumeHint))
			checkpoint.UpdatedAt = time.Now().UnixMilli()
		}
	}
}

func (o *Orchestrator) recordWorkerToolExecution(id, toolName, input, result string, isError bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	meta, ok := o.workerMeta[id]
	if !ok || meta == nil {
		return
	}
	checkpoint := ensureWorkerCheckpoint(meta, "", "")
	if checkpoint == nil {
		return
	}
	status := "success"
	if isError {
		status = "error"
	}
	checkpoint.RecentToolSteps = appendWorkerCheckpointStep(checkpoint.RecentToolSteps, WorkerCheckpointStep{
		Tool:         toolName,
		InputPreview: input,
		Status:       status,
		Summary:      result,
	})
	checkpoint.UpdatedAt = time.Now().UnixMilli()
}

func (o *Orchestrator) applyWorkerCheckpointHistory(id string, checkpoint *WorkerCheckpoint) error {
	if checkpoint == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	w, ok := o.workers[id]
	if !ok || w == nil {
		return fmt.Errorf("worker not found: %s", id)
	}
	history := workerCheckpointHistoryToMessages(checkpoint)
	if len(history) == 0 {
		return nil
	}
	w.mu.Lock()
	w.history = history
	w.mu.Unlock()
	if meta, ok := o.workerMeta[id]; ok && meta != nil {
		meta.Checkpoint = cloneWorkerCheckpoint(checkpoint)
	}
	return nil
}

func (o *Orchestrator) ResumeWorkerFromCheckpoint(ctx context.Context, id, overrideTask string) (string, error) {
	snapshot, err := o.GetWorkerRuntime(id)
	if err != nil {
		return "", err
	}
	if !snapshot.Archived && !isTerminalWorkerStatus(snapshot.Status) {
		return "", fmt.Errorf("worker is still %s; wait, stop, or inspect before resuming: %s", snapshot.Status, id)
	}
	if snapshot.Checkpoint == nil {
		return "", fmt.Errorf("worker checkpoint not available: %s", id)
	}
	checkpoint := cloneWorkerCheckpoint(snapshot.Checkpoint)
	if checkpoint == nil {
		return "", fmt.Errorf("worker checkpoint not available: %s", id)
	}
	checkpoint.ValidArtifacts, checkpoint.MissingArtifacts = validateWorkerCheckpointArtifacts(checkpoint.Artifacts)
	role := firstNonEmpty(checkpoint.Role, snapshot.Role)
	systemPrompt := strings.TrimSpace(checkpoint.SystemPrompt)
	if systemPrompt == "" {
		return "", fmt.Errorf("worker checkpoint missing system prompt: %s", id)
	}
	timeout := time.Duration(snapshot.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	resumedID, err := o.SpawnAgentWithTaskContext(ctx, role, systemPrompt, checkpoint.ParentSessionID, checkpoint.TaskID, timeout)
	if err != nil {
		return "", err
	}
	if err := o.applyWorkerCheckpointHistory(resumedID, checkpoint); err != nil {
		o.KillAgent(resumedID)
		return "", err
	}
	o.SendTask(resumedID, checkpoint.TaskID, buildWorkerResumePrompt(checkpoint, overrideTask))
	return resumedID, nil
}

func (o *Orchestrator) ListActiveWorkers() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()

	var active []string
	for id, w := range o.workers {
		if w.IsBusy() {
			active = append(active, id)
		}
	}
	return active
}

func (o *Orchestrator) GetAllWorkerIDs() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()

	var ids []string
	for id := range o.workers {
		ids = append(ids, id)
	}
	return ids
}

// SpawnWorker creates a temporary worker for a specific task and waits for its result.
// It returns the result content or error. This is a blocking call that manages the worker lifecycle.
func (o *Orchestrator) SpawnWorker(ctx context.Context, role, systemPrompt, task string) (string, error) {
	// 1. Spawn the worker
	// For SpawnWorker (legacy/simple method), we don't have parentSessionID readily available from signature.
	// We can pass empty string, or we should update SpawnWorker signature?
	// Given this is used internally or by older tools, empty string is safer than breaking API widely.
	// However, if we want cascading kill to work for SpawnWorker too, we need it.
	// But SpawnWorker is blocking, so the caller (e.g. DelegateTaskTool) waits for it.
	// If caller context is cancelled, SpawnWorker context is cancelled?
	// DelegateTaskTool calls SpawnWorker with a context. If that context is cancelled, SpawnWorker should exit?
	// Actually DelegateTaskTool calls SendAndAwait which handles context cancellation.
	// So passing "" as parentID is acceptable for now, as the lifecycle is managed by the blocking call.

	id, err := o.SpawnAgent(role, systemPrompt, "")
	if err != nil {
		return "", err
	}
	defer o.KillAgent(id)

	return o.SendAndAwaitTask(ctx, id, "", task)
}

// SendAndAwait sends a task to an existing worker and waits for the turn to complete.
func (o *Orchestrator) SendAndAwait(ctx context.Context, id, task string) (string, error) {
	return o.SendAndAwaitTask(ctx, id, "", task)
}

func (o *Orchestrator) SendAndAwaitTask(ctx context.Context, id, taskID, task string) (string, error) {
	// Create a child context for subscription so we can cancel it when done
	subCtx, cancelSub := context.WithCancel(ctx)
	defer cancelSub()

	// Subscribe to the bus
	sub := o.bus.Subscribe(subCtx)

	// Send the task
	o.SendTask(id, taskID, task)

	var resultBuilder strings.Builder

	// Wait for response or timeout
	// Use a timer to detect potential stuck state if no messages arrive for a long time
	idleTimeout := 2 * time.Minute
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return resultBuilder.String(), ctx.Err()
		case <-timer.C:
			// If we haven't received any message from the worker for a while, we assume it might be stuck or failed silently
			return resultBuilder.String(), fmt.Errorf("worker execution timed out (no activity for %v)", idleTimeout)
		case event, ok := <-sub:
			if !ok {
				return resultBuilder.String(), fmt.Errorf("bus closed")
			}
			// Only process messages from this worker to Orchestrator
			// We also need to listen for broadcast messages from this worker (like ToolCalls which are usually broadcast)
			if event.Payload.From != id {
				continue
			}
			if event.Payload.To != "Orchestrator" && event.Payload.To != "*" {
				continue
			}
			if taskID != "" && event.Payload.TaskID != taskID {
				continue
			}

			// Reset timer on valid message
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idleTimeout)

			switch event.Payload.Type {
			case MAEventMessage:
				// Only accumulate content directed to Orchestrator
				if event.Payload.To == "Orchestrator" {
					if resultBuilder.Len() > 0 {
						resultBuilder.WriteString("\n")
					}
					resultBuilder.WriteString(event.Payload.Content)
				}
			case MAEventToolCall:
				// Just keep alive, maybe log
			case MAEventToolResult:
				// Append tool result to output for visibility, especially if the LLM doesn't summarize it
				if event.Payload.ToolResult != nil {
					// We wrap it in a special tag or format to distinguish it, but simple text is fine for now
					// Limit the length if necessary, but for now we trust the tool result is relevant
					content := fmt.Sprintf("\n[Tool Result: %s]\n%s\n", event.Payload.ToolResult.Name, event.Payload.ToolResult.Content)
					if resultBuilder.Len() > 0 {
						resultBuilder.WriteString("\n")
					}
					resultBuilder.WriteString(content)
				}
			case MAEventTurnComplete:
				// Finished
				// Summarization Optimization:
				// If the output contains <summary>...</summary>, extract only that part for the tool result.
				// This saves context for the Main Agent while preserving the full log in the stream/files.
				fullOutput := resultBuilder.String()
				if structured, ok := parseWorkerResultEnvelope(fullOutput); ok {
					if rendered := renderWorkerResultEnvelope(structured); rendered != "" {
						return rendered, nil
					}
				}
				if start := strings.Index(fullOutput, "<summary>"); start != -1 {
					if end := strings.Index(fullOutput, "</summary>"); end != -1 && end > start {
						summary := fullOutput[start+9 : end]
						return strings.TrimSpace(summary), nil
					}
				}
				return fullOutput, nil
			}
		}
	}
}

// WaitWorker blocks until the worker is idle or timeout occurs.
func (o *Orchestrator) WaitWorker(ctx context.Context, id string, timeoutSeconds int) error {
	// Create a timeout context
	timeout := time.Duration(timeoutSeconds) * time.Second
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Polling loop (Simple but effective for in-process)
	// Since we don't have a "BusyStateChanged" event easily accessible without modifying Worker struct deeply,
	// we poll IsBusy() every 500ms. This is cheap.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			if waitCtx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("wait timed out after %d seconds (worker still running)", timeoutSeconds)
			}
			return waitCtx.Err()
		case <-ticker.C:
			runtimeInfo, runtimeErr := o.GetWorkerRuntime(id)
			if runtimeErr == nil {
				if runtimeInfo.Archived {
					return nil
				}
				if isTerminalWorkerStatus(runtimeInfo.Status) {
					return nil
				}
				if runtimeInfo.Status == "pending" {
					continue
				}
			}

			o.mu.RLock()
			w, ok := o.workers[id]
			o.mu.RUnlock()
			if !ok {
				if runtimeErr == nil {
					return nil
				}
				return fmt.Errorf("worker not found: %s", id)
			}
			if !w.IsBusy() {
				if runtimeErr == nil && (runtimeInfo.Status == "running" || runtimeInfo.Status == "pending") {
					continue
				}
				return nil
			}
		}
	}
}

func (o *Orchestrator) Broadcast(content string) {
	o.bus.Publish(pubsub.EventType(MAEventSystem), MultiAgentEvent{
		Type:    MAEventSystem,
		From:    "Orchestrator",
		To:      "*",
		Content: content,
	})
}

func (o *Orchestrator) Send(to string, content string) {
	o.SendTask(to, "", content)
}

func (o *Orchestrator) SendTask(to string, taskID string, content string) {
	now := time.Now()
	o.mu.Lock()
	if meta, ok := o.workerMeta[to]; ok {
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil && isTerminalWorkerStatus(meta.Status) {
			trimmedContent := strings.TrimSpace(content)
			if trimmedContent != "" && trimmedContent != strings.TrimSpace(checkpoint.CurrentTask) && trimmedContent != strings.TrimSpace(checkpoint.OriginalTask) && !strings.HasPrefix(trimmedContent, "SYSTEM CHECKPOINT:") {
				role := checkpoint.Role
				systemPrompt := checkpoint.SystemPrompt
				if w, exists := o.workers[to]; exists && w != nil {
					role = firstNonEmpty(role, w.role)
					systemPrompt = firstNonEmpty(systemPrompt, w.systemPrompt)
				}
				meta.Checkpoint = newWorkerCheckpoint(role, systemPrompt, firstNonEmpty(taskID, meta.TaskID), meta.ParentSessionID, "running")
				checkpoint = meta.Checkpoint
			}
		}
		meta.LastActivityAt = now
		meta.LastProgressAt = now
		meta.Status = "running"
		if taskID != "" {
			meta.TaskID = taskID
		}
		if meta.TimeoutDuration <= 0 {
			meta.TimeoutDuration = 5 * time.Minute
		}
		deadline := now.Add(meta.TimeoutDuration)
		meta.DeadlineAt = &deadline
		meta.CompletedAt = nil
		if checkpoint := ensureWorkerCheckpoint(meta, "", ""); checkpoint != nil {
			if checkpoint.OriginalTask == "" {
				checkpoint.OriginalTask = strings.TrimSpace(content)
			}
			checkpoint.CurrentTask = strings.TrimSpace(content)
			if !strings.HasPrefix(strings.TrimSpace(content), "SYSTEM CHECKPOINT:") {
				checkpoint.PendingGoal = strings.TrimSpace(content)
				checkpoint.ResumeHint = ""
			}
			checkpoint.Status = meta.Status
			checkpoint.TaskID = firstNonEmpty(checkpoint.TaskID, taskID, meta.TaskID)
			checkpoint.ParentSessionID = firstNonEmpty(checkpoint.ParentSessionID, meta.ParentSessionID)
			checkpoint.UpdatedAt = now.UnixMilli()
		}
	}
	delete(o.workerArchive, to)
	o.mu.Unlock()
	o.bus.Publish(pubsub.EventType(MAEventMessage), MultiAgentEvent{
		Type:    MAEventMessage,
		From:    "Orchestrator",
		To:      to,
		TaskID:  taskID,
		Content: content,
	})
}

func (o *Orchestrator) Shutdown() {
	if o.cancel != nil {
		o.cancel()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, w := range o.workers {
		w.Stop()
	}
	o.bus.Shutdown()
}

// Subscribe allows external listeners (like the UI/WS) to listen to the bus
func (o *Orchestrator) DistributeTask(task string, workers []string) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	// If workers list is empty, send to all
	if len(workers) == 0 {
		for id := range o.workers {
			workers = append(workers, id)
		}
	}

	for _, workerID := range workers {
		if _, exists := o.workers[workerID]; exists {
			o.Send(workerID, task)
		}
	}
}

func (o *Orchestrator) KillAgent(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.killAgentLocked(id)
}

func (o *Orchestrator) killAgentLocked(id string) {
	o.archiveWorkerLocked(id, "killed")
	if w, exists := o.workers[id]; exists {
		w.Stop()
		delete(o.workers, id)
	}
	delete(o.workerMeta, id)
	for parentID, workerIDs := range o.workerParents {
		filtered := workerIDs[:0]
		for _, workerID := range workerIDs {
			if workerID != id {
				filtered = append(filtered, workerID)
			}
		}
		if len(filtered) == 0 {
			delete(o.workerParents, parentID)
			continue
		}
		o.workerParents[parentID] = filtered
	}
}

func (o *Orchestrator) archiveWorkerLocked(id string, reason string) {
	meta, metaExists := o.workerMeta[id]
	w, workerExists := o.workers[id]
	if !metaExists && !workerExists {
		return
	}
	snapshot := WorkerRuntimeSnapshot{
		WorkerID:       id,
		Archived:       true,
		ArchivedReason: strings.TrimSpace(reason),
	}
	if workerExists {
		snapshot.Role = w.role
		snapshot.LastResultPreview = compactWorkerResult(w.GetLastResult())
	}
	if metaExists && meta != nil {
		snapshot.TaskID = meta.TaskID
		snapshot.Status = meta.Status
		snapshot.LastActivityAt = meta.LastActivityAt
		snapshot.LastHeartbeatAt = meta.LastHeartbeatAt
		snapshot.LastProgressAt = meta.LastProgressAt
		snapshot.TimeoutSeconds = int(meta.TimeoutDuration.Seconds())
		snapshot.ParentSessionID = meta.ParentSessionID
		snapshot.LastError = meta.LastError
		snapshot.FailureType = meta.FailureType
		snapshot.RetryableFailure = meta.Retryable
		snapshot.ProviderRetryCount = meta.ProviderRetries
		snapshot.OutputRetryCount = meta.OutputRetries
		snapshot.ToolRetryCount = meta.ToolRetries
		snapshot.StructuredStatus = meta.StructuredStatus
		snapshot.StructuredSummary = meta.StructuredSummary
		snapshot.LastRecoveryNote = meta.LastRecoveryNote
		if snapshot.LastResultPreview == "" {
			snapshot.LastResultPreview = meta.LastResult
		}
		if meta.DeadlineAt != nil {
			snapshot.HasDeadline = true
			snapshot.DeadlineUnix = meta.DeadlineAt.Unix()
		}
		if meta.CompletedAt != nil {
			snapshot.CompletedUnix = meta.CompletedAt.Unix()
		} else {
			now := time.Now().Unix()
			snapshot.CompletedUnix = now
		}
		snapshot.Checkpoint = buildWorkerCheckpointSnapshot(meta.Checkpoint, meta, w)
	}
	if snapshot.Status == "" {
		snapshot.Status = "completed"
	}
	if snapshot.Checkpoint == nil {
		snapshot.Checkpoint = buildWorkerCheckpointSnapshot(nil, meta, w)
	}
	o.workerArchive[id] = normalizeArchivedWorkerSnapshot(snapshot)
}

func (o *Orchestrator) KillWorkersByParent(parentID string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	workerIDs, exists := o.workerParents[parentID]
	if !exists {
		return
	}

	logging.Info("Killing child workers for session", "parent_id", parentID, "count", len(workerIDs))
	for _, id := range workerIDs {
		o.killAgentLocked(id)
		logging.Info("Killed child worker", "id", id)
	}
	delete(o.workerParents, parentID)
}

func (o *Orchestrator) GetWorkers() map[string]string {
	o.mu.RLock()
	defer o.mu.RUnlock()

	result := make(map[string]string)
	for id, w := range o.workers {
		result[id] = w.role
	}
	return result
}

func (o *Orchestrator) GetWorkerState(id string) (bool, string, error) {
	o.mu.RLock()
	w, ok := o.workers[id]
	archived, archivedOk := o.workerArchive[id]
	o.mu.RUnlock()
	if !ok {
		if archivedOk {
			archived = normalizeArchivedWorkerSnapshot(archived)
			return false, firstNonEmpty(archived.LastResultPreview, archived.StructuredSummary, archived.LastError), nil
		}
		return false, "", fmt.Errorf("worker not found: %s", id)
	}
	return w.IsBusy(), w.GetLastResult(), nil
}

func (o *Orchestrator) GetWorkerHistory(id string) ([]message.Message, error) {
	o.mu.RLock()
	w, ok := o.workers[id]
	archived, archivedOk := o.workerArchive[id]
	o.mu.RUnlock()
	if !ok {
		if archivedOk {
			archived = normalizeArchivedWorkerSnapshot(archived)
			content := strings.TrimSpace(archived.LastResultPreview)
			if content == "" {
				content = strings.TrimSpace(archived.StructuredSummary)
			}
			if content == "" {
				content = strings.TrimSpace(archived.LastError)
			}
			if content == "" {
				return nil, fmt.Errorf("worker history not available: %s", id)
			}
			return []message.Message{message.NewTextMessage(message.Assistant, content)}, nil
		}
		return nil, fmt.Errorf("worker not found: %s", id)
	}
	return w.GetHistory(), nil
}

type WorkerRuntimeSnapshot struct {
	WorkerID           string            `json:"worker_id"`
	Role               string            `json:"role"`
	TaskID             string            `json:"task_id,omitempty"`
	Status             string            `json:"status"`
	LastActivityAt     time.Time         `json:"last_activity_at"`
	LastHeartbeatAt    time.Time         `json:"last_heartbeat_at,omitempty"`
	LastProgressAt     time.Time         `json:"last_progress_at,omitempty"`
	TimeoutSeconds     int               `json:"timeout_seconds"`
	HasDeadline        bool              `json:"has_deadline"`
	DeadlineUnix       int64             `json:"deadline_unix,omitempty"`
	CompletedUnix      int64             `json:"completed_unix,omitempty"`
	ParentSessionID    string            `json:"parent_session_id,omitempty"`
	Archived           bool              `json:"archived"`
	ArchivedReason     string            `json:"archived_reason,omitempty"`
	LastError          string            `json:"last_error,omitempty"`
	FailureType        string            `json:"failure_type,omitempty"`
	RetryableFailure   bool              `json:"retryable_failure"`
	ProviderRetryCount int               `json:"provider_retry_count,omitempty"`
	OutputRetryCount   int               `json:"output_retry_count,omitempty"`
	ToolRetryCount     int               `json:"tool_retry_count,omitempty"`
	LastResultPreview  string            `json:"last_result_preview,omitempty"`
	StructuredStatus   string            `json:"structured_status,omitempty"`
	StructuredSummary  string            `json:"structured_summary,omitempty"`
	LastRecoveryNote   string            `json:"last_recovery_note,omitempty"`
	Checkpoint         *WorkerCheckpoint `json:"checkpoint,omitempty"`
}

func (o *Orchestrator) GetWorkerRuntime(id string) (WorkerRuntimeSnapshot, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	w, ok := o.workers[id]
	if !ok {
		if archived, archivedOK := o.workerArchive[id]; archivedOK {
			return normalizeArchivedWorkerSnapshot(archived), nil
		}
		return WorkerRuntimeSnapshot{}, fmt.Errorf("worker not found: %s", id)
	}
	meta, ok := o.workerMeta[id]
	if !ok || meta == nil {
		return WorkerRuntimeSnapshot{}, fmt.Errorf("worker metadata not found: %s", id)
	}
	snapshot := WorkerRuntimeSnapshot{
		WorkerID:           id,
		Role:               w.role,
		TaskID:             meta.TaskID,
		Status:             meta.Status,
		LastActivityAt:     meta.LastActivityAt,
		LastHeartbeatAt:    meta.LastHeartbeatAt,
		LastProgressAt:     meta.LastProgressAt,
		TimeoutSeconds:     int(meta.TimeoutDuration.Seconds()),
		ParentSessionID:    meta.ParentSessionID,
		LastError:          meta.LastError,
		FailureType:        meta.FailureType,
		RetryableFailure:   meta.Retryable,
		ProviderRetryCount: meta.ProviderRetries,
		OutputRetryCount:   meta.OutputRetries,
		ToolRetryCount:     meta.ToolRetries,
		LastResultPreview:  meta.LastResult,
		StructuredStatus:   meta.StructuredStatus,
		StructuredSummary:  meta.StructuredSummary,
		LastRecoveryNote:   meta.LastRecoveryNote,
		Checkpoint:         buildWorkerCheckpointSnapshot(meta.Checkpoint, meta, w),
	}
	if meta.DeadlineAt != nil {
		snapshot.HasDeadline = true
		snapshot.DeadlineUnix = meta.DeadlineAt.Unix()
	}
	if meta.CompletedAt != nil {
		snapshot.CompletedUnix = meta.CompletedAt.Unix()
	}
	return snapshot, nil
}

func (o *Orchestrator) listWorkerRuntimeSnapshots(limit int, includeArchived bool, scope string) []WorkerRuntimeSnapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()
	capacity := len(o.workerMeta)
	if includeArchived {
		capacity += len(o.workerArchive)
	}
	snapshots := make([]WorkerRuntimeSnapshot, 0, capacity)
	for id, meta := range o.workerMeta {
		if meta == nil {
			continue
		}
		if scope != "" && buildWorkerSnapshotScope(meta.ParentSessionID, meta.TaskID) != scope {
			continue
		}
		w := o.workers[id]
		snapshot := WorkerRuntimeSnapshot{
			WorkerID:           id,
			TaskID:             meta.TaskID,
			Status:             meta.Status,
			LastActivityAt:     meta.LastActivityAt,
			LastHeartbeatAt:    meta.LastHeartbeatAt,
			LastProgressAt:     meta.LastProgressAt,
			TimeoutSeconds:     int(meta.TimeoutDuration.Seconds()),
			ParentSessionID:    meta.ParentSessionID,
			LastError:          meta.LastError,
			FailureType:        meta.FailureType,
			RetryableFailure:   meta.Retryable,
			ProviderRetryCount: meta.ProviderRetries,
			OutputRetryCount:   meta.OutputRetries,
			ToolRetryCount:     meta.ToolRetries,
			LastResultPreview:  meta.LastResult,
			StructuredStatus:   meta.StructuredStatus,
			StructuredSummary:  meta.StructuredSummary,
			LastRecoveryNote:   meta.LastRecoveryNote,
			Checkpoint:         buildWorkerCheckpointSnapshot(meta.Checkpoint, meta, w),
		}
		if w != nil {
			snapshot.Role = w.role
		}
		if meta.DeadlineAt != nil {
			snapshot.HasDeadline = true
			snapshot.DeadlineUnix = meta.DeadlineAt.Unix()
		}
		if meta.CompletedAt != nil {
			snapshot.CompletedUnix = meta.CompletedAt.Unix()
		}
		snapshots = append(snapshots, snapshot)
	}
	if includeArchived {
		for id, snapshot := range o.workerArchive {
			if _, active := o.workerMeta[id]; active {
				continue
			}
			if scope != "" && buildWorkerSnapshotScope(snapshot.ParentSessionID, snapshot.TaskID) != scope {
				continue
			}
			snapshots = append(snapshots, normalizeArchivedWorkerSnapshot(snapshot))
		}
	}
	sort.Slice(snapshots, func(i, j int) bool {
		left := snapshots[i].LastActivityAt.Unix()
		right := snapshots[j].LastActivityAt.Unix()
		if left == right {
			return snapshots[i].WorkerID > snapshots[j].WorkerID
		}
		return left > right
	})
	if limit > 0 && len(snapshots) > limit {
		return append([]WorkerRuntimeSnapshot(nil), snapshots[:limit]...)
	}
	return append([]WorkerRuntimeSnapshot(nil), snapshots...)
}

func (o *Orchestrator) ListWorkerRuntimeSnapshots(limit int) []WorkerRuntimeSnapshot {
	return o.listWorkerRuntimeSnapshots(limit, true, "")
}

func (o *Orchestrator) ListActiveWorkerRuntimeSnapshots(limit int) []WorkerRuntimeSnapshot {
	return o.listWorkerRuntimeSnapshots(limit, false, "")
}

func (o *Orchestrator) ListCurrentWorkerRuntimeSnapshots(limit int) []WorkerRuntimeSnapshot {
	o.mu.RLock()
	scope := o.currentSnapshotScope
	o.mu.RUnlock()
	if scope == "" {
		return []WorkerRuntimeSnapshot{}
	}
	return o.listWorkerRuntimeSnapshots(limit, true, scope)
}

func (o *Orchestrator) ListCurrentActiveWorkerRuntimeSnapshots(limit int) []WorkerRuntimeSnapshot {
	o.mu.RLock()
	scope := o.currentSnapshotScope
	o.mu.RUnlock()
	if scope == "" {
		return []WorkerRuntimeSnapshot{}
	}
	return o.listWorkerRuntimeSnapshots(limit, false, scope)
}

// Subscribe allows external listeners (like the UI/WS) to listen to the bus
func (o *Orchestrator) Subscribe(ctx context.Context) <-chan pubsub.Event[MultiAgentEvent] {
	return o.bus.Subscribe(ctx)
}
