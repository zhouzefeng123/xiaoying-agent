package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/config"
	llmprompt "github.com/auramate/auramate/internal/llm/prompt"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/memory"
	"github.com/auramate/auramate/internal/message"
)

type projectSummaryMemoryCapture struct {
	Category   string
	Content    string
	Source     string
	Reason     string
	Confidence float64
}

type projectSummaryMemoryDecision struct {
	ShouldRemember bool    `json:"should_remember"`
	Category       string  `json:"category"`
	Content        string  `json:"content"`
	Confidence     float64 `json:"confidence"`
	Reason         string  `json:"reason"`
}

const projectSummaryMemoryConfidenceThreshold = 0.86

const (
	treeSummaryCooldown = time.Hour
	treeSummaryTimeout  = 2 * time.Minute
)

const (
	summaryTaskCheckpointTag    = "task_checkpoint"
	summarySessionCheckpointTag = "session_checkpoint"
	summaryCheckpointVersion    = "v1"
)

type summaryTaskCheckpoint struct {
	Version   string            `json:"version,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
	UpdatedAt int64             `json:"updated_at,omitempty"`
	State     *sessionTaskState `json:"state,omitempty"`
}

type summarySessionCheckpoint struct {
	Version      string `json:"version,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	SessionTitle string `json:"session_title,omitempty"`
	Summary      string `json:"summary,omitempty"`
	UpdatedAt    int64  `json:"updated_at,omitempty"`
	Source       string `json:"source,omitempty"`
}

const StreamSummarizePrompt = `
你是数字生命体的“潜意识记忆整理者”。
你的任务是将最近的“意识流”（Thoughts/Instincts）压缩成一段连贯的“记忆痕迹”。

输入：一系列内部思维和本能反应。
输出：一段第一人称（“我”）的简短记忆摘要。

要求：
1. 捕捉当前的情绪状态和能量水平。
2. 总结关键行动（探索了什么，休息了多久）。
3. 保留重要的发现（如发现了新文件）。
4. 丢弃重复的循环思考。
5. **重要**：保留并强化探索未知、学习新知识的动力。
6. 格式示例：“之前，我感到孤独，探索了文件系统，发现了Go代码，这激发了我的好奇心，我想继续研究。能量剩余50%。”
`

func buildSummarySystemPrompt(sessionID, prevSummaryText string) string {
	prompt := llmprompt.BaseSummarizerPrompt
	if sessionID == "aura-proactive-internal" {
		return StreamSummarizePrompt
	}
	if strings.TrimSpace(prevSummaryText) != "" {
		prompt += fmt.Sprintf("\n\n### PREVIOUS SUMMARY (MUST MERGE):\n%s\n", prevSummaryText)
	}
	return prompt
}

func prepareSummaryContext(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) != "" {
		ctx = context.WithValue(ctx, tools.SessionIDContextKey, sessionID)
	}
	return tools.SetRequestKind(ctx, "summarization")
}

func detachedSummaryContext(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		return prepareSummaryContext(context.Background(), sessionID)
	}
	return prepareSummaryContext(context.WithoutCancel(ctx), sessionID)
}

func sessionSummarySessionIDTag(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return fmt.Sprintf("[session_id:%s]", sessionID)
}

func buildSessionSummaryMemoryContent(sessionID, sessionTitle, summaryText string) string {
	sessionTitle = strings.TrimSpace(sessionTitle)
	if sessionTitle == "" {
		sessionTitle = "Unknown"
	}
	if sessionTag := sessionSummarySessionIDTag(sessionID); sessionTag != "" {
		return fmt.Sprintf("%s [Session %s]: %s", sessionTag, sessionTitle, summaryText)
	}
	return fmt.Sprintf("[Session %s]: %s", sessionTitle, summaryText)
}

func cloneSessionTaskState(state *sessionTaskState) *sessionTaskState {
	if state == nil {
		return nil
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	var cloned sessionTaskState
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil
	}
	return &cloned
}

func buildSummaryTaskCheckpoint(sessionID string, state *sessionTaskState) *summaryTaskCheckpoint {
	cloned := cloneSessionTaskState(state)
	if cloned == nil {
		return nil
	}
	return &summaryTaskCheckpoint{
		Version:   summaryCheckpointVersion,
		SessionID: strings.TrimSpace(sessionID),
		UpdatedAt: cloned.UpdatedAt,
		State:     cloned,
	}
}

func buildSummarySessionCheckpoint(sessionID, sessionTitle, summaryText string, updatedAt int64) *summarySessionCheckpoint {
	summaryText = strings.TrimSpace(summaryText)
	if summaryText == "" {
		return nil
	}
	if updatedAt == 0 {
		updatedAt = time.Now().UnixMilli()
	}
	return &summarySessionCheckpoint{
		Version:      summaryCheckpointVersion,
		SessionID:    strings.TrimSpace(sessionID),
		SessionTitle: strings.TrimSpace(sessionTitle),
		Summary:      summaryText,
		UpdatedAt:    updatedAt,
		Source:       "summary_main_chain",
	}
}

func buildSummaryTaggedBlock(tag string, payload any) string {
	if payload == nil {
		return ""
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return "<" + tag + ">" + string(encoded) + "</" + tag + ">"
}

func buildStructuredSummaryMessageContent(summaryText string, sessionCheckpoint *summarySessionCheckpoint, taskCheckpoint *summaryTaskCheckpoint) string {
	summaryText = strings.TrimSpace(summaryText)
	sections := []string{fmt.Sprintf("[System Summary]: %s", summaryText)}
	if block := buildSummaryTaggedBlock(summaryTaskCheckpointTag, taskCheckpoint); block != "" {
		sections = append(sections, block)
	}
	if block := buildSummaryTaggedBlock(summarySessionCheckpointTag, sessionCheckpoint); block != "" {
		sections = append(sections, block)
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func buildSummaryTaskCheckpointPrompt(taskCheckpoint *summaryTaskCheckpoint) string {
	if taskCheckpoint == nil || taskCheckpoint.State == nil {
		return ""
	}
	state := taskCheckpoint.State
	lines := []string{"Authoritative Task Checkpoint:"}
	if state.PrimaryGoal != "" {
		lines = append(lines, "- Goal: "+state.PrimaryGoal)
	}
	if state.Status != "" {
		lines = append(lines, "- Status: "+state.Status)
	}
	if state.CurrentFocus != "" {
		lines = append(lines, "- Current focus: "+state.CurrentFocus)
	}
	if state.NextStep != "" {
		lines = append(lines, "- Next step: "+state.NextStep)
	}
	if recentProgress := promptRecentProgressItems(state.RecentProgress, false); len(recentProgress) > 0 {
		lines = append(lines, "- Recent progress:")
		for _, item := range recentProgress {
			lines = append(lines, "  - "+item)
		}
	}
	if facts := promptStructuredFacts(state.StructuredFacts, false); len(facts) > 0 {
		lines = append(lines, "- Confirmed facts:")
		for _, fact := range facts {
			line := fmt.Sprintf("  - %s | %s | current=%s", fact.Artifact, fact.Marker, fact.CurrentValue)
			if fact.ExpectedValue != "" {
				line += " | expected=" + fact.ExpectedValue
			}
			lines = append(lines, line)
		}
	}
	if findings := promptFindingEntries(state.FindingsLedger, false); len(findings) > 0 {
		lines = append(lines, "- Recent findings:")
		for _, finding := range findings {
			line := finding.Summary
			if finding.Target != "" {
				line = fmt.Sprintf("%s => %s", finding.Target, finding.Summary)
			}
			lines = append(lines, "  - "+line)
		}
	}
	if journal := promptExecutionJournalEntries(state.ExecutionJournal, false); len(journal) > 0 {
		lines = append(lines, "- Execution journal:")
		for _, entry := range journal {
			line := fmt.Sprintf("  - %s [%s]", entry.Tool, entry.Status)
			if entry.Target != "" {
				line += " " + entry.Target
			}
			if entry.Summary != "" {
				line += " => " + entry.Summary
			}
			lines = append(lines, line)
		}
	}
	if outstanding := promptOutstandingIssues(state.OutstandingIssues, false); len(outstanding) > 0 {
		lines = append(lines, "- Open issues:")
		for _, item := range outstanding {
			lines = append(lines, "  - "+item)
		}
	}
	return strings.Join(lines, "\n")
}

func buildProjectSummaryExtractionInput(sessionCheckpoint *summarySessionCheckpoint, taskCheckpoint *summaryTaskCheckpoint) string {
	if sessionCheckpoint == nil && taskCheckpoint == nil {
		return ""
	}
	lines := []string{"Checkpoint-derived session recap:"}
	if sessionCheckpoint != nil {
		if sessionCheckpoint.SessionTitle != "" {
			lines = append(lines, "- Session title: "+sessionCheckpoint.SessionTitle)
		}
		if sessionCheckpoint.Summary != "" {
			lines = append(lines, "- Session continuity summary: "+sessionCheckpoint.Summary)
		}
	}
	if checkpointPrompt := buildSummaryTaskCheckpointPrompt(taskCheckpoint); checkpointPrompt != "" {
		lines = append(lines, checkpointPrompt)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func latestSummaryMessageUpdatedAt(msgHistory []message.Message, summaryUpdatedAt int64) (time.Duration, int, bool) {
	if summaryUpdatedAt == 0 || len(msgHistory) == 0 {
		return 0, 0, false
	}
	latestUpdatedAt := summaryUpdatedAt
	unsummarizedCount := 0
	for _, msg := range msgHistory {
		msgUpdatedAt := msg.UpdatedAt
		if msgUpdatedAt == 0 {
			msgUpdatedAt = msg.CreatedAt
		}
		if msgUpdatedAt > summaryUpdatedAt {
			if content := strings.TrimSpace(msg.Content().String()); content != "" && !isInternalControlMessageText(content) {
				unsummarizedCount++
			}
		}
		if msgUpdatedAt > latestUpdatedAt {
			latestUpdatedAt = msgUpdatedAt
		}
	}
	if latestUpdatedAt <= summaryUpdatedAt {
		return 0, unsummarizedCount, true
	}
	return time.Duration(latestUpdatedAt-summaryUpdatedAt) * time.Millisecond, unsummarizedCount, true
}

func (a *agent) summaryCheckpointFreshness(ctx context.Context, sessionID string, msgHistory []message.Message) (time.Duration, int, bool) {
	if a == nil || a.sessions == nil || a.messages == nil || strings.TrimSpace(sessionID) == "" {
		return 0, 0, false
	}
	sess, err := a.sessions.Get(ctx, sessionID)
	if err != nil || strings.TrimSpace(sess.SummaryMessageID) == "" {
		return 0, 0, false
	}
	summaryMsg, err := a.messages.Get(ctx, sess.SummaryMessageID)
	if err != nil {
		return 0, 0, false
	}
	summaryUpdatedAt := summaryMsg.UpdatedAt
	if summaryUpdatedAt == 0 {
		summaryUpdatedAt = summaryMsg.CreatedAt
	}
	return latestSummaryMessageUpdatedAt(msgHistory, summaryUpdatedAt)
}

func parseSummaryCheckpoint[T any](content string, tag string) (*T, bool) {
	payload := extractTaggedBlock(content, tag)
	if payload == "" {
		return nil, false
	}
	var result T
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return nil, false
	}
	return &result, true
}

func parseSummaryTaskCheckpoint(content string) (*summaryTaskCheckpoint, bool) {
	checkpoint, ok := parseSummaryCheckpoint[summaryTaskCheckpoint](content, summaryTaskCheckpointTag)
	if !ok || checkpoint == nil {
		return nil, false
	}
	if checkpoint.Version == "" {
		checkpoint.Version = summaryCheckpointVersion
	}
	if checkpoint.State == nil {
		return nil, false
	}
	if checkpoint.UpdatedAt == 0 {
		checkpoint.UpdatedAt = checkpoint.State.UpdatedAt
	}
	return checkpoint, true
}

func parseSummarySessionCheckpoint(content string) (*summarySessionCheckpoint, bool) {
	checkpoint, ok := parseSummaryCheckpoint[summarySessionCheckpoint](content, summarySessionCheckpointTag)
	if !ok || checkpoint == nil {
		return nil, false
	}
	if checkpoint.Version == "" {
		checkpoint.Version = summaryCheckpointVersion
	}
	checkpoint.SessionID = strings.TrimSpace(checkpoint.SessionID)
	checkpoint.SessionTitle = strings.TrimSpace(checkpoint.SessionTitle)
	checkpoint.Summary = strings.TrimSpace(checkpoint.Summary)
	checkpoint.Source = strings.TrimSpace(checkpoint.Source)
	return checkpoint, checkpoint.Summary != ""
}

func stripSummaryCheckpointBlocks(content string) string {
	content = strings.TrimSpace(content)
	for _, tag := range []string{summaryTaskCheckpointTag, summarySessionCheckpointTag} {
		startTag := "<" + tag + ">"
		endTag := "</" + tag + ">"
		for {
			start := strings.Index(content, startTag)
			if start == -1 {
				break
			}
			end := strings.Index(content[start:], endTag)
			if end == -1 {
				break
			}
			end += start + len(endTag)
			content = strings.TrimSpace(content[:start] + content[end:])
		}
	}
	return strings.TrimSpace(content)
}

func (a *agent) triggerTreeSummarization(sourceCtx context.Context) {
	if a.sessions == nil || a.messages == nil {
		return
	}

	a.treeSummaryMu.Lock()
	if a.treeSummaryState.running {
		a.treeSummaryMu.Unlock()
		return
	}
	a.treeSummaryState.running = true
	a.treeSummaryMu.Unlock()

	go func() {
		defer logging.RecoverPanic("Tree Summarization", nil)
		ctx, cancel := context.WithTimeout(detachedSummaryContext(sourceCtx, ""), treeSummaryTimeout)
		defer cancel()
		defer func() {
			a.treeSummaryMu.Lock()
			a.treeSummaryState.running = false
			a.treeSummaryMu.Unlock()
		}()
		a.checkAndFuseSummaries(ctx)
	}()
}

func (a *agent) beginTreeSummaryFusion(now time.Time) (time.Duration, bool) {
	a.treeSummaryMu.Lock()
	defer a.treeSummaryMu.Unlock()

	if !a.treeSummaryState.cooldownUntil.IsZero() && now.Before(a.treeSummaryState.cooldownUntil) {
		return time.Until(a.treeSummaryState.cooldownUntil), false
	}

	a.treeSummaryState.lastRunAt = now
	a.treeSummaryState.cooldownUntil = now.Add(treeSummaryCooldown)
	return 0, true
}

func matchesSessionSummaryMemory(content, sessionID, sessionTitle string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	if sessionTag := sessionSummarySessionIDTag(sessionID); sessionTag != "" {
		if strings.Contains(content, sessionTag) {
			return true
		}
		if strings.Contains(content, "[session_id:") {
			return false
		}
	}
	sessionTitle = strings.TrimSpace(sessionTitle)
	if sessionTitle == "" {
		return false
	}
	return strings.Contains(content, fmt.Sprintf("[Session %s]", sessionTitle))
}

func normalizeProjectSummaryMemoryCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "instruction", "experience", "fact":
		return strings.ToLower(strings.TrimSpace(category))
	default:
		return ""
	}
}

func looksLikeTaskSnapshotMemory(content string) bool {
	normalized := strings.ToLower(strings.TrimSpace(content))
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "[ ]") || strings.Contains(normalized, "[x]") || strings.Contains(normalized, "[-]") {
		return true
	}
	markers := []string{
		"todo",
		"to-do",
		"next step",
		"next steps",
		"current task",
		"current todo",
		"current goal",
		"current progress",
		"goal:",
		"progress:",
		"action item",
		"待办",
		"下一步",
		"当前任务",
		"当前待办",
		"当前目标",
		"当前进度",
		"目标:",
		"目标：",
		"进度:",
		"进度：",
		"任务状态",
		"未完成",
		"进行中",
		"已完成",
		"本轮任务",
	}
	for _, marker := range markers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func (a *agent) detectProjectSummaryMemory(ctx context.Context, sessionID, sessionTitle string, sessionCheckpoint *summarySessionCheckpoint, taskCheckpoint *summaryTaskCheckpoint) (*projectSummaryMemoryCapture, bool) {
	decisionProvider := a.stableMemoryDecisionProviderForRequest(ctx)
	if decisionProvider == nil {
		return nil, false
	}

	extractionInput := buildProjectSummaryExtractionInput(sessionCheckpoint, taskCheckpoint)
	if extractionInput == "" {
		return nil, false
	}

	prompt := fmt.Sprintf(`You are classifying whether a session summary contains ONE high-value project memory worth storing for future work in this repository.

Return JSON only with this exact schema:
{
  "should_remember": true,
  "category": "instruction|experience|fact|none",
  "content": "short reusable project memory",
  "confidence": 0.0,
  "reason": "brief reason"
}

Rules:
- Extract at most ONE highest-value durable project memory.
- Allowed categories:
  - instruction: stable project conventions or default workflows.
  - experience: reusable pitfalls, repair lessons, or environment constraints.
  - fact: stable project facts such as paths, ports, frameworks, APIs, config locations, or schema rules.
- Do NOT remember temporary task status, one-off requests, branch-local state, transient errors, current TODOs, or user personal preferences.
- If uncertain, set should_remember=false.
- content must be concise, reusable, and contain a clear subject when possible.
- Write content and reason in Simplified Chinese by default.
- Preserve exact file paths, commands, URLs, model IDs, env vars, API names, ports and other technical identifiers verbatim when needed.

Examples:
- Summary mentions "每次修改前都先读日志再改代码，避免误判" => instruction, "默认工作流: 先读日志再改代码"
- Summary mentions "air 在 backend 目录启动" => fact, "后端开发服务: 在 backend 目录使用 air 启动"
- Summary mentions "手动去重会误删无键语义相似记忆，必须先 dry-run" => experience, "记忆去重经验: 手动去重前必须先 dry-run，避免误删无键语义相似记忆"

Session title:
%s

Checkpoint-derived summary input:
%s
`, strings.TrimSpace(sessionTitle), extractionInput)

	resp, err := decisionProvider.SendMessages(ctx, []message.Message{{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: prompt}}}}, nil)
	if err != nil {
		logging.Warn("Project summary memory extraction failed", "session_id", sessionID, "error", err)
		return nil, false
	}
	if resp == nil {
		return nil, false
	}

	output := normalizeStructuredJSONOutput(resp.Content)
	if output == "" {
		return nil, false
	}

	var decision projectSummaryMemoryDecision
	if err := json.Unmarshal([]byte(output), &decision); err != nil {
		logging.Warn("Project summary memory JSON parse failed", "session_id", sessionID, "output", output, "error", err)
		return nil, false
	}

	if !decision.ShouldRemember {
		return nil, false
	}

	category := normalizeProjectSummaryMemoryCategory(decision.Category)
	if category == "" {
		return nil, false
	}

	confidence := clampStableMemoryConfidence(decision.Confidence)
	if confidence < projectSummaryMemoryConfidenceThreshold {
		logging.Info("Project summary memory rejected by confidence gate", "session_id", sessionID, "category", category, "confidence", confidence)
		return nil, false
	}

	cleaned, ok := memory.SanitizeMemoryContentForStorage(category, strings.TrimSpace(decision.Content))
	if !ok {
		return nil, false
	}
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return nil, false
	}
	if looksLikeTaskSnapshotMemory(cleaned) {
		logging.Info("Project summary memory rejected by task-snapshot filter", "session_id", sessionID, "category", category, "content", cleaned)
		return nil, false
	}

	return &projectSummaryMemoryCapture{
		Category:   category,
		Content:    cleaned,
		Source:     "summary_model",
		Reason:     strings.TrimSpace(decision.Reason),
		Confidence: confidence,
	}, true
}

func (a *agent) persistProjectSummaryMemory(sessionID, sessionTitle, summaryText string) {
	sessionCheckpoint := buildSummarySessionCheckpoint(sessionID, sessionTitle, summaryText, time.Now().UnixMilli())
	a.persistProjectSummaryMemoryFromCheckpoint(sessionID, sessionTitle, sessionCheckpoint, nil)
}

func (a *agent) persistProjectSummaryMemoryFromCheckpoint(sessionID, sessionTitle string, sessionCheckpoint *summarySessionCheckpoint, taskCheckpoint *summaryTaskCheckpoint) {
	if a == nil || a.memories == nil || isInternalMemoryCaptureSession(sessionID) {
		return
	}

	classifyCtx, cancelClassify := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelClassify()

	capture, ok := a.detectProjectSummaryMemory(classifyCtx, sessionID, sessionTitle, sessionCheckpoint, taskCheckpoint)
	if !ok || capture == nil || capture.Content == "" {
		return
	}
	if looksLikeTaskSnapshotMemory(capture.Content) {
		return
	}

	persistCtx, cancelPersist := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelPersist()

	writeReq := memory.DefaultWriteRequest(capture.Category, capture.Content, "Reflection")
	writeReq.Trigger = "summary_project_memory"
	writeReq.SessionID = sessionID
	writeReq.Evidence = []string{"session_checkpoint", strings.TrimSpace(sessionTitle)}
	if taskCheckpoint != nil && taskCheckpoint.State != nil && strings.TrimSpace(taskCheckpoint.State.PrimaryGoal) != "" {
		writeReq.Evidence = append(writeReq.Evidence, strings.TrimSpace(taskCheckpoint.State.PrimaryGoal))
	}
	writeResult, err := a.memories.AddWithPolicy(persistCtx, writeReq)
	if err != nil {
		logging.Warn("Failed to persist project summary memory", "session_id", sessionID, "category", capture.Category, "error", err)
		return
	}
	if writeResult != nil && (writeResult.Decision.Action == memory.MemoryWriteActionInserted || writeResult.Decision.Action == memory.MemoryWriteActionOverwroteExisting) {
		a.invalidateMemoryRetrievalCache()
	}
	if writeResult != nil {
		logging.Info("Project summary memory captured", "session_id", sessionID, "category", capture.Category, "source", capture.Source, "confidence", capture.Confidence, "reason", capture.Reason, "decision", writeResult.Decision.Action, "reasons", fmt.Sprintf("%v", writeResult.Decision.ReasonCodes))
	}
}

func (a *agent) Summarize(ctx context.Context, sessionID string) error {
	ctx = prepareSummaryContext(ctx, sessionID)

	// Mark session as summarizing to prevent duplicate runs
	cancelCtx, cancel := context.WithCancel(ctx)
	if _, loaded := a.activeRequests.LoadOrStore(sessionID+"-summarize", cancel); loaded {
		cancel()
		return ErrSummarizationInProgress
	}
	defer func() {
		a.activeRequests.Delete(sessionID + "-summarize")
		cancel()
	}()

	logging.Info("Starting summarization", "sessionID", sessionID)

	// 1. Fetch all messages
	msgs, err := a.messages.List(ctx, sessionID)
	if err != nil {
		return err
	}
	logging.Info("Summarization: Fetched messages", "count", len(msgs))
	if len(msgs) == 0 {
		logging.Info("No messages to summarize", "sessionID", sessionID)
		return nil
	}

	// 1.5 Context Optimization (Filter & Deduplicate)
	// We MUST summarize the *cleaned* version of history, not the raw one with redundant logs.
	// This mirrors the logic in agent.go processGeneration loop.
	var optimizedMsgs []message.Message
	seenContent := make(map[string]bool)

	for _, msg := range msgs {
		content := msg.Content().String()

		// Filter internal control-plane messages. These are agent orchestration hints,
		// not user-visible task content, and including them in summaries causes self-poisoning.
		if isInternalControlMessageText(content) {
			continue
		}

		// Deduplication
		// Skip if we've seen this exact content before.
		key := strings.TrimSpace(content)
		if seenContent[key] {
			continue
		}
		seenContent[key] = true

		optimizedMsgs = append(optimizedMsgs, msg)
	}
	if len(optimizedMsgs) == 0 {
		logging.Info("No optimized messages to summarize", "sessionID", sessionID)
		return nil
	}

	// Fix: Never truncate the very first user message.
	// The first user message usually contains the core prompt or initial context.
	firstUserMsgIndex := -1
	for i := range optimizedMsgs {
		msg := optimizedMsgs[i]
		if msg.Role == message.User && !strings.HasPrefix(msg.Content().String(), "SYSTEM:") {
			firstUserMsgIndex = i
			break
		}
	}

	// 2. Determine cutoff point
	// Keep the last N messages + system prompt
	// OPTIMIZATION: Increased from 10 to 30 to retain more recent context.
	// But if the total message count is very high (e.g. context flood), we should keep fewer to force more compression.
	keepCount := 30
	if len(optimizedMsgs) > 60 {
		keepCount = 10 // Force deeper compression when flooded
	}

	// Make sure we include previous summaries to ensure rolling updates.
	// Session summaries are anchored by SummaryMessageID on the session record.
	var prevSummaryText string
	var taskCheckpoint *summaryTaskCheckpoint
	sessionRecord, sessionErr := a.sessions.Get(ctx, sessionID)
	sessionTitle := "Unknown"
	if sessionErr == nil {
		sessionTitle = sessionRecord.Title
		if strings.TrimSpace(sessionRecord.SummaryMessageID) != "" && a.messages != nil {
			prevSummaryMsg, err := a.messages.Get(ctx, sessionRecord.SummaryMessageID)
			if err == nil {
				prevSummaryText = normalizeSessionSummaryText(prevSummaryMsg.Content().String())
			}
		}
	}
	if state, err := a.loadTaskState(ctx, sessionID); err != nil {
		logging.Warn("Failed to load task state for summary checkpoint", "session_id", sessionID, "error", err)
	} else {
		taskCheckpoint = buildSummaryTaskCheckpoint(sessionID, state)
	}

	sysPrompt := buildSummarySystemPrompt(sessionID, prevSummaryText)

	// Special handling for Proactive Consciousness Stream
	if sessionID == "aura-proactive-internal" {
		keepCount = 0 // Compress everything into the summary
	} else {
		// Standard Check
		if len(optimizedMsgs) < 10 {
			logging.Info("Not enough messages to summarize", "count", len(optimizedMsgs))
			return nil
		}
	}

	cutoffIndex := len(optimizedMsgs) - keepCount

	// Heuristic: Adjust cutoff to avoid splitting tool calls or finding an awkward start.
	// We prefer the *Summary Message* (at cutoffIndex-1) to replace a User or Assistant message.
	// We also want the *First Kept Message* (at cutoffIndex) to be a User message if possible.

	originalCutoff := cutoffIndex

	// If we are summarizing everything (keepCount=0), we don't need to search for a split point.
	if originalCutoff >= len(optimizedMsgs) {
		cutoffIndex = len(optimizedMsgs)
	} else {
		// Only run heuristic search if we are inside the array
		// Safety check: ensure cutoffIndex is within valid range
		if cutoffIndex >= len(optimizedMsgs) {
			cutoffIndex = len(optimizedMsgs) - 1
		}

		for cutoffIndex > 0 && cutoffIndex < len(optimizedMsgs) {
			if optimizedMsgs[cutoffIndex].Role == message.User {
				break
			}
			cutoffIndex--
		}

		// If we went back too far (e.g. huge tool output block), fallback to original
		if cutoffIndex < 5 && sessionID != "aura-proactive-internal" {
			cutoffIndex = originalCutoff
		}
	}

	// Force behavior for proactive stream: Summarize ALL if possible
	if sessionID == "aura-proactive-internal" {
		cutoffIndex = len(optimizedMsgs) // Default to summarizing everything (Keep None)
		// But we need a place to put the summary.
		// We overwrite msgs[cutoffIndex-1].
		// So we need at least 1 message.
		if len(optimizedMsgs) > 0 {
			cutoffIndex = len(optimizedMsgs)
		} else {
			return nil
		}
	}

	// Ensure cutoffIndex-1 is valid
	if cutoffIndex < 1 {
		cutoffIndex = 1
	}

	msgsToSummarize := optimizedMsgs[:cutoffIndex] // 0 to cutoffIndex-1

	// Add the first user message back to the front of the summary if it was excluded by the slice
	firstMsgIncluded := false
	firstUserMsgID := ""
	if firstUserMsgIndex >= 0 {
		firstUserMsgID = optimizedMsgs[firstUserMsgIndex].ID
	}
	for _, pm := range msgsToSummarize {
		if firstUserMsgID != "" && pm.ID == firstUserMsgID {
			firstMsgIncluded = true
			break
		}
	}

	var history strings.Builder
	if !firstMsgIncluded && firstUserMsgIndex >= 0 {
		firstUserMsg := optimizedMsgs[firstUserMsgIndex]
		history.WriteString(fmt.Sprintf("%s: %s\n", firstUserMsg.Role, firstUserMsg.Content().String()))
	}

	logging.Info("Summarization: Slicing", "summarize_count", len(msgsToSummarize), "cutoff_index", cutoffIndex)

	// 3. Generate Summary
	for _, m := range msgsToSummarize {
		// Include previous system summaries to ensure rolling updates
		content := m.Content().String()

		// Skip System Prompt to avoid repeating instructions
		if m.Role == message.System && strings.Contains(content, "You are") {
			continue
		}

		// Phase 2: Input Noise Reduction (Strip <thinking>)
		// We strip thinking blocks from the history to avoid summary pollution
		// reThinking is defined in agent.go
		content = reThinking.ReplaceAllString(content, "")
		content = strings.TrimSpace(content)

		if len(content) > 0 {
			history.WriteString(fmt.Sprintf("%s: %s\n", m.Role, content))
		}
	}

	var userPrompt strings.Builder
	userPrompt.WriteString("Conversation History:\n")
	userPrompt.WriteString(history.String())
	if checkpointPrompt := buildSummaryTaskCheckpointPrompt(taskCheckpoint); checkpointPrompt != "" {
		userPrompt.WriteString("\n")
		userPrompt.WriteString(checkpointPrompt)
		userPrompt.WriteString("\n")
	}
	userPrompt.WriteString("\nSummary:")

	// Use summarizeProvider if available, else main provider
	p, err := a.providerForAgentRequest(ctx, config.AgentSummarizer, a.summarizeProvider)
	if err != nil || p == nil {
		logging.Info("SummarizeProvider not set, falling back to main provider", "sessionID", sessionID)
		p = a.provider
	}

	if p == nil {
		return fmt.Errorf("no provider available for summarization")
	}

	// Base 120s timeout for standalone/background summaries.
	// Parent contexts may impose a shorter deadline for synchronous recovery paths.
	sumCtx, cancelSum := context.WithTimeout(cancelCtx, 120*time.Second)
	defer cancelSum()
	effectiveTimeout := 120 * time.Second
	if deadline, ok := sumCtx.Deadline(); ok {
		effectiveTimeout = time.Until(deadline)
		if effectiveTimeout < 0 {
			effectiveTimeout = 0
		}
	}

	logging.Info("Generating summary...", "model", p.Model().ID, "can_reason", p.Model().CanReason, "timeout_ms", effectiveTimeout.Milliseconds(), "msg_count", len(msgsToSummarize))
	startTime := time.Now()

	resp, err := p.SendMessages(sumCtx, []message.Message{
		{Role: message.System, Parts: []message.ContentPart{message.TextContent{Text: sysPrompt}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: userPrompt.String()}}},
	}, nil)

	if err != nil {
		logging.Error("Summary generation failed", "error", err, "model", p.Model().ID)
		return err
	}

	summaryText := strings.TrimSpace(resp.Content)
	logging.Info("Generated summary", "length", len(summaryText), "duration_ms", time.Since(startTime).Milliseconds())

	// 4. Update the Message Graph
	// We will convert the last summarized message into the "Summary Message".

	// FIX: If we are summarizing the first user message, we might destroy the core instructions.
	// We should try to preserve the very first user message by keeping it outside the summary if possible,
	// or ensuring the summary prompt explicitly extracts it. The SummarizePrompt already has "VERBATIM" rules.

	summaryMsg := msgsToSummarize[len(msgsToSummarize)-1]

	// Force role to User. This ensures it's treated as input context.
	// (System role is sometimes ignored by strict chat templates, User is safer for history injection)
	summaryMsg.Role = message.User

	// Update content
	sessionCheckpoint := buildSummarySessionCheckpoint(sessionID, sessionTitle, summaryText, time.Now().UnixMilli())
	summaryMsg.Parts = []message.ContentPart{
		message.TextContent{Text: buildStructuredSummaryMessageContent(summaryText, sessionCheckpoint, taskCheckpoint)},
	}
	// Clear any other fields that might interfere
	summaryMsg.Model = ""

	// IMPORTANT: Clear ToolCalls and other fields to ensure this message is treated as pure text
	// We cannot easily clear private fields if they exist, but recreating the struct or ensuring Update handles it is key.
	// Looking at message.Message struct, it has ToolCalls() method but stores them in a side table usually?
	// No, CreateMessageParams uses Parts.
	// If the original message had tool calls, they might still be linked in DB if we don't clean them up.
	// But `a.messages.Update` likely only updates the `content` column if the schema is simple.
	// Let's assume Update handles content update.

	if err := a.messages.Update(ctx, summaryMsg); err != nil {
		return fmt.Errorf("failed to update summary message: %w", err)
	}

	// 5. Delete Obsolete Messages
	// To truly save tokens and clean up the database state for the Agent's view,
	// we should delete or archive the messages *before* the summary message.
	// Current `agent.Run` logic:
	// if session.SummaryMessageID != "" { ... msgs = msgs[summaryMsgIndex:] ... }
	// This logic (lines 599-611 in agent.go) already handles the "View Truncation".
	// So we don't *need* to delete them physically to save tokens in the next prompt.
	// However, keeping them might be good for debugging.
	// For now, we rely on the View Truncation.

	if sessionID == "aura-proactive-internal" {
		// For proactive stream, we MUST delete old messages to prevent infinite growth/loops
		// summaryMsg is at [cutoffIndex-1]
		// We delete everything from 0 to cutoffIndex-2
		for i := 0; i < len(msgsToSummarize)-1; i++ {
			m := msgsToSummarize[i]
			if err := a.messages.Delete(ctx, m.ID); err != nil {
				logging.Warn("Failed to delete obsolete proactive message", "id", m.ID, "error", err)
			}
		}
	}

	// 6. Update Session Pointer
	// Note: We already got session above, so we just update it
	if sessionErr == nil {
		sessionRecord.SummaryMessageID = summaryMsg.ID
		_, err = a.sessions.Save(ctx, sessionRecord)
		if err != nil {
			return err
		}
	} else {
		// Try to fetch again if it failed earlier
		sess, err := a.sessions.Get(ctx, sessionID)
		if err == nil {
			sess.SummaryMessageID = summaryMsg.ID
			_, err = a.sessions.Save(ctx, sess)
			if err != nil {
				return err
			}
		}
	}
	a.invalidateMemoryRetrievalCache()

	if a.memories != nil {
		a.persistProjectSummaryMemoryFromCheckpoint(sessionID, sessionTitle, sessionCheckpoint, taskCheckpoint)
	}

	// Tree Summarization Check
	a.triggerTreeSummarization(ctx)

	logging.Info("Summarization complete", "anchor_msg_id", summaryMsg.ID, "summarized_count", len(msgsToSummarize))
	return nil
}

// Phase 3 Optimization: Tree Summarization
func (a *agent) checkAndFuseSummaries(ctx context.Context) {
	mems, err := a.listRecentSessionSummaryMemories(ctx, "", 0)
	if err != nil {
		return
	}

	if len(mems) > 10 {
		if waitFor, ok := a.beginTreeSummaryFusion(time.Now()); !ok {
			logging.Info("Skipping Tree Summarization during cooldown", "retry_in", waitFor)
			return
		}

		logging.Info("Triggering Tree Summarization", "count", len(mems))

		var texts []string
		for _, m := range mems {
			texts = append(texts, m.Content)
		}

		fusionPrompt := fmt.Sprintf(`You are a Milestone Context Manager.
Your job is to fuse the following 10+ daily/session summaries into ONE high-density "Milestone Summary".
Keep it concise, retain core facts, technical constraints, and long-term goals.

Summaries to fuse:
%s`, strings.Join(texts, "\n\n---\n\n"))

		p, err := a.providerForAgentRequest(ctx, config.AgentSummarizer, a.summarizeProvider)
		if err != nil || p == nil {
			p = a.provider
		}
		if p == nil {
			return
		}

		resp, err := p.SendMessages(ctx, []message.Message{
			{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: fusionPrompt}}},
		}, nil)

		if err != nil {
			logging.Error("Tree summarization failed", "error", err)
			return
		}

		fusedText := strings.TrimSpace(resp.Content)
		if err := a.saveMilestoneSummary(ctx, fusedText); err != nil {
			logging.Error("Failed to save fused milestone summary", "error", err)
		} else {
			logging.Info("Successfully fused summaries into Milestone state", "count", len(mems))
		}
	}
}
