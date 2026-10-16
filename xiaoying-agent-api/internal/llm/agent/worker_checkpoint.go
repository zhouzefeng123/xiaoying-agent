package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/message"
)

const (
	workerCheckpointVersion   = "v1"
	workerCheckpointStepLimit = 8
	workerCheckpointTailLimit = 8
)

type WorkerCheckpointStep struct {
	Tool         string `json:"tool,omitempty"`
	InputPreview string `json:"input_preview,omitempty"`
	Status       string `json:"status,omitempty"`
	Summary      string `json:"summary,omitempty"`
}

type WorkerCheckpointMessage struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type WorkerCheckpoint struct {
	Version            string                    `json:"version,omitempty"`
	Role               string                    `json:"role,omitempty"`
	SystemPrompt       string                    `json:"system_prompt,omitempty"`
	TaskID             string                    `json:"task_id,omitempty"`
	ParentSessionID    string                    `json:"parent_session_id,omitempty"`
	OriginalTask       string                    `json:"original_task,omitempty"`
	CurrentTask        string                    `json:"current_task,omitempty"`
	PendingGoal        string                    `json:"pending_goal,omitempty"`
	Status             string                    `json:"status,omitempty"`
	Summary            string                    `json:"summary,omitempty"`
	FailureType        string                    `json:"failure_type,omitempty"`
	LastError          string                    `json:"last_error,omitempty"`
	Retryable          bool                      `json:"retryable,omitempty"`
	ProviderRetryCount int                       `json:"provider_retry_count,omitempty"`
	OutputRetryCount   int                       `json:"output_retry_count,omitempty"`
	ToolRetryCount     int                       `json:"tool_retry_count,omitempty"`
	LastRecoveryNote   string                    `json:"last_recovery_note,omitempty"`
	ResumeHint         string                    `json:"resume_hint,omitempty"`
	Artifacts          []string                  `json:"artifacts,omitempty"`
	ValidArtifacts     []string                  `json:"valid_artifacts,omitempty"`
	MissingArtifacts   []string                  `json:"missing_artifacts,omitempty"`
	EvidenceRefs       []string                  `json:"evidence_refs,omitempty"`
	RecentToolSteps    []WorkerCheckpointStep    `json:"recent_tool_steps,omitempty"`
	HistoryTail        []WorkerCheckpointMessage `json:"history_tail,omitempty"`
	UpdatedAt          int64                     `json:"updated_at,omitempty"`
}

func cloneWorkerCheckpoint(checkpoint *WorkerCheckpoint) *WorkerCheckpoint {
	if checkpoint == nil {
		return nil
	}
	cloned := *checkpoint
	cloned.Artifacts = append([]string(nil), checkpoint.Artifacts...)
	cloned.ValidArtifacts = append([]string(nil), checkpoint.ValidArtifacts...)
	cloned.MissingArtifacts = append([]string(nil), checkpoint.MissingArtifacts...)
	cloned.EvidenceRefs = append([]string(nil), checkpoint.EvidenceRefs...)
	cloned.RecentToolSteps = append([]WorkerCheckpointStep(nil), checkpoint.RecentToolSteps...)
	cloned.HistoryTail = append([]WorkerCheckpointMessage(nil), checkpoint.HistoryTail...)
	return &cloned
}

func newWorkerCheckpoint(role, systemPrompt, taskID, parentSessionID, status string) *WorkerCheckpoint {
	return &WorkerCheckpoint{
		Version:         workerCheckpointVersion,
		Role:            strings.TrimSpace(role),
		SystemPrompt:    strings.TrimSpace(systemPrompt),
		TaskID:          strings.TrimSpace(taskID),
		ParentSessionID: strings.TrimSpace(parentSessionID),
		Status:          strings.TrimSpace(status),
		UpdatedAt:       time.Now().UnixMilli(),
	}
}

func ensureWorkerCheckpoint(meta *workerRuntime, role, systemPrompt string) *WorkerCheckpoint {
	if meta == nil {
		return nil
	}
	if meta.Checkpoint == nil {
		meta.Checkpoint = newWorkerCheckpoint(role, systemPrompt, meta.TaskID, meta.ParentSessionID, meta.Status)
	}
	if meta.Checkpoint.Version == "" {
		meta.Checkpoint.Version = workerCheckpointVersion
	}
	if strings.TrimSpace(meta.Checkpoint.Role) == "" {
		meta.Checkpoint.Role = strings.TrimSpace(role)
	}
	if strings.TrimSpace(meta.Checkpoint.SystemPrompt) == "" {
		meta.Checkpoint.SystemPrompt = strings.TrimSpace(systemPrompt)
	}
	if strings.TrimSpace(meta.Checkpoint.TaskID) == "" {
		meta.Checkpoint.TaskID = strings.TrimSpace(meta.TaskID)
	}
	if strings.TrimSpace(meta.Checkpoint.ParentSessionID) == "" {
		meta.Checkpoint.ParentSessionID = strings.TrimSpace(meta.ParentSessionID)
	}
	return meta.Checkpoint
}

func appendWorkerCheckpointStep(steps []WorkerCheckpointStep, step WorkerCheckpointStep) []WorkerCheckpointStep {
	if strings.TrimSpace(step.Tool) == "" {
		return steps
	}
	step.Tool = strings.TrimSpace(step.Tool)
	step.InputPreview = normalizeWorkerSummaryText(step.InputPreview, 220)
	step.Status = strings.TrimSpace(step.Status)
	step.Summary = normalizeWorkerSummaryText(step.Summary, 220)
	steps = append(steps, step)
	if len(steps) <= workerCheckpointStepLimit {
		return steps
	}
	return append([]WorkerCheckpointStep(nil), steps[len(steps)-workerCheckpointStepLimit:]...)
}

func compactWorkerCheckpointMessageContent(msg message.Message) string {
	content := strings.TrimSpace(msg.Content().String())
	if content == "" {
		return ""
	}
	return normalizeWorkerSummaryText(content, 800)
}

func buildWorkerCheckpointHistoryTail(w *worker) []WorkerCheckpointMessage {
	if w == nil {
		return nil
	}
	history := w.optimizeContext(w.GetHistory())
	if len(history) == 0 {
		return nil
	}
	selected := make([]message.Message, 0, workerCheckpointTailLimit)
	if history[0].Role == message.System {
		selected = append(selected, history[0])
	}
	start := len(history) - (workerCheckpointTailLimit - len(selected))
	if start < len(selected) {
		start = len(selected)
	}
	if start < 0 {
		start = 0
	}
	selected = append(selected, history[start:]...)
	tail := make([]WorkerCheckpointMessage, 0, len(selected))
	for _, msg := range selected {
		content := compactWorkerCheckpointMessageContent(msg)
		if content == "" {
			continue
		}
		tail = append(tail, WorkerCheckpointMessage{Role: strings.ToLower(strings.TrimSpace(string(msg.Role))), Content: content})
	}
	return tail
}

func buildWorkerCheckpointSnapshot(base *WorkerCheckpoint, meta *workerRuntime, w *worker) *WorkerCheckpoint {
	checkpoint := cloneWorkerCheckpoint(base)
	if checkpoint == nil {
		role := ""
		systemPrompt := ""
		if w != nil {
			role = w.role
			systemPrompt = w.systemPrompt
		}
		taskID := ""
		parentSessionID := ""
		status := ""
		if meta != nil {
			taskID = meta.TaskID
			parentSessionID = meta.ParentSessionID
			status = meta.Status
		}
		checkpoint = newWorkerCheckpoint(role, systemPrompt, taskID, parentSessionID, status)
	}
	if meta != nil {
		checkpoint.TaskID = firstNonEmpty(checkpoint.TaskID, meta.TaskID)
		checkpoint.ParentSessionID = firstNonEmpty(checkpoint.ParentSessionID, meta.ParentSessionID)
		checkpoint.Status = firstNonEmpty(strings.TrimSpace(meta.Status), checkpoint.Status)
		checkpoint.FailureType = firstNonEmpty(strings.TrimSpace(meta.FailureType), checkpoint.FailureType)
		checkpoint.LastError = firstNonEmpty(strings.TrimSpace(meta.LastError), checkpoint.LastError)
		checkpoint.Retryable = meta.Retryable || checkpoint.Retryable
		checkpoint.ProviderRetryCount = workerCheckpointMaxInt(checkpoint.ProviderRetryCount, meta.ProviderRetries)
		checkpoint.OutputRetryCount = workerCheckpointMaxInt(checkpoint.OutputRetryCount, meta.OutputRetries)
		checkpoint.ToolRetryCount = workerCheckpointMaxInt(checkpoint.ToolRetryCount, meta.ToolRetries)
		checkpoint.LastRecoveryNote = firstNonEmpty(strings.TrimSpace(meta.LastRecoveryNote), checkpoint.LastRecoveryNote)
		checkpoint.ResumeHint = firstNonEmpty(strings.TrimSpace(checkpoint.ResumeHint), strings.TrimSpace(meta.LastRecoveryNote), strings.TrimSpace(meta.LastError))
		checkpoint.Summary = firstNonEmpty(strings.TrimSpace(meta.StructuredSummary), strings.TrimSpace(meta.LastResult), checkpoint.Summary)
	}
	if w != nil {
		checkpoint.Role = firstNonEmpty(checkpoint.Role, w.role)
		checkpoint.SystemPrompt = firstNonEmpty(checkpoint.SystemPrompt, w.systemPrompt)
		checkpoint.HistoryTail = buildWorkerCheckpointHistoryTail(w)
	}
	checkpoint.Artifacts = compactNonEmpty(checkpoint.Artifacts)
	checkpoint.ValidArtifacts, checkpoint.MissingArtifacts = validateWorkerCheckpointArtifacts(checkpoint.Artifacts)
	checkpoint.EvidenceRefs = compactNonEmpty(checkpoint.EvidenceRefs)
	checkpoint.UpdatedAt = time.Now().UnixMilli()
	return checkpoint
}

func validateWorkerCheckpointArtifacts(artifacts []string) ([]string, []string) {
	valid := make([]string, 0, len(artifacts))
	missing := make([]string, 0)
	for _, artifact := range compactNonEmpty(artifacts) {
		if !filepath.IsAbs(artifact) {
			valid = append(valid, artifact)
			continue
		}
		if _, err := os.Stat(artifact); err != nil {
			missing = append(missing, artifact)
			continue
		}
		valid = append(valid, artifact)
	}
	return valid, missing
}

func workerCheckpointHistoryToMessages(checkpoint *WorkerCheckpoint) []message.Message {
	if checkpoint == nil {
		return nil
	}
	history := make([]message.Message, 0, len(checkpoint.HistoryTail)+1)
	hasSystem := false
	for _, item := range checkpoint.HistoryTail {
		role := message.MessageRole(strings.TrimSpace(item.Role))
		if role == "" {
			role = message.User
		}
		if role == message.System {
			hasSystem = true
		}
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		history = append(history, message.NewTextMessage(role, content))
	}
	if !hasSystem && strings.TrimSpace(checkpoint.SystemPrompt) != "" {
		history = append([]message.Message{message.NewTextMessage(message.System, checkpoint.SystemPrompt)}, history...)
	}
	if len(history) == 0 && strings.TrimSpace(checkpoint.SystemPrompt) != "" {
		history = append(history, message.NewTextMessage(message.System, checkpoint.SystemPrompt))
	}
	return history
}

func buildWorkerResumePrompt(checkpoint *WorkerCheckpoint, overrideTask string) string {
	if checkpoint == nil {
		return strings.TrimSpace(overrideTask)
	}
	lines := []string{"SYSTEM CHECKPOINT: Resume the interrupted worker task from the checkpointed state below. Continue from current progress instead of restarting from scratch."}
	originalTask := firstNonEmpty(strings.TrimSpace(checkpoint.OriginalTask), strings.TrimSpace(overrideTask))
	if originalTask != "" {
		lines = append(lines, "Original task: "+originalTask)
	}
	if checkpoint.Summary != "" {
		lines = append(lines, "Latest summary: "+checkpoint.Summary)
	}
	if checkpoint.PendingGoal != "" {
		lines = append(lines, "Pending goal: "+checkpoint.PendingGoal)
	}
	if checkpoint.FailureType != "" {
		lines = append(lines, "Failure type: "+checkpoint.FailureType)
	}
	if checkpoint.LastError != "" {
		lines = append(lines, "Last error: "+checkpoint.LastError)
	}
	if checkpoint.LastRecoveryNote != "" {
		lines = append(lines, "Recovery note: "+checkpoint.LastRecoveryNote)
	}
	if checkpoint.ResumeHint != "" {
		lines = append(lines, "Resume hint: "+checkpoint.ResumeHint)
	}
	if len(checkpoint.Artifacts) > 0 {
		lines = append(lines, "Known artifacts:")
		for _, artifact := range checkpoint.Artifacts {
			lines = append(lines, "- "+artifact)
		}
	}
	if len(checkpoint.ValidArtifacts) > 0 {
		lines = append(lines, "Validated artifacts:")
		for _, artifact := range checkpoint.ValidArtifacts {
			lines = append(lines, "- "+artifact)
		}
	}
	if len(checkpoint.MissingArtifacts) > 0 {
		lines = append(lines, "Missing artifacts to recreate or revalidate:")
		for _, artifact := range checkpoint.MissingArtifacts {
			lines = append(lines, "- "+artifact)
		}
	}
	if len(checkpoint.RecentToolSteps) > 0 {
		lines = append(lines, "Recent tool steps:")
		for _, step := range checkpoint.RecentToolSteps {
			line := fmt.Sprintf("- %s [%s]", step.Tool, step.Status)
			if step.InputPreview != "" {
				line += " " + step.InputPreview
			}
			if step.Summary != "" {
				line += " => " + step.Summary
			}
			lines = append(lines, line)
		}
	}
	if strings.TrimSpace(overrideTask) != "" && strings.TrimSpace(overrideTask) != strings.TrimSpace(checkpoint.OriginalTask) {
		lines = append(lines, "Additional user instruction: "+strings.TrimSpace(overrideTask))
	}
	lines = append(lines, "Instruction: continue the SAME task from this checkpoint. Do not repeat already completed work unless needed for recovery. Reuse valid artifacts and evidence. Emit a final <worker_result> block when done.")
	return strings.Join(lines, "\n")
}

func workerCheckpointMaxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
