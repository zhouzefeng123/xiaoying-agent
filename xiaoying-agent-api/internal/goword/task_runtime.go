package goword

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type SessionCheckpoint struct {
	ID           string    `json:"id"`
	Label        string    `json:"label,omitempty"`
	Revision     int64     `json:"revision"`
	CreatedAt    time.Time `json:"created_at"`
	SnapshotPath string    `json:"snapshot_path,omitempty"`
}

type AnchorHealth struct {
	Status            string   `json:"status,omitempty"`
	ExpectedRevision  int64    `json:"expected_revision,omitempty"`
	CurrentRevision   int64    `json:"current_revision,omitempty"`
	DriftScore        int      `json:"drift_score,omitempty"`
	IsStale           bool     `json:"is_stale,omitempty"`
	ReplanApplied     bool     `json:"replan_applied,omitempty"`
	RecommendedAction string   `json:"recommended_action,omitempty"`
	Observations      []string `json:"observations,omitempty"`
}

type FailureRecoveryPlan struct {
	ErrorCode        string   `json:"error_code,omitempty"`
	PrimaryStrategy  string   `json:"primary_strategy,omitempty"`
	BackupStrategies []string `json:"backup_strategies,omitempty"`
	Retryable        bool     `json:"retryable,omitempty"`
	AutoRetryCount   int      `json:"auto_retry_count,omitempty"`
	MaxAutoRetries   int      `json:"max_auto_retries,omitempty"`
	UserPrompt       string   `json:"user_prompt,omitempty"`
	CheckpointID     string   `json:"checkpoint_id,omitempty"`
	Notes            []string `json:"notes,omitempty"`
}

type BatchStepResult struct {
	Index         int                  `json:"index"`
	Status        string               `json:"status"`
	StatusText    string               `json:"status_text,omitempty"`
	Plan          *ActionPlanResult    `json:"plan,omitempty"`
	ExecuteResult *ActionExecuteResult `json:"execute_result,omitempty"`
}

type ActionBatch struct {
	ID              string               `json:"id"`
	SessionID       string               `json:"session_id"`
	Status          string               `json:"status"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	InitialRevision int64                `json:"initial_revision"`
	PlannedRevision int64                `json:"planned_revision,omitempty"`
	CheckpointID    string               `json:"checkpoint_id,omitempty"`
	Actions         []EditorAction       `json:"actions,omitempty"`
	Plans           []*ActionPlanResult  `json:"plans,omitempty"`
	Results         []BatchStepResult    `json:"results,omitempty"`
	FailureStep     int                  `json:"failure_step,omitempty"`
	RecoveryPlan    *FailureRecoveryPlan `json:"recovery_plan,omitempty"`
}

type checkpointPayload struct {
	Checkpoint SessionCheckpoint `json:"checkpoint"`
	Document   *Document         `json:"document"`
}

func (s *Service) SaveCheckpoint(sessionID string, label string) (*SessionCheckpoint, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		return nil, err
	}
	if session.Document == nil {
		return nil, fmt.Errorf("document is nil")
	}

	checkpoint := SessionCheckpoint{
		ID:        uuid.NewString(),
		Label:     strings.TrimSpace(label),
		Revision:  session.Document.Revision,
		CreatedAt: time.Now(),
	}
	checkpoint.SnapshotPath = filepath.Join(s.snapshotDir, fmt.Sprintf("cp_%s_%s.json", trimmedSessionID, checkpoint.ID))
	if err := writeCheckpointPayload(checkpoint.SnapshotPath, checkpointPayload{Checkpoint: checkpoint, Document: session.Document.Clone()}); err != nil {
		return nil, err
	}
	session.Checkpoints = append(session.Checkpoints, checkpoint)
	sort.Slice(session.Checkpoints, func(left int, right int) bool {
		return session.Checkpoints[left].CreatedAt.Before(session.Checkpoints[right].CreatedAt)
	})
	return cloneSessionCheckpoint(&checkpoint), nil
}

func (s *Service) ListCheckpoints(sessionID string) ([]SessionCheckpoint, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		return nil, err
	}
	session.Checkpoints = s.listSessionCheckpointsLocked(trimmedSessionID)
	return cloneSessionCheckpoints(session.Checkpoints), nil
}

func (s *Service) RestoreCheckpoint(sessionID string, checkpointID string) (*Document, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	trimmedCheckpointID := strings.TrimSpace(checkpointID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if trimmedCheckpointID == "" {
		return nil, fmt.Errorf("checkpoint_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		return nil, err
	}
	payload, err := readCheckpointPayload(filepath.Join(s.snapshotDir, fmt.Sprintf("cp_%s_%s.json", trimmedSessionID, trimmedCheckpointID)))
	if err != nil {
		return nil, err
	}
	if payload.Document == nil {
		return nil, fmt.Errorf("checkpoint document is nil")
	}
	restored := payload.Document.Clone()
	applyResolvedNumbering(restored)
	before := session.Document.Clone()
	previousBefore := session.Previous.Clone()
	session.Previous = session.Document.Clone()
	session.Document = restored
	session.LastChangeHints = nil
	session.UpdatedAt = time.Now()
	session.Checkpoints = s.listSessionCheckpointsLocked(trimmedSessionID)
	if err := s.SaveSnapshot(session); err != nil {
		session.Document = before
		session.Previous = previousBefore
		session.LastChangeHints = nil
		session.UpdatedAt = time.Now()
		return nil, fmt.Errorf("persist session snapshot after restore checkpoint: %w", err)
	}
	return restored.Clone(), nil
}

func (s *Service) PlanActionBatch(sessionID string, actions []EditorAction) (*ActionBatch, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if len(actions) == 0 {
		return nil, fmt.Errorf("actions are required")
	}

	snapshot, err := s.Snapshot(trimmedSessionID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Document == nil {
		return nil, fmt.Errorf("document is nil")
	}

	working := snapshot.Document.Clone()
	batch := &ActionBatch{
		ID:              uuid.NewString(),
		SessionID:       trimmedSessionID,
		Status:          "ready",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		InitialRevision: snapshot.Document.Revision,
		Actions:         append([]EditorAction(nil), actions...),
		Plans:           make([]*ActionPlanResult, 0, len(actions)),
		Results:         make([]BatchStepResult, 0, len(actions)),
	}

	for index, action := range actions {
		plan := buildActionPlan(working, action)
		plan.AnchorHealth = diagnoseAnchorHealth(action, plan)
		plan = finalizeActionPlanResult(plan)
		batch.Plans = append(batch.Plans, plan)
		step := BatchStepResult{Index: index, Status: plan.Status, StatusText: plan.StatusText, Plan: plan}
		batch.Results = append(batch.Results, step)
		if plan.Status == "failed" {
			batch.Status = "failed"
			batch.FailureStep = index
			batch.RecoveryPlan = cloneFailureRecoveryPlan(diagnosticsRecoveryPlan(plan.Diagnostics))
			break
		}
		if plan.Status == "noop" || action.PreviewOnly || len(plan.Operations) == 0 {
			continue
		}
		if _, applyErr := ApplyOperationsWithHints(working, plan.Operations); applyErr != nil {
			diagnostics := buildExecutionDiagnostics(applyErr, plan)
			step.Status = "failed"
			step.StatusText = applyErr.Error()
			step.ExecuteResult = finalizeActionExecuteResult(&ActionExecuteResult{
				Status:       "failed",
				StatusText:   applyErr.Error(),
				ReplayAction: plan.ReplayAction,
				Plan:         plan,
				Diagnostics:  diagnostics,
				Validation:   ActionValidation{OK: false, Message: applyErr.Error(), ErrorCode: diagnosticCode(diagnostics), Diagnostics: diagnostics},
				AnchorHealth: cloneAnchorHealth(plan.AnchorHealth),
			})
			batch.Results[index] = step
			batch.Status = "failed"
			batch.FailureStep = index
			batch.RecoveryPlan = cloneFailureRecoveryPlan(diagnosticsRecoveryPlan(diagnostics))
			break
		}
	}

	batch.PlannedRevision = working.Revision
	s.mu.Lock()
	s.batches[batch.ID] = cloneActionBatch(batch)
	s.mu.Unlock()
	return cloneActionBatch(batch), nil
}

func (s *Service) ExecuteActionBatch(sessionID string, batchID string) (*ActionBatch, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	trimmedBatchID := strings.TrimSpace(batchID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if trimmedBatchID == "" {
		return nil, fmt.Errorf("batch_id is required")
	}

	s.mu.Lock()
	batch, ok := s.batches[trimmedBatchID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("batch not found: %s", trimmedBatchID)
	}
	if batch.SessionID != trimmedSessionID {
		s.mu.Unlock()
		return nil, fmt.Errorf("batch session mismatch")
	}
	workingBatch := cloneActionBatch(batch)
	workingBatch.Status = "executing"
	workingBatch.UpdatedAt = time.Now()
	s.batches[trimmedBatchID] = cloneActionBatch(workingBatch)
	s.mu.Unlock()

	snapshot, err := s.Snapshot(trimmedSessionID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Document == nil {
		return nil, fmt.Errorf("document is nil")
	}
	if snapshot.Document.Revision != workingBatch.InitialRevision {
		diagnostics := &ActionDiagnostics{ErrorCode: "REVISION_CONFLICT", Message: fmt.Sprintf("batch expected revision %d, current %d", workingBatch.InitialRevision, snapshot.Document.Revision), Retryable: true}
		diagnostics.RecoveryPlan = buildFailureRecoveryPlan(diagnostics, EditorAction{}, nil)
		workingBatch.Status = "failed"
		workingBatch.RecoveryPlan = cloneFailureRecoveryPlan(diagnosticsRecoveryPlan(diagnostics))
		workingBatch.UpdatedAt = time.Now()
		s.mu.Lock()
		s.batches[trimmedBatchID] = cloneActionBatch(workingBatch)
		s.mu.Unlock()
		return workingBatch, nil
	}

	checkpoint, err := s.SaveCheckpoint(trimmedSessionID, fmt.Sprintf("batch:%s", workingBatch.ID))
	if err != nil {
		return nil, err
	}
	workingBatch.CheckpointID = checkpoint.ID

	for index, action := range workingBatch.Actions {
		var executeResult *ActionExecuteResult
		var executeErr error
		plannedStep := (*ActionPlanResult)(nil)
		if index < len(workingBatch.Plans) {
			plannedStep = workingBatch.Plans[index]
		}
		if plannedStep != nil && plannedStep.Status == "ready" {
			applyResult, applyErr := s.ApplyWithOptions(trimmedSessionID, plannedStep.Operations, ApplyOptions{ExpectedRevision: plannedStep.ExpectedRevision})
			if applyErr != nil {
				executeErr = applyErr
				diagnostics := buildExecutionDiagnostics(applyErr, plannedStep)
				executeResult = finalizeActionExecuteResult(&ActionExecuteResult{
					Status:       "failed",
					StatusText:   applyErr.Error(),
					ReplayAction: plannedStep.ReplayAction,
					Plan:         plannedStep,
					Diagnostics:  diagnostics,
					Validation:   ActionValidation{OK: false, Message: applyErr.Error(), ErrorCode: diagnosticCode(diagnostics), Diagnostics: diagnostics},
					AnchorHealth: cloneAnchorHealth(plannedStep.AnchorHealth),
				})
			} else {
				activeParagraphID := plannedStep.ActiveParagraphID
				if activeParagraphID == "" {
					activeParagraphID = resolveFocusParagraphID(applyResult.Diffs, "")
				}
				validation := ValidateActionResult(applyResult, plannedStep.ReplayAction, activeParagraphID)
				status := "applied"
				if !validation.OK {
					status = "applied_with_warning"
				}
				executeResult = finalizeActionExecuteResult(&ActionExecuteResult{
					Status:            status,
					StatusText:        plannedStep.StatusText,
					Document:          applyResult.Document,
					Diffs:             applyResult.Diffs,
					ActiveParagraphID: activeParagraphID,
					ReplayAction:      plannedStep.ReplayAction,
					Plan:              plannedStep,
					Validation:        validation,
					AnchorHealth:      cloneAnchorHealth(plannedStep.AnchorHealth),
				})
			}
		} else {
			executeResult, executeErr = s.ExecutePlannedAction(trimmedSessionID, action)
		}
		step := BatchStepResult{Index: index, Status: "applied", ExecuteResult: executeResult}
		if executeResult != nil {
			step.Status = executeResult.Status
			step.StatusText = executeResult.StatusText
			step.Plan = executeResult.Plan
		}
		if executeErr != nil {
			step.Status = "failed"
			step.StatusText = executeErr.Error()
		}
		if len(workingBatch.Results) > index {
			workingBatch.Results[index] = step
		} else {
			workingBatch.Results = append(workingBatch.Results, step)
		}
		if executeErr != nil || executeResult == nil || executeResult.Status == "failed" || !executeResult.Validation.OK {
			workingBatch.Status = "rolled_back"
			workingBatch.FailureStep = index
			if executeResult != nil {
				workingBatch.RecoveryPlan = cloneFailureRecoveryPlan(diagnosticsRecoveryPlan(executeResult.Diagnostics))
			}
			if workingBatch.RecoveryPlan == nil {
				diagnostics := &ActionDiagnostics{ErrorCode: "VALIDATION_FAILED", Message: step.StatusText, Retryable: true}
				workingBatch.RecoveryPlan = cloneFailureRecoveryPlan(diagnosticsRecoveryPlan(diagnostics))
			}
			workingBatch.RecoveryPlan.CheckpointID = checkpoint.ID
			if _, restoreErr := s.RestoreCheckpoint(trimmedSessionID, checkpoint.ID); restoreErr != nil {
				workingBatch.RecoveryPlan.Notes = append(workingBatch.RecoveryPlan.Notes, fmt.Sprintf("restore checkpoint failed: %v", restoreErr))
			}
			workingBatch.UpdatedAt = time.Now()
			s.mu.Lock()
			s.batches[trimmedBatchID] = cloneActionBatch(workingBatch)
			s.mu.Unlock()
			return cloneActionBatch(workingBatch), nil
		}
	}

	workingBatch.Status = "applied"
	workingBatch.UpdatedAt = time.Now()
	s.mu.Lock()
	s.batches[trimmedBatchID] = cloneActionBatch(workingBatch)
	s.mu.Unlock()
	return cloneActionBatch(workingBatch), nil
}

func (s *Service) listSessionCheckpointsLocked(sessionID string) []SessionCheckpoint {
	entries, err := os.ReadDir(s.snapshotDir)
	if err != nil {
		return nil
	}
	prefix := fmt.Sprintf("cp_%s_", sessionID)
	checkpoints := make([]SessionCheckpoint, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		payload, readErr := readCheckpointPayload(filepath.Join(s.snapshotDir, name))
		if readErr != nil {
			continue
		}
		checkpoints = append(checkpoints, payload.Checkpoint)
	}
	sort.Slice(checkpoints, func(left int, right int) bool {
		return checkpoints[left].CreatedAt.Before(checkpoints[right].CreatedAt)
	})
	return checkpoints
}

func writeCheckpointPayload(path string, payload checkpointPayload) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func readCheckpointPayload(path string) (*checkpointPayload, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var payload checkpointPayload
	if err := json.NewDecoder(file).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Document != nil {
		payload.Document.ensureBlockStructure()
	}
	return &payload, nil
}

func cloneSessionCheckpoint(checkpoint *SessionCheckpoint) *SessionCheckpoint {
	if checkpoint == nil {
		return nil
	}
	cloned := *checkpoint
	return &cloned
}

func cloneAnchorHealth(health *AnchorHealth) *AnchorHealth {
	if health == nil {
		return nil
	}
	cloned := *health
	if len(health.Observations) > 0 {
		cloned.Observations = append([]string(nil), health.Observations...)
	}
	return &cloned
}

func cloneFailureRecoveryPlan(plan *FailureRecoveryPlan) *FailureRecoveryPlan {
	if plan == nil {
		return nil
	}
	cloned := *plan
	if len(plan.BackupStrategies) > 0 {
		cloned.BackupStrategies = append([]string(nil), plan.BackupStrategies...)
	}
	if len(plan.Notes) > 0 {
		cloned.Notes = append([]string(nil), plan.Notes...)
	}
	return &cloned
}

func cloneActionBatch(batch *ActionBatch) *ActionBatch {
	if batch == nil {
		return nil
	}
	cloned := *batch
	if len(batch.Actions) > 0 {
		cloned.Actions = append([]EditorAction(nil), batch.Actions...)
	}
	if len(batch.Plans) > 0 {
		cloned.Plans = make([]*ActionPlanResult, len(batch.Plans))
		for index, plan := range batch.Plans {
			cloned.Plans[index] = cloneActionPlanResult(plan)
		}
	}
	if len(batch.Results) > 0 {
		cloned.Results = make([]BatchStepResult, len(batch.Results))
		for index, result := range batch.Results {
			cloned.Results[index] = BatchStepResult{
				Index:         result.Index,
				Status:        result.Status,
				StatusText:    result.StatusText,
				Plan:          cloneActionPlanResult(result.Plan),
				ExecuteResult: cloneActionExecuteResult(result.ExecuteResult),
			}
		}
	}
	cloned.RecoveryPlan = cloneFailureRecoveryPlan(batch.RecoveryPlan)
	return &cloned
}

func cloneActionPlanResult(plan *ActionPlanResult) *ActionPlanResult {
	if plan == nil {
		return nil
	}
	cloned := *plan
	if len(plan.Operations) > 0 {
		cloned.Operations = append([]Operation(nil), plan.Operations...)
	}
	if plan.ResolvedTarget != nil {
		resolved := *plan.ResolvedTarget
		if len(plan.ResolvedTarget.Path) > 0 {
			resolved.Path = append([]string(nil), plan.ResolvedTarget.Path...)
		}
		if len(plan.ResolvedTarget.HeadingScope) > 0 {
			resolved.HeadingScope = append([]string(nil), plan.ResolvedTarget.HeadingScope...)
		}
		cloned.ResolvedTarget = &resolved
	}
	cloned.Diagnostics = cloneActionDiagnostics(plan.Diagnostics)
	cloned.AnchorHealth = cloneAnchorHealth(plan.AnchorHealth)
	return &cloned
}

func cloneActionExecuteResult(result *ActionExecuteResult) *ActionExecuteResult {
	if result == nil {
		return nil
	}
	cloned := *result
	if result.Document != nil {
		cloned.Document = result.Document.Clone()
	}
	if len(result.Diffs) > 0 {
		cloned.Diffs = append([]ParagraphDiff(nil), result.Diffs...)
	}
	cloned.Plan = cloneActionPlanResult(result.Plan)
	cloned.Diagnostics = cloneActionDiagnostics(result.Diagnostics)
	cloned.Validation.Diagnostics = cloneActionDiagnostics(result.Validation.Diagnostics)
	cloned.AnchorHealth = cloneAnchorHealth(result.AnchorHealth)
	return &cloned
}

func cloneActionDiagnostics(diagnostics *ActionDiagnostics) *ActionDiagnostics {
	if diagnostics == nil {
		return nil
	}
	cloned := *diagnostics
	if len(diagnostics.Candidates) > 0 {
		cloned.Candidates = append([]ActionCandidate(nil), diagnostics.Candidates...)
	}
	if len(diagnostics.AttemptedStrategies) > 0 {
		cloned.AttemptedStrategies = append([]string(nil), diagnostics.AttemptedStrategies...)
	}
	if len(diagnostics.RecommendedActions) > 0 {
		cloned.RecommendedActions = append([]string(nil), diagnostics.RecommendedActions...)
	}
	if diagnostics.RetryAction != nil {
		actionCopy := *diagnostics.RetryAction
		cloned.RetryAction = &actionCopy
	}
	cloned.RecoveryPlan = cloneFailureRecoveryPlan(diagnostics.RecoveryPlan)
	return &cloned
}

func diagnosticsRecoveryPlan(diagnostics *ActionDiagnostics) *FailureRecoveryPlan {
	if diagnostics == nil {
		return nil
	}
	return diagnostics.RecoveryPlan
}

func diagnoseAnchorHealth(action EditorAction, plan *ActionPlanResult) *AnchorHealth {
	health := &AnchorHealth{
		Status:            "healthy",
		RecommendedAction: "keep_current_anchor",
	}
	if plan != nil {
		health.ExpectedRevision = plan.ExpectedRevision
		health.CurrentRevision = plan.ExpectedRevision
	}
	observations := make([]string, 0)
	if plan == nil {
		observations = append(observations, "缺少可分析的执行计划")
		health.Status = "unknown"
		health.Observations = observations
		return health
	}
	if action.PreferredParagraphID != "" && strings.TrimSpace(plan.ReplayAction.PreferredParagraphID) != "" && action.PreferredParagraphID != plan.ReplayAction.PreferredParagraphID {
		health.DriftScore += 75
		health.ReplanApplied = true
		observations = append(observations, fmt.Sprintf("preferred_paragraph_id 已从 %s 重绑定到 %s", action.PreferredParagraphID, plan.ReplayAction.PreferredParagraphID))
	}
	if action.PreferredParagraphID != "" && strings.TrimSpace(plan.ReplayAction.PreferredParagraphID) == "" && plan.ResolvedTarget != nil && strings.TrimSpace(plan.ResolvedTarget.ParagraphID) != "" && action.PreferredParagraphID != plan.ResolvedTarget.ParagraphID {
		health.DriftScore += 75
		health.ReplanApplied = true
		observations = append(observations, fmt.Sprintf("preferred_paragraph_id %s 已失效，当前已改用最新段落 %s", action.PreferredParagraphID, plan.ResolvedTarget.ParagraphID))
	}
	if selectorDriftDetected(action.TargetSelector, plan.ReplayAction.TargetSelector) {
		health.DriftScore += 70
		health.ReplanApplied = true
		observations = append(observations, "target_selector 与最新 replay_action 不再一致，已按当前结构重定位")
	}
	if action.TableID != "" && strings.TrimSpace(plan.ReplayAction.TableID) != "" && action.TableID != plan.ReplayAction.TableID {
		health.DriftScore += 70
		health.ReplanApplied = true
		observations = append(observations, fmt.Sprintf("table_id 已从 %s 重绑定到 %s", action.TableID, plan.ReplayAction.TableID))
	}
	if plan.Diagnostics != nil && len(plan.Diagnostics.AttemptedStrategies) > 0 {
		health.DriftScore += 15
		observations = append(observations, "本次计划使用了额外定位策略，说明旧锚点已有漂移迹象")
	}
	if health.DriftScore >= 60 {
		health.IsStale = true
		health.Status = "stale"
		health.RecommendedAction = "replan_and_replay"
		if health.ReplanApplied {
			health.Status = "replanned"
		}
	}
	if len(observations) == 0 {
		observations = append(observations, "当前锚点与最新文档结构一致")
	}
	health.Observations = observations
	return health
}

func selectorDriftDetected(original *ActionTargetSelector, replay *ActionTargetSelector) bool {
	if original == nil || replay == nil {
		return false
	}
	return strings.TrimSpace(original.Type) != strings.TrimSpace(replay.Type) ||
		strings.TrimSpace(original.ParagraphID) != strings.TrimSpace(replay.ParagraphID) ||
		strings.TrimSpace(original.TableID) != strings.TrimSpace(replay.TableID) ||
		original.RowIndex != replay.RowIndex ||
		original.CellIndex != replay.CellIndex ||
		original.HeadingLevel != replay.HeadingLevel ||
		original.Ordinal != replay.Ordinal ||
		strings.TrimSpace(original.Chapter) != strings.TrimSpace(replay.Chapter) ||
		strings.TrimSpace(original.TextEquals) != strings.TrimSpace(replay.TextEquals) ||
		strings.TrimSpace(original.Region) != strings.TrimSpace(replay.Region)
}

func buildFailureRecoveryPlan(diagnostics *ActionDiagnostics, action EditorAction, anchorHealth *AnchorHealth) *FailureRecoveryPlan {
	if diagnostics == nil {
		return nil
	}
	code := strings.ToUpper(strings.TrimSpace(diagnostics.ErrorCode))
	plan := &FailureRecoveryPlan{
		ErrorCode:      code,
		Retryable:      diagnostics.Retryable,
		MaxAutoRetries: 1,
	}
	switch code {
	case "TARGET_NOT_FOUND":
		plan.PrimaryStrategy = "auto_replan"
		plan.BackupStrategies = []string{"refresh_document_context", "manual_prompt"}
		plan.UserPrompt = "目标已发生漂移或不存在，需基于最新文档上下文重新定位。"
	case "AMBIGUOUS_TARGET":
		plan.PrimaryStrategy = "return_candidates"
		if allowBestCandidateResolution(action) {
			plan.PrimaryStrategy = "use_best_candidate"
		}
		plan.BackupStrategies = []string{"narrow_selector_scope", "manual_prompt"}
		plan.UserPrompt = "当前目标存在多个候选，需要根据最新结构继续缩小范围。"
	case "VALIDATION_FAILED":
		plan.PrimaryStrategy = "revalidate_then_replan"
		if action.FailurePolicy != nil && action.FailurePolicy.OnValidationFailed == "rollback_and_report" {
			plan.PrimaryStrategy = "rollback_to_previous"
		}
		plan.BackupStrategies = []string{"refresh_document_context", "manual_prompt"}
		plan.UserPrompt = "写入结果与预期不一致，需要先核对现状再继续。"
	case "REVISION_CONFLICT":
		plan.PrimaryStrategy = "reload_and_replan"
		plan.BackupStrategies = []string{"restore_checkpoint", "manual_prompt"}
		plan.UserPrompt = "文档版本已经变化，需按最新 revision 自动重规划后再执行。"
	case "EXECUTION_FAILED":
		plan.PrimaryStrategy = "reload_and_replan"
		plan.BackupStrategies = []string{"refresh_document_context", "manual_prompt"}
		plan.UserPrompt = "执行失败，需根据最新文档结构重新规划后重试。"
	default:
		plan.PrimaryStrategy = "manual_prompt"
		plan.BackupStrategies = []string{"refresh_document_context"}
		plan.UserPrompt = "需要重新读取文档上下文并补充更精确锚点。"
	}
	if anchorHealth != nil && anchorHealth.IsStale {
		plan.Notes = append(plan.Notes, fmt.Sprintf("anchor drift score=%d", anchorHealth.DriftScore))
		if plan.PrimaryStrategy == "return_candidates" {
			plan.BackupStrategies = append([]string{"auto_replan"}, plan.BackupStrategies...)
		} else if plan.PrimaryStrategy == "manual_prompt" || plan.PrimaryStrategy == "refresh_document_context" {
			plan.PrimaryStrategy = "auto_replan"
		}
	}
	return plan
}

func buildExecutionDiagnostics(err error, plan *ActionPlanResult) *ActionDiagnostics {
	message := ""
	if err != nil {
		message = err.Error()
	}
	code := classifyExecutionErrorCode(message)
	recommended := []string{"检查当前文档 revision 是否变化", "重新获取最新快照后再执行", "若目标已变化，请重新定位后再修改"}
	switch code {
	case "REVISION_CONFLICT":
		recommended = []string{"重新读取最新文档快照", "按最新 revision 重规划当前动作", "如已存在阶段快照，可直接恢复到最近 checkpoint 后继续"}
	case "TARGET_NOT_FOUND":
		recommended = []string{"重新读取最新文档结构", "补充更精确的 selector 或 paragraph_id/table_id", "必要时先恢复到最近 checkpoint 再重新定位"}
	}
	diagnostics := &ActionDiagnostics{
		ErrorCode:          code,
		Message:            message,
		Retryable:          true,
		RecommendedActions: recommended,
	}
	if plan != nil {
		diagnostics.TargetKind = plan.ResolvedTargetKind()
	}
	diagnostics.RecoveryPlan = buildFailureRecoveryPlan(diagnostics, planActionOrZero(plan), diagnoseAnchorHealth(planActionOrZero(plan), plan))
	return diagnostics
}

func classifyExecutionErrorCode(message string) string {
	normalized := strings.ToLower(strings.TrimSpace(message))
	switch {
	case normalized == "":
		return "EXECUTION_FAILED"
	case strings.Contains(normalized, "document revision changed"):
		return "REVISION_CONFLICT"
	case strings.HasPrefix(normalized, "stale "):
		return "REVISION_CONFLICT"
	case strings.Contains(normalized, "not found"):
		return "TARGET_NOT_FOUND"
	default:
		return "EXECUTION_FAILED"
	}
}

func (s *Service) attemptExecuteRecovery(sessionID string, action EditorAction, originalPlan *ActionPlanResult, diagnostics *ActionDiagnostics, anchorHealth *AnchorHealth) *ActionExecuteResult {
	recovery := diagnosticsRecoveryPlan(diagnostics)
	if recovery == nil {
		return nil
	}
	if recovery.AutoRetryCount >= recovery.MaxAutoRetries {
		return nil
	}
	if recovery.PrimaryStrategy != "reload_and_replan" && recovery.PrimaryStrategy != "auto_replan" {
		return nil
	}
	recovery.AutoRetryCount++
	refreshedPlan, err := s.PlanAction(sessionID, action)
	if err != nil {
		recovery.Notes = append(recovery.Notes, fmt.Sprintf("refresh plan failed: %v", err))
		return nil
	}
	refreshedHealth := cloneAnchorHealth(refreshedPlan.AnchorHealth)
	if refreshedHealth == nil {
		refreshedHealth = diagnoseAnchorHealth(action, refreshedPlan)
	}
	refreshedHealth.ReplanApplied = true
	if !refreshedHealth.IsStale {
		refreshedHealth.IsStale = true
		if refreshedHealth.DriftScore < 60 {
			refreshedHealth.DriftScore = 60
		}
	}
	refreshedHealth.Status = "replanned"
	refreshedHealth.RecommendedAction = "use_refreshed_replay_action"
	refreshedHealth.Observations = append(refreshedHealth.Observations, "执行阶段检测到 revision/锚点漂移，已自动重规划一次")
	if refreshedPlan.Status != "ready" {
		return finalizeActionExecuteResult(&ActionExecuteResult{
			Status:       "failed",
			StatusText:   refreshedPlan.StatusText,
			ReplayAction: refreshedPlan.ReplayAction,
			Plan:         refreshedPlan,
			Diagnostics:  refreshedPlan.Diagnostics,
			Validation: ActionValidation{
				OK:          false,
				Message:     diagnosticMessage(refreshedPlan.Diagnostics, refreshedPlan.StatusText),
				ErrorCode:   diagnosticCode(refreshedPlan.Diagnostics),
				Diagnostics: refreshedPlan.Diagnostics,
			},
			AnchorHealth: refreshedHealth,
		})
	}
	applyResult, applyErr := s.ApplyWithOptions(sessionID, refreshedPlan.Operations, ApplyOptions{ExpectedRevision: refreshedPlan.ExpectedRevision})
	if applyErr != nil {
		refreshedDiagnostics := buildExecutionDiagnostics(applyErr, refreshedPlan)
		if refreshedDiagnostics.RecoveryPlan != nil {
			refreshedDiagnostics.RecoveryPlan.AutoRetryCount = recovery.AutoRetryCount
		}
		return finalizeActionExecuteResult(&ActionExecuteResult{
			Status:       "failed",
			StatusText:   applyErr.Error(),
			ReplayAction: refreshedPlan.ReplayAction,
			Plan:         refreshedPlan,
			Diagnostics:  refreshedDiagnostics,
			Validation:   ActionValidation{OK: false, Message: applyErr.Error(), ErrorCode: diagnosticCode(refreshedDiagnostics), Diagnostics: refreshedDiagnostics},
			AnchorHealth: refreshedHealth,
		})
	}
	activeParagraphID := refreshedPlan.ActiveParagraphID
	if activeParagraphID == "" {
		activeParagraphID = resolveFocusParagraphID(applyResult.Diffs, "")
	}
	validation := ValidateActionResult(applyResult, refreshedPlan.ReplayAction, activeParagraphID)
	status := "applied"
	if !validation.OK {
		status = "applied_with_warning"
	}
	message := "检测到 revision/锚点漂移后已自动重规划并执行"
	finalDiagnostics := &ActionDiagnostics{
		ErrorCode:          "RECOVERED_AFTER_REPLAN",
		Message:            message,
		TargetKind:         refreshedPlan.ResolvedTargetKind(),
		Retryable:          false,
		RecommendedActions: []string{"后续步骤请沿用最新 replay_action 和最新 revision", "如需阶段回退，可恢复到最近 checkpoint 再继续"},
		RecoveryPlan:       cloneFailureRecoveryPlan(recovery),
	}
	if finalDiagnostics.RecoveryPlan != nil {
		finalDiagnostics.RecoveryPlan.AutoRetryCount = recovery.AutoRetryCount
	}
	return finalizeActionExecuteResult(&ActionExecuteResult{
		Status:            status,
		StatusText:        message,
		Document:          applyResult.Document,
		Diffs:             applyResult.Diffs,
		ActiveParagraphID: activeParagraphID,
		ReplayAction:      refreshedPlan.ReplayAction,
		Plan:              refreshedPlan,
		Validation:        validation,
		Diagnostics:       finalDiagnostics,
		AnchorHealth:      refreshedHealth,
	})
}

func planActionOrZero(plan *ActionPlanResult) EditorAction {
	if plan == nil {
		return EditorAction{}
	}
	return plan.ReplayAction
}
