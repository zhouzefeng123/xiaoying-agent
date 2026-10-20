package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/prompt"
	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/memory"
	"github.com/auramate/auramate/internal/message"
)

// Constants for context management
const (
	// CharPerToken is a conservative estimate for characters per token.
	// For CJK-heavy content, tokens-per-char is higher, so chars-per-token is LOWER.
	// Use a conservative default; can be overridden by config (see agent.go fallback estimation).
	// Corrected from 1.2 (3-5x overestimate) to 3.5 (more accurate for mixed content)
	CharPerToken = 3.5

	// Emergency Truncation
	EmergencyStubSize        = 200
	EmergencyTruncationRatio = 0.8

	// Hot Context Protection
	MinHotMessages = 20

	// RAG Limits (Balanced)
	MaxL1Items                        = 30
	MaxL2Items                        = 30  // Adjusted from 40
	MaxKnowledgeItems                 = 12  // Adjusted from 15
	MaxPastMessages                   = 20  // Warm-context recall widened for better same-scope hit rate
	MaxTotalMemories                  = 60  // Adjusted from 100
	MaxWarmContextItems               = 20  // Adjusted from 30
	WarmContextTruncation             = 600 // Adjusted from 800
	WarmContextCurrentSessionTailSkip = 6
)

// ContextWindowConfig holds the unified optimal parameters for different context window sizes.
type ContextWindowConfig struct {
	MaxHistoryChars             int
	HotTokenBudgetChars         int
	HistoryWindowMessages       int
	ToolOutputMaxChars          int
	TextCompressionThreshold    int
	LargeToolOutputLogThreshold int
	HeadKeepChars               int
	TailKeepChars               int
	TextHeadKeep                int
}

type ContextStatsSection struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Tokens int64  `json:"tokens"`
}

type ContextStats struct {
	Source                string                 `json:"source"`
	Confidence            string                 `json:"confidence"`
	Model                 string                 `json:"model"`
	CurrentTokens         int64                  `json:"current_tokens"`
	EstimatedTokens       int64                  `json:"estimated_tokens"`
	ObservedPromptTokens  int64                  `json:"observed_prompt_tokens,omitempty"`
	ContextWindowTokens   int64                  `json:"context_window_tokens"`
	MaxTokens             int64                  `json:"max_tokens"`
	HardLimitTokens       int64                  `json:"hard_limit_tokens"`
	SoftLimitTokens       int64                  `json:"soft_limit_tokens"`
	ResponseReserveTokens int64                  `json:"response_reserve_tokens"`
	RemainingTokens       int64                  `json:"remaining_tokens"`
	MessageCount          int                    `json:"message_count"`
	StepCount             int                    `json:"step_count"`
	Sections              []ContextStatsSection  `json:"sections"`
	Assembly              *ContextAssemblyReport `json:"assembly,omitempty"`
}

type ContextAssemblyReport struct {
	CriticalCache                 string `json:"critical_cache,omitempty"`
	DynamicCache                  string `json:"dynamic_cache,omitempty"`
	HistoryWindowSource           string `json:"history_window_source,omitempty"`
	HistoryWindowFallbackReason   string `json:"history_window_fallback_reason,omitempty"`
	HistoryWindowMessages         int    `json:"history_window_messages,omitempty"`
	MemoryCapsule                 bool   `json:"memory_capsule,omitempty"`
	TurnContext                   bool   `json:"turn_context,omitempty"`
	CapsuleMemoryCount            int    `json:"capsule_memory_count,omitempty"`
	TurnMemoryCount               int    `json:"turn_memory_count,omitempty"`
	ContinuityMemoryCount         int    `json:"continuity_memory_count,omitempty"`
	SessionSummaryInjected        bool   `json:"session_summary_injected,omitempty"`
	WarmContextSource             string `json:"warm_context_source,omitempty"`
	WarmContextSessionID          string `json:"warm_context_session_id,omitempty"`
	WarmContextMessages           int    `json:"warm_context_messages,omitempty"`
	WarmContextStatus             string `json:"warm_context_status,omitempty"`
	ImplicitGlobalFallbackSkipped bool   `json:"implicit_global_fallback_skipped,omitempty"`
	WorkingState                  bool   `json:"working_state,omitempty"`
	MutationJournal               bool   `json:"mutation_journal,omitempty"`
	WorkerCheckpointInjected      bool   `json:"worker_checkpoint_injected,omitempty"`
	WorkerCheckpointCount         int    `json:"worker_checkpoint_count,omitempty"`
	DynamicSuffix                 bool   `json:"dynamic_suffix,omitempty"`
}

type historyWindowLoadReport struct {
	Source         string
	FallbackReason string
	MessageCount   int
}

var (
	// ContextConfig128K is optimized for 128K context models (e.g. GPT-4o-mini, Claude 3 Haiku)
	ContextConfig128K = ContextWindowConfig{
		MaxHistoryChars:             400000,
		HotTokenBudgetChars:         60000,
		HistoryWindowMessages:       64,
		ToolOutputMaxChars:          15000,
		TextCompressionThreshold:    10000,
		LargeToolOutputLogThreshold: 5000,
		HeadKeepChars:               2500,
		TailKeepChars:               2500,
		TextHeadKeep:                2500,
	}

	// ContextConfig256K is optimized for 256K context models (e.g. GPT-4o, Claude 3.5 Sonnet)
	ContextConfig256K = ContextWindowConfig{
		MaxHistoryChars:             800000,
		HotTokenBudgetChars:         90000,
		HistoryWindowMessages:       96,
		ToolOutputMaxChars:          30000,
		TextCompressionThreshold:    20000,
		LargeToolOutputLogThreshold: 10000,
		HeadKeepChars:               5000,
		TailKeepChars:               5000,
		TextHeadKeep:                5000,
	}

	// ContextConfig1M is optimized for 1M+ context models (e.g. Gemini 1.5 Pro)
	ContextConfig1M = ContextWindowConfig{
		MaxHistoryChars:             2500000,
		HotTokenBudgetChars:         1000000,
		HistoryWindowMessages:       320,
		ToolOutputMaxChars:          100000,
		TextCompressionThreshold:    100000,
		LargeToolOutputLogThreshold: 50000,
		HeadKeepChars:               20000,
		TailKeepChars:               20000,
		TextHeadKeep:                20000,
	}
)

var (
	// Pre-compiled regex for memory content normalization
	reMemoryTags = regexp.MustCompile(`\[(scope|category|type):\s*[^\]]+\]`)
	reMultiSpace = regexp.MustCompile(`\s+`)
)

// GetContextConfig selects the optimal configuration based on the model's actual context window tokens.
func GetContextConfig(modelContextTokens int64) ContextWindowConfig {
	if modelContextTokens >= 1000000 {
		return ContextConfig1M
	} else if modelContextTokens >= 256000 {
		return ContextConfig256K
	}
	return ContextConfig128K
}

func contextCharPerToken() float64 {
	charPerToken := CharPerToken
	if cfg := config.Get(); cfg != nil && cfg.LLM.CharPerToken > 0 {
		charPerToken = cfg.LLM.CharPerToken
	}
	if charPerToken <= 0 {
		return CharPerToken
	}
	return charPerToken
}

func contextSoftLimitTokens(modelContextWindow int64) int64 {
	if modelContextWindow <= 0 {
		return 0
	}
	return int64(float64(modelContextWindow) * 0.6)
}

func contextHardLimitTokens(modelContextWindow int64) int64 {
	if modelContextWindow <= 0 {
		return 0
	}
	return int64(float64(modelContextWindow) * 0.85)
}

func contextResponseReserveTokens(modelContextWindow int64) int64 {
	if modelContextWindow <= 0 {
		return 0
	}
	return int64(float64(modelContextWindow) * 0.08)
}

func estimateCharsForTokens(tokens int64) int {
	if tokens <= 0 {
		return 0
	}
	return int(math.Ceil(float64(tokens) * contextCharPerToken()))
}

func scaleContextLimit(baseValue int, ratio float64, minValue int, maxValue int) int {
	if maxValue <= 0 {
		return 0
	}
	if baseValue <= 0 {
		return minInt(maxValue, maxInt(minValue, 0))
	}
	if ratio <= 0 {
		ratio = 0.05
	}
	scaled := int(math.Round(float64(baseValue) * ratio))
	if scaled < minValue {
		scaled = minValue
	}
	if scaled > maxValue {
		scaled = maxValue
	}
	return scaled
}

func resolveContextConfigForReservedTokens(modelContextTokens int64, reservedPromptTokens int64) ContextWindowConfig {
	if modelContextTokens <= 0 {
		modelContextTokens = 256000
	}

	baseCfg := GetContextConfig(modelContextTokens)
	hardLimitTokens := contextHardLimitTokens(modelContextTokens)
	if reservedPromptTokens < 0 {
		reservedPromptTokens = 0
	}
	historyBudgetTokens := hardLimitTokens - reservedPromptTokens
	if historyBudgetTokens < 0 {
		historyBudgetTokens = 0
	}

	maxHistoryChars := estimateCharsForTokens(historyBudgetTokens)
	if maxHistoryChars <= 0 {
		maxHistoryChars = EmergencyStubSize * 4
	}
	if baseCfg.MaxHistoryChars > 0 {
		maxHistoryChars = minInt(baseCfg.MaxHistoryChars, maxHistoryChars)
	}

	ratio := 1.0
	if baseCfg.MaxHistoryChars > 0 {
		ratio = float64(maxHistoryChars) / float64(baseCfg.MaxHistoryChars)
		if ratio > 1 {
			ratio = 1
		}
		if ratio <= 0 {
			ratio = 0.05
		}
	}

	return ContextWindowConfig{
		MaxHistoryChars:             maxHistoryChars,
		HotTokenBudgetChars:         scaleContextLimit(baseCfg.HotTokenBudgetChars, ratio, minInt(maxHistoryChars, 12000), maxHistoryChars),
		HistoryWindowMessages:       baseCfg.HistoryWindowMessages,
		ToolOutputMaxChars:          scaleContextLimit(baseCfg.ToolOutputMaxChars, ratio, minInt(maxHistoryChars, 4000), maxHistoryChars),
		TextCompressionThreshold:    scaleContextLimit(baseCfg.TextCompressionThreshold, ratio, minInt(maxHistoryChars, 3000), maxHistoryChars),
		LargeToolOutputLogThreshold: scaleContextLimit(baseCfg.LargeToolOutputLogThreshold, ratio, minInt(maxHistoryChars, 2000), maxHistoryChars),
		HeadKeepChars:               scaleContextLimit(baseCfg.HeadKeepChars, ratio, minInt(maxHistoryChars, 1200), maxHistoryChars),
		TailKeepChars:               scaleContextLimit(baseCfg.TailKeepChars, ratio, minInt(maxHistoryChars, 1200), maxHistoryChars),
		TextHeadKeep:                scaleContextLimit(baseCfg.TextHeadKeep, ratio, minInt(maxHistoryChars, 1200), maxHistoryChars),
	}
}

func estimateContextTokensFromChars(chars int) int64 {
	if chars <= 0 {
		return 0
	}

	return int64(math.Ceil(float64(chars) / contextCharPerToken()))
}

func estimateContextTokensFromText(text string) int64 {
	return estimateContextTokensFromChars(len([]rune(text)))
}

func estimateBinaryContentTokens(content message.BinaryContent) int64 {
	if strings.HasPrefix(strings.ToLower(content.MIMEType), "image/") {
		return 1536
	}
	return estimateContextTokensFromChars(len(content.Data))
}

func truncateRunes(text string, limit int, suffix string) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + suffix
}

type messageTokenBreakdown struct {
	system      int64
	messages    int64
	toolResults int64
}

func (b messageTokenBreakdown) total() int64 {
	return b.system + b.messages + b.toolResults
}

func estimateMessageTokenBreakdown(msg message.Message) messageTokenBreakdown {
	var breakdown messageTokenBreakdown
	for _, part := range msg.Parts {
		switch current := part.(type) {
		case message.TextContent:
			tokens := estimateContextTokensFromText(current.Text)
			if msg.Role == message.System {
				breakdown.system += tokens
			} else {
				breakdown.messages += tokens
			}
		case message.ReasoningContent:
			breakdown.messages += estimateContextTokensFromText(current.Thinking)
		case message.ToolCall:
			breakdown.messages += estimateContextTokensFromText(current.Name)
			breakdown.messages += estimateContextTokensFromText(current.Input)
		case message.ToolResult:
			breakdown.toolResults += estimateContextTokensFromText(current.Content)
			breakdown.toolResults += estimateContextTokensFromText(current.Metadata)
		case message.AttachmentMetaContent:
			breakdown.messages += estimateContextTokensFromText(current.Name + " " + current.Type + " " + current.URL)
		case message.ImageURLContent:
			breakdown.messages += estimateContextTokensFromText(current.URL)
		case message.BinaryContent:
			breakdown.messages += estimateBinaryContentTokens(current)
		}
	}
	return breakdown
}

func estimateMessageTokens(msg message.Message) int64 {
	return estimateMessageTokenBreakdown(msg).total()
}

func estimateHistoryTokens(history []message.Message) int64 {
	var total int64
	for _, msg := range history {
		total += estimateMessageTokens(msg)
	}
	return total
}

func estimateToolDefinitionTokens(activeTools []tools.BaseTool) int64 {
	var total int64
	for _, tool := range activeTools {
		info := tool.Info()
		payload := map[string]any{
			"name":        info.Name,
			"description": info.Description,
			"parameters":  info.Parameters,
			"required":    info.Required,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			total += estimateContextTokensFromText(info.Name + "\n" + info.Description)
			continue
		}
		total += estimateContextTokensFromText(string(encoded))
	}
	return total
}

func estimateMessageContextSections(history []message.Message) []ContextStatsSection {
	var systemTokens int64
	var messageTokens int64
	var toolResultTokens int64

	for _, msg := range history {
		breakdown := estimateMessageTokenBreakdown(msg)
		systemTokens += breakdown.system
		messageTokens += breakdown.messages
		toolResultTokens += breakdown.toolResults
	}

	return []ContextStatsSection{
		{Key: "system_instructions", Label: "系统指令", Tokens: systemTokens},
		{Key: "messages", Label: "消息", Tokens: messageTokens},
		{Key: "tool_results", Label: "工具结果", Tokens: toolResultTokens},
	}
}

func scaleContextSections(sections []ContextStatsSection, targetTotal int64) []ContextStatsSection {
	currentTotal := int64(0)
	for _, section := range sections {
		currentTotal += section.Tokens
	}
	if targetTotal <= 0 || currentTotal <= 0 || currentTotal == targetTotal {
		return sections
	}

	scaled := make([]ContextStatsSection, len(sections))
	remaining := targetTotal
	for index, section := range sections {
		scaled[index] = section
		if remaining <= 0 {
			scaled[index].Tokens = 0
			continue
		}
		if index == len(sections)-1 {
			scaled[index].Tokens = maxInt64(0, remaining)
			break
		}
		nextTokens := int64(math.Round(float64(section.Tokens) * float64(targetTotal) / float64(currentTotal)))
		if nextTokens < 0 {
			nextTokens = 0
		}
		if nextTokens > remaining {
			nextTokens = remaining
		}
		scaled[index].Tokens = nextTokens
		remaining -= nextTokens
	}
	return scaled
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (a *agent) buildContextStats(
	source string,
	model models.Model,
	stepCount int,
	history []message.Message,
	activeTools []tools.BaseTool,
	observedPromptTokens int64,
) ContextStats {
	modelContextWindow := effectiveModelContextWindowForModel(model)
	if modelContextWindow <= 0 {
		modelContextWindow = 256000
	}
	hardLimit := contextHardLimitTokens(modelContextWindow)
	softLimit := contextSoftLimitTokens(modelContextWindow)
	reserve := contextResponseReserveTokens(modelContextWindow)

	sections := estimateMessageContextSections(history)
	toolDefinitionTokens := estimateToolDefinitionTokens(activeTools)
	sections = []ContextStatsSection{
		sections[0],
		{Key: "tool_definitions", Label: "工具定义", Tokens: toolDefinitionTokens},
		sections[1],
		sections[2],
	}
	sections[0].Label = "\u7cfb\u7edf\u6307\u4ee4"
	sections[1].Label = "\u5de5\u5177\u5b9a\u4e49"
	sections[2].Label = "\u6d88\u606f"
	sections[3].Label = "\u5de5\u5177\u7ed3\u679c"

	currentTokens := int64(0)
	for _, section := range sections {
		currentTokens += section.Tokens
	}
	estimatedTokens := currentTokens
	effectiveSource := source
	confidence := "estimate"

	// Some providers report only uncached/new prompt tokens in usage. That value is
	// useful for cost accounting, but it can be much smaller than the actual prompt
	// currently occupying the context window. Only let observed usage calibrate upward.
	if observedPromptTokens > currentTokens {
		currentTokens = observedPromptTokens
		sections = scaleContextSections(sections, observedPromptTokens)
		confidence = "provider_adjusted"
	} else if observedPromptTokens > 0 && source == "provider" {
		effectiveSource = "estimate"
	}
	remainingTokens := maxInt64(0, modelContextWindow-currentTokens-reserve)

	modelID := string(model.ID)
	if strings.TrimSpace(modelID) == "" {
		modelID = string(model.APIModel)
	}

	return ContextStats{
		Source:                effectiveSource,
		Confidence:            confidence,
		Model:                 modelID,
		CurrentTokens:         currentTokens,
		EstimatedTokens:       estimatedTokens,
		ObservedPromptTokens:  observedPromptTokens,
		ContextWindowTokens:   modelContextWindow,
		MaxTokens:             modelContextWindow,
		HardLimitTokens:       hardLimit,
		SoftLimitTokens:       softLimit,
		ResponseReserveTokens: reserve,
		RemainingTokens:       remainingTokens,
		MessageCount:          len(history),
		StepCount:             stepCount,
		Sections:              sections,
	}
}

func latestUserContentFromHistory(history []message.Message) string {
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role != message.User {
			continue
		}
		content := strings.TrimSpace(history[index].Content().String())
		if content != "" {
			return content
		}
	}
	return ""
}

func latestUserMessageIDFromHistory(history []message.Message) string {
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role != message.User {
			continue
		}
		if id := strings.TrimSpace(history[index].ID); id != "" {
			return id
		}
	}
	return ""
}

func contextStatsIntentForMode(mode string) string {
	switch mode {
	case "coding":
		return prompt.IntentCoding
	case "autonomous":
		return prompt.IntentLifeAutonomous
	default:
		return prompt.IntentChat
	}
}

func (a *agent) buildSessionContextStatsWithProvider(ctx context.Context, sessionID string, mode string, currentProvider provider.Provider, stepCount int) (ContextStats, error) {
	if currentProvider == nil {
		return ContextStats{}, fmt.Errorf("provider not configured")
	}

	model := currentProvider.Model()
	history, err := a.loadSessionHistoryWindowForModel(ctx, sessionID, model)
	if err != nil {
		return ContextStats{}, err
	}

	baseHistory := a.prependTodoSystemMessage(ctx, sessionID, history)
	preparedHistory, err := a.prepareConversationContextForModel(ctx, sessionID, baseHistory, model)
	if err != nil {
		return ContextStats{}, err
	}

	promptState, promptStateErr := a.loadTaskState(ctx, sessionID)
	if promptStateErr != nil {
		logging.Warn("Failed to load task state for context stats", "session_id", sessionID, "error", promptStateErr)
	}

	intent := contextStatsIntentForMode(mode)
	sysMsgs, dynamicSuffix, err := a.buildSystemMessage(ctx, "", mode, model.Provider, intent, promptState)
	if err != nil {
		return ContextStats{}, err
	}
	currentInput := latestUserContentFromHistory(baseHistory)
	currentTools := a.getActiveToolsForSession(sessionID, currentInput)
	sort.Slice(currentTools, func(i, j int) bool {
		return currentTools[i].Info().Name < currentTools[j].Info().Name
	})

	var ragLayers *ragContextLayers
	if currentInput != "" {
		ragLayers, err = a.retrieveRAGContext(ctx, sessionID, currentInput, mode, intent, nil, nil)
		if err != nil {
			logging.Warn("Failed to build RAG context for session stats", "session_id", sessionID, "error", err)
			ragLayers = nil
		}
	}

	layers := a.buildPromptContextLayers(ctx, sessionID, sysMsgs, ragLayers, promptState, dynamicSuffix)
	currentHistory, err := a.squeezePreparedHistoryForPromptBudget(ctx, sessionID, preparedHistory, model, currentTools, layers)
	if err != nil {
		return ContextStats{}, err
	}

	stats := a.buildContextStats("session", model, stepCount, currentHistory, currentTools, 0)
	stats.Assembly = buildContextAssemblyReport(a.historyWindowLoadReportForSession(sessionID), ragLayers, promptState, dynamicSuffix, a.childWorkerCheckpointCount(sessionID))
	if stats.Assembly != nil && strings.TrimSpace(stats.Assembly.WarmContextStatus) == "" {
		switch {
		case strings.TrimSpace(currentInput) == "":
			stats.Assembly.WarmContextStatus = "disabled_no_user_input"
		case a.messages == nil:
			stats.Assembly.WarmContextStatus = "disabled_message_store_unavailable"
		}
	}
	return stats, nil
}

func (a *agent) BuildSessionContextStats(ctx context.Context, sessionID string, mode string) (ContextStats, error) {
	currentProvider, err := a.getProviderForRequest(ctx, nil)
	if err != nil {
		return ContextStats{}, err
	}
	return a.buildSessionContextStatsWithProvider(ctx, sessionID, mode, currentProvider, 0)
}

func (a *agent) emitSessionContextStats(ctx context.Context, events chan AgentEvent, sessionID string, mode string, currentProvider provider.Provider, stepCount int) {
	stats, err := a.buildSessionContextStatsWithProvider(ctx, sessionID, mode, currentProvider, stepCount)
	if err != nil {
		logging.Warn("Failed to build final context stats", "session_id", sessionID, "error", err)
		return
	}
	sendAgentEvent(ctx, events, AgentEvent{
		Type: AgentEventTypeContextStats,
		Data: stats,
	})
}

type normalizedReadSpec struct {
	Path       string
	Offset     int
	Limit      int
	Mode       string
	SearchTerm string
}

type ragContextLayers struct {
	MemoryCapsule *message.Message
	TurnContext   *message.Message
	Assembly      *ContextAssemblyReport
}

type warmContextSearchScope struct {
	SessionID string
	Label     string
}

func (a *agent) retrievalWaitBudgets(ctx context.Context, sessionID, mode, intent string, warmContextEligible bool) (time.Duration, time.Duration) {
	criticalTimeout := 300 * time.Millisecond
	deepTimeout := 120 * time.Millisecond
	if warmContextEligible {
		deepTimeout = 300 * time.Millisecond
	}

	firstTurn := false
	if a.sessions != nil && strings.TrimSpace(sessionID) != "" {
		if sess, err := a.sessions.Get(ctx, sessionID); err == nil {
			firstTurn = sess.MessageCount <= 2
		}
	}
	complexTask := mode == "autonomous"
	switch strings.ToLower(strings.TrimSpace(intent)) {
	case "broad", "search", "task", "coding", "diagnostic":
		complexTask = true
	}

	if mode == "autonomous" {
		criticalTimeout = 800 * time.Millisecond
		deepTimeout += 120 * time.Millisecond
	}
	if firstTurn && complexTask {
		if criticalTimeout < 900*time.Millisecond {
			criticalTimeout = 900 * time.Millisecond
		}
		if deepTimeout < 750*time.Millisecond {
			deepTimeout = 750 * time.Millisecond
		}
	}
	return criticalTimeout, deepTimeout
}

type promptContextLayers struct {
	prefix []message.Message
	suffix []message.Message
}

func (l promptContextLayers) fixedTokens() int64 {
	if len(l.prefix) == 0 && len(l.suffix) == 0 {
		return 0
	}

	total := estimateHistoryTokens(l.prefix)
	total += estimateHistoryTokens(l.suffix)
	return total
}

func (l promptContextLayers) assemble(history []message.Message) []message.Message {
	assembled := make([]message.Message, 0, len(l.prefix)+len(history)+len(l.suffix))
	assembled = append(assembled, l.prefix...)
	assembled = append(assembled, history...)
	assembled = append(assembled, l.suffix...)
	return assembled
}

func (a *agent) buildPromptContextLayers(ctx context.Context, sessionID string, sysMsgs []message.Message, ragLayers *ragContextLayers, promptState *sessionTaskState, dynamicSuffix string) promptContextLayers {
	layers := promptContextLayers{}
	layers.prefix = append(layers.prefix, sysMsgs...)

	if ragLayers != nil && ragLayers.MemoryCapsule != nil {
		layers.suffix = append([]message.Message{*ragLayers.MemoryCapsule}, layers.suffix...)
	}

	if writePolicyMsg := buildWritePolicySystemMessage(promptState); writePolicyMsg != nil {
		layers.suffix = append(layers.suffix, *writePolicyMsg)
	}
	if promptState != nil {
		layers.suffix = append(layers.suffix, a.buildTaskStatePromptLayers(sessionID, promptState)...)
	} else if taskStateMsg := a.buildTaskStateSystemMessage(ctx, sessionID); taskStateMsg != nil {
		layers.suffix = append(layers.suffix, *taskStateMsg)
	}

	var dynamicParts []string
	if ragLayers != nil && ragLayers.TurnContext != nil {
		dynamicParts = append(dynamicParts, "\n\n"+ragLayers.TurnContext.Content().String())
	}
	if dynamicSuffix != "" {
		dynamicParts = append(dynamicParts, "\n\n[SYSTEM INFO]\n"+dynamicSuffix)
	}
	if len(dynamicParts) > 0 {
		layers.suffix = append(layers.suffix, message.Message{
			Role: message.System,
			Parts: []message.ContentPart{
				message.TextContent{Text: strings.Join(dynamicParts, "")},
			},
		})
	}

	return layers
}

func buildContextAssemblyReport(historyReport *historyWindowLoadReport, ragLayers *ragContextLayers, promptState *sessionTaskState, dynamicSuffix string, workerCheckpointCount int) *ContextAssemblyReport {
	var report *ContextAssemblyReport
	if ragLayers != nil && ragLayers.Assembly != nil {
		copied := *ragLayers.Assembly
		report = &copied
	}
	if report == nil {
		report = &ContextAssemblyReport{}
	}
	if historyReport != nil {
		report.HistoryWindowSource = historyReport.Source
		report.HistoryWindowFallbackReason = historyReport.FallbackReason
		report.HistoryWindowMessages = historyReport.MessageCount
	}
	if promptState != nil {
		report.WorkingState = true
		report.MutationJournal = isMutationHeavyPromptState(promptState)
	}
	report.WorkerCheckpointCount = workerCheckpointCount
	report.WorkerCheckpointInjected = workerCheckpointCount > 0
	report.DynamicSuffix = strings.TrimSpace(dynamicSuffix) != ""
	return report
}

func countMemoriesByCategory(memories []db.Memory, category string) int {
	count := 0
	for _, memory := range memories {
		if memory.Category == category {
			count++
		}
	}
	return count
}

func (a *agent) warmContextSearchScopes(ctx context.Context, sessionID string) []warmContextSearchScope {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}

	scopes := []warmContextSearchScope{{SessionID: sessionID, Label: "current_session"}}
	if a.sessions == nil {
		return scopes
	}

	visited := map[string]struct{}{sessionID: {}}
	currentID := sessionID
	for depth := 0; depth < 8; depth++ {
		sess, err := a.sessions.Get(ctx, currentID)
		if err != nil {
			break
		}
		parentID := strings.TrimSpace(sess.ParentSessionID)
		if parentID == "" {
			break
		}
		if _, ok := visited[parentID]; ok {
			break
		}
		label := "parent_session"
		if depth > 0 {
			label = "ancestor_session"
		}
		scopes = append(scopes, warmContextSearchScope{SessionID: parentID, Label: label})
		visited[parentID] = struct{}{}
		currentID = parentID
	}

	return scopes
}

func (a *agent) recentWarmContextMessages(ctx context.Context, sessionID string, scopeLabel string, limit int) ([]message.Message, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || a.messages == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = MaxPastMessages
	}

	fetchLimit := limit
	if scopeLabel == "current_session" {
		fetchLimit += WarmContextCurrentSessionTailSkip
	}

	summaryMessageID := ""
	if a.sessions != nil {
		if sess, err := a.sessions.Get(ctx, sessionID); err == nil {
			summaryMessageID = sess.SummaryMessageID
		}
	}

	msgs, err := a.messages.ListWindow(ctx, sessionID, summaryMessageID, fetchLimit)
	if err != nil {
		msgs, err = a.messages.List(ctx, sessionID)
		if err != nil {
			return nil, err
		}
	}

	filtered := filterSessionHistoryMessages(msgs)
	if scopeLabel == "current_session" {
		if len(filtered) <= WarmContextCurrentSessionTailSkip {
			return nil, nil
		}
		filtered = filtered[:len(filtered)-WarmContextCurrentSessionTailSkip]
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered, nil
}

func (a *agent) squeezePreparedHistoryForPromptBudget(ctx context.Context, sessionID string, preparedHistory []message.Message, model models.Model, activeTools []tools.BaseTool, layers promptContextLayers) ([]message.Message, error) {
	hardLimitTokens := contextHardLimitTokens(effectiveModelContextWindowForModel(model))
	if hardLimitTokens <= 0 {
		return layers.assemble(preparedHistory), nil
	}

	toolDefinitionTokens := estimateToolDefinitionTokens(activeTools)
	reservedTokens := layers.fixedTokens() + toolDefinitionTokens
	budgetedHistory, err := a.prepareConversationContextForModelWithReservedTokens(ctx, sessionID, preparedHistory, model, reservedTokens)
	if err != nil {
		return nil, err
	}

	currentHistory := layers.assemble(budgetedHistory)
	for attempt := 0; attempt < 2; attempt++ {
		totalPromptTokens := estimateHistoryTokens(currentHistory) + toolDefinitionTokens
		if totalPromptTokens <= hardLimitTokens {
			return currentHistory, nil
		}

		overflow := totalPromptTokens - hardLimitTokens
		logging.Warn("Prompt context still exceeds hard limit after squeeze", "session_id", sessionID, "overflow_tokens", overflow, "attempt", attempt+1)

		budgetedHistory, err = a.prepareConversationContextForModelWithReservedTokens(ctx, sessionID, budgetedHistory, model, reservedTokens+overflow)
		if err != nil {
			return nil, err
		}
		currentHistory = layers.assemble(budgetedHistory)
	}

	return currentHistory, nil
}

func logMemorySearchDecision(decision memory.MemorySearchDecision) {
	logging.Info(
		"Memory search decision",
		"stage", decision.Stage,
		"category", decision.Category,
		"limit", decision.Limit,
		"fts_candidates", decision.FTSCandidateLimit,
		"vector_candidates", decision.VecCandidateLimit,
		"channels", strings.Join(decision.UsedChannels, ","),
		"fallback", decision.FallbackPath,
		"reasons", fmt.Sprintf("%v", decision.ReasonCodes),
		"duration_ms", decision.DurationMs,
	)
}

func isReadToolName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "read" || lower == "read_file"
}

func normalizeReadSpec(input string) (normalizedReadSpec, bool) {
	var params tools.ReadParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return normalizedReadSpec{}, false
	}

	if params.FilePath == "" {
		if params.Path != "" {
			params.FilePath = params.Path
		} else if params.FileName != "" {
			params.FilePath = params.FileName
		}
	}

	if strings.TrimSpace(params.FilePath) == "" {
		return normalizedReadSpec{}, false
	}

	limit := params.Limit
	if limit <= 0 {
		limit = tools.DefaultReadLines
	}

	offset := params.Offset
	if offset <= 0 {
		offset = 1
	}

	mode := strings.ToLower(strings.TrimSpace(params.Mode))
	if mode == "" {
		mode = "full"
	}
	if mode == "definitions" {
		mode = "skeleton"
	}

	resolvedPath := tools.ResolvePath(params.FilePath)
	if resolvedPath == "" {
		resolvedPath = params.FilePath
	}

	return normalizedReadSpec{
		Path:       strings.ToLower(filepath.Clean(resolvedPath)),
		Offset:     offset,
		Limit:      limit,
		Mode:       mode,
		SearchTerm: strings.TrimSpace(params.SearchTerm),
	}, true
}

func (s normalizedReadSpec) exactKey() string {
	return fmt.Sprintf("%s|%d|%d|%s|%s", s.Path, s.Offset, s.Limit, s.Mode, s.SearchTerm)
}

func (s normalizedReadSpec) canCompareOverlap() bool {
	return s.Path != "" && s.Mode == "full" && s.SearchTerm == "" && s.Offset > 0 && s.Limit > 0
}

func (s normalizedReadSpec) overlapsStrongly(other normalizedReadSpec) bool {
	if !s.canCompareOverlap() || !other.canCompareOverlap() {
		return false
	}
	if s.Path != other.Path {
		return false
	}

	sStart, sEnd := s.Offset, s.Offset+s.Limit-1
	oStart, oEnd := other.Offset, other.Offset+other.Limit-1
	if sEnd < oStart || oEnd < sStart {
		return false
	}

	intersectionStart := sStart
	if oStart > intersectionStart {
		intersectionStart = oStart
	}
	intersectionEnd := sEnd
	if oEnd < intersectionEnd {
		intersectionEnd = oEnd
	}

	intersectionLen := intersectionEnd - intersectionStart + 1
	if intersectionLen <= 0 {
		return false
	}

	minLen := s.Limit
	if other.Limit < minLen {
		minLen = other.Limit
	}
	if minLen <= 0 {
		return false
	}

	return float64(intersectionLen)/float64(minLen) >= 0.8
}

// prepareConversationContext handles history truncation and token management.
// It ensures the conversation history fits within the model's context window.
// REFACTORED: Single-Pass Backwards Processing for Efficiency
func (a *agent) prepareConversationContextForModel(ctx context.Context, sessionID string, msgHistory []message.Message, model models.Model) ([]message.Message, error) {
	return a.prepareConversationContextForModelWithReservedTokens(ctx, sessionID, msgHistory, model, 0)
}

func (a *agent) prepareConversationContextForModelWithReservedTokens(ctx context.Context, sessionID string, msgHistory []message.Message, model models.Model, reservedPromptTokens int64) ([]message.Message, error) {
	// Determine optimal context window configuration
	modelContextWindow := effectiveModelContextWindowForModel(model)
	ctxCfg := resolveContextConfigForReservedTokens(modelContextWindow, reservedPromptTokens)
	baseCtxCfg := GetContextConfig(modelContextWindow)
	isReservedPromptConstrained := reservedPromptTokens > 0 && ctxCfg.MaxHistoryChars < baseCtxCfg.MaxHistoryChars
	logging.Info("Context Config Selected", "model_context_window", modelContextWindow, "reserved_prompt_tokens", reservedPromptTokens, "max_history_chars", ctxCfg.MaxHistoryChars)

	// Sliding Window Strategy for Long Context
	// Instead of dropping old messages, we can compress them or rely on the summarizer.
	// We'll enforce a stricter budget for the "Hot Context" to preserve recent actions.
	// Optimization: Pre-calculate total size to avoid repeated traversals
	// We use a heuristic (char count) which is much faster than tokenization.
	// If the total size is small (< 32k chars), we can skip most expensive checks.

	// Helper to estimate message size
	getMessageSize := func(m *message.Message) int {
		size := 0
		for _, p := range m.Parts {
			switch v := p.(type) {
			case message.TextContent:
				size += len(v.Text)
			case message.ToolResult:
				size += len(v.Content) + len(v.Metadata)
			case message.ToolCall:
				size += len(v.Name) + len(v.Input)
			case message.ReasoningContent:
				size += len(v.Thinking)
			case message.AttachmentMetaContent:
				size += len(v.Name) + len(v.Type) + len(v.URL)
			case message.ImageURLContent:
				size += len(v.URL)
			case message.BinaryContent:
				if strings.HasPrefix(strings.ToLower(v.MIMEType), "image/") {
					size += int(estimateBinaryContentTokens(v) * int64(contextCharPerToken()))
				} else {
					size += len(v.Data)
				}
			}
		}
		return size
	}

	toolCallReadSpecs := make(map[string]normalizedReadSpec)

	if len(msgHistory) > 5 {
		for i := range msgHistory {
			m := &msgHistory[i]
			if m.Role == message.Assistant {
				for _, part := range m.Parts {
					if tc, ok := part.(message.ToolCall); ok {
						if isReadToolName(tc.Name) {
							if spec, ok := normalizeReadSpec(tc.Input); ok {
								toolCallReadSpecs[tc.ID] = spec
							}
						}
					}
				}
			}
		}
	}

	// Phase 2: Main Backwards Pass (Processing & Budgeting)
	// We allocate the slice with capacity to avoid resizes
	processedMessages := make([]message.Message, 0, len(msgHistory))
	totalSize := 0
	seenReadExact := make(map[string]bool)
	seenReadSpecs := make([]normalizedReadSpec, 0, 8)
	seenMsgIDs := make(map[string]bool)
	keptMessagesFromTail := 0

	// Always preserve the System Prompt (usually index 0)
	var systemPrompt *message.Message
	if len(msgHistory) > 0 && msgHistory[0].Role == message.System {
		systemPrompt = &msgHistory[0]
	}

	// Iterate backwards
	for i := len(msgHistory) - 1; i >= 0; i-- {
		// Skip index 0 if it's the system prompt (we'll add it at the end)
		if i == 0 && systemPrompt != nil {
			continue
		}

		m := msgHistory[i] // Copy message (shallow copy of slices inside)

		// Deduplication by ID
		if m.ID != "" {
			if seenMsgIDs[m.ID] {
				continue
			}
			seenMsgIDs[m.ID] = true
		}

		if sanitized, keep := sanitizeMessageForModelReplay(m); keep {
			m = sanitized
		} else {
			continue
		}

		// Determine Hot/Cold after replay sanitization so dropped internal-thinking
		// turns do not consume recent-message protection.
		isCold := totalSize > ctxCfg.HotTokenBudgetChars
		isWithinHotProtection := keptMessagesFromTail < MinHotMessages

		// Process Content (Compression & Stripping)
		// Optimization: Reuse existing slice if no modification needed
		// We only allocate 'newParts' if we detect a change.
		var newParts []message.ContentPart
		modified := false

		// Determine if this is the last message (User Input)
		isLastMessage := (i == len(msgHistory)-1)

		for partIdx, p := range m.Parts {
			switch v := p.(type) {
			case message.ToolResult:
				if spec, exists := toolCallReadSpecs[v.ToolCallID]; exists {
					exactKey := spec.exactKey()
					hideReason := ""
					if seenReadExact[exactKey] {
						hideReason = fmt.Sprintf("[System: Redundant read hidden. You already read %s L%d-%d. Do NOT re-read.]",
							spec.Path, spec.Offset, spec.Offset+spec.Limit-1)
					} else {
						for _, seenSpec := range seenReadSpecs {
							if spec.overlapsStrongly(seenSpec) {
								hideReason = fmt.Sprintf("[System: Overlapping read hidden. You already read %s L%d-%d which overlaps with L%d-%d. Do NOT re-read.]",
									seenSpec.Path, seenSpec.Offset, seenSpec.Offset+seenSpec.Limit-1,
									spec.Offset, spec.Offset+spec.Limit-1)
								break
							}
						}
					}

					if hideReason != "" {
						if newParts == nil {
							newParts = make([]message.ContentPart, len(m.Parts))
							copy(newParts, m.Parts)
						}

						newRes := v
						newRes.Content = hideReason
						newParts[partIdx] = newRes
						modified = true
						continue
					}

					seenReadExact[exactKey] = true
					seenReadSpecs = append(seenReadSpecs, spec)
				}

				shouldForceFold := false
				if len(v.Content) > ctxCfg.ToolOutputMaxChars {
					remainingBudgetChars := maxInt(ctxCfg.MaxHistoryChars-totalSize, 0)
					if remainingBudgetChars == 0 || len(v.Content)+len(v.Metadata) > remainingBudgetChars {
						shouldForceFold = true
					} else if isReservedPromptConstrained && len(v.Content)+len(v.Metadata) > ctxCfg.HotTokenBudgetChars {
						shouldForceFold = true
					} else if isCold {
						shouldForceFold = true
					} else if !isWithinHotProtection {
						toolName := strings.ToLower(strings.TrimSpace(v.Name))
						if toolName == "read" || toolName == "read_file" || toolName == "cat" || len(v.Content) > ctxCfg.ToolOutputMaxChars*2 {
							shouldForceFold = true
						}
					}
				}

				if shouldForceFold {
					if newParts == nil {
						newParts = make([]message.ContentPart, len(m.Parts))
						copy(newParts, m.Parts)
					}
					newRes := v
					newRes.Content = truncateToolResultContent(v.Content, ctxCfg)
					newParts[partIdx] = newRes
					modified = true
				}

			case message.TextContent:
				// Optimization: Fast check before expensive string operations
				// FIX: Removed length check for stripping to ensure even short system tags are removed
				// Also added [CONTEXT INFO] to the strip list
				hasTags := strings.Contains(v.Text, "[SYSTEM INFO]") || strings.Contains(v.Text, "<available_skills>") || strings.Contains(v.Text, "[CONTEXT INFO]") || strings.Contains(v.Text, "[TURN CONTEXT]") || strings.Contains(v.Text, "[MEMORY CAPSULE]")
				needsStrip := !isLastMessage && hasTags
				needsCompression := isCold && len(v.Text) > ctxCfg.TextCompressionThreshold

				if needsStrip || needsCompression {
					if newParts == nil {
						newParts = make([]message.ContentPart, len(m.Parts))
						copy(newParts, m.Parts)
					}

					cleanedText := v.Text

					if needsStrip {
						hasStripped := false

						// Check [CONTEXT INFO]
						if idx := strings.Index(cleanedText, "[CONTEXT INFO]"); idx != -1 {
							cleanedText = cleanedText[:idx] + "\n\n[System: Historical Context Hidden]"
							hasStripped = true
						}

						if idx := strings.Index(cleanedText, "[MEMORY CAPSULE]"); idx != -1 {
							cleanedText = cleanedText[:idx] + "\n\n[System: Historical Memory Capsule Hidden]"
							hasStripped = true
						}

						if idx := strings.Index(cleanedText, "[TURN CONTEXT]"); idx != -1 {
							cleanedText = cleanedText[:idx] + "\n\n[System: Historical Turn Context Hidden]"
							hasStripped = true
						}

						// Check [SYSTEM INFO]
						if idx := strings.Index(cleanedText, "[SYSTEM INFO]"); idx != -1 {
							cleanedText = cleanedText[:idx] + "\n\n[System: Historical Info Hidden]"
							hasStripped = true
						}

						// Check <available_skills>
						if idx := strings.Index(cleanedText, "<available_skills>"); idx != -1 {
							cleanedText = cleanedText[:idx] + "\n[System: Historical Skills Hidden]"
							hasStripped = true
						}

						if hasStripped && strings.TrimSpace(cleanedText) == "" {
							cleanedText = "[System: Content Hidden]"
						}
					}

					if isCold && len(cleanedText) > ctxCfg.TextCompressionThreshold {
						head := truncateRunes(cleanedText, ctxCfg.TextHeadKeep, "")
						cleanedText = head + "\n... [System: User Input Folded] ..."
					}

					newParts[partIdx] = message.TextContent{Text: cleanedText}
					modified = true
				}
			case message.BinaryContent:
				if !isLastMessage {
					if newParts == nil {
						newParts = make([]message.ContentPart, len(m.Parts))
						copy(newParts, m.Parts)
					}
					replacement := "[System: Historical binary attachment omitted from model replay.]"
					if strings.HasPrefix(strings.ToLower(v.MIMEType), "image/") {
						replacement = "[System: Historical image attachment omitted from model replay.]"
					}
					newParts[partIdx] = message.TextContent{Text: replacement}
					modified = true
				}
			}
		}

		if modified {
			m.Parts = newParts
		}

		// Calculate Size & Budget Check
		msgSize := getMessageSize(&m)

		// Check if adding this message exceeds the hard limit
		// We prioritize recent messages, so if we exceed, we stop adding (effectively truncating head)
		// But we must respect MinHotMessages protection
		if !isWithinHotProtection && (totalSize+msgSize > ctxCfg.MaxHistoryChars) {
			// Budget exceeded. Stop adding older messages.
			// Collect read file records from truncated messages so model knows what it already read
			truncatedReadPaths := make(map[string]map[string]bool) // path -> set of range strings
			for j := 0; j <= i; j++ {
				for _, p := range msgHistory[j].Parts {
					if tc, ok := p.(message.ToolCall); ok {
						if spec, exists := toolCallReadSpecs[tc.ID]; exists {
							if truncatedReadPaths[spec.Path] == nil {
								truncatedReadPaths[spec.Path] = make(map[string]bool)
							}
							truncatedReadPaths[spec.Path][fmt.Sprintf("L%d-%d", spec.Offset, spec.Offset+spec.Limit-1)] = true
						}
					}
				}
			}

			truncateText := "[System: History Truncated]"
			if len(truncatedReadPaths) > 0 {
				var sb strings.Builder
				sb.WriteString("[System: History Truncated]\n\nFiles previously read (in truncated history):\n")
				// Sort paths for deterministic output
				paths := make([]string, 0, len(truncatedReadPaths))
				for p := range truncatedReadPaths {
					paths = append(paths, p)
				}
				sort.Strings(paths)
				for _, p := range paths {
					ranges := make([]string, 0, len(truncatedReadPaths[p]))
					for r := range truncatedReadPaths[p] {
						ranges = append(ranges, r)
					}
					sort.Strings(ranges)
					sb.WriteString(fmt.Sprintf("- %s: %s\n", p, strings.Join(ranges, ", ")))
				}
				sb.WriteString("Do NOT re-read these ranges unless explicitly asked.")
				truncateText = sb.String()
			}

			processedMessages = append(processedMessages, message.Message{
				Role: message.System,
				Parts: []message.ContentPart{
					message.TextContent{Text: truncateText},
				},
			})
			break
		}

		totalSize += msgSize
		processedMessages = append(processedMessages, m)
		keptMessagesFromTail++
	}

	// Reconstruct Final History (Reverse processedMessages and prepend System)
	// Optimization: Pre-allocate exact size
	finalHistory := make([]message.Message, 0, len(processedMessages)+2)
	if systemPrompt != nil {
		finalHistory = append(finalHistory, *systemPrompt)
	}

	for i := len(processedMessages) - 1; i >= 0; i-- {
		finalHistory = append(finalHistory, processedMessages[i])
	}

	logging.Info("Context Prepared", "original", len(msgHistory), "final", len(finalHistory), "chars", totalSize)
	return finalHistory, nil
}

func (a *agent) prepareConversationContext(ctx context.Context, sessionID string, msgHistory []message.Message) ([]message.Message, error) {
	model := models.Model{}
	if a.provider != nil {
		model = a.provider.Model()
	}
	return a.prepareConversationContextForModel(ctx, sessionID, msgHistory, model)
}

type structuredToolResultFoldConfig struct {
	textLimit     int
	headKeep      int
	tailKeep      int
	maxArrayItems int
}

func truncateToolResultContent(content string, ctxCfg ContextWindowConfig) string {
	if len(content) <= ctxCfg.ToolOutputMaxChars {
		return content
	}
	for _, maxArrayItems := range []int{8, 4, 2} {
		if folded, ok := foldStructuredToolResultContent(content, ctxCfg, maxArrayItems); ok {
			return folded
		}
	}
	return foldPlainToolResultText(content, ctxCfg.ToolOutputMaxChars, ctxCfg.HeadKeepChars, ctxCfg.TailKeepChars)
}

func foldStructuredToolResultContent(content string, ctxCfg ContextWindowConfig, maxArrayItems int) (string, bool) {
	var payload any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return "", false
	}
	textLimit := maxInt(256, minInt(ctxCfg.ToolOutputMaxChars/4, 2048))
	headKeep := maxInt(64, textLimit/2)
	tailKeep := maxInt(48, textLimit/3)
	folded := foldStructuredToolResultValue(payload, structuredToolResultFoldConfig{
		textLimit:     textLimit,
		headKeep:      headKeep,
		tailKeep:      tailKeep,
		maxArrayItems: maxArrayItems,
	})
	encoded, err := json.Marshal(folded)
	if err != nil || len(encoded) > ctxCfg.ToolOutputMaxChars {
		return "", false
	}
	return string(encoded), true
}

func foldStructuredToolResultValue(value any, cfg structuredToolResultFoldConfig) any {
	switch typed := value.(type) {
	case map[string]any:
		folded := make(map[string]any, len(typed))
		for key, child := range typed {
			folded[key] = foldStructuredToolResultValue(child, cfg)
		}
		return folded
	case []any:
		if len(typed) <= cfg.maxArrayItems {
			folded := make([]any, 0, len(typed))
			for _, item := range typed {
				folded = append(folded, foldStructuredToolResultValue(item, cfg))
			}
			return folded
		}
		headCount := cfg.maxArrayItems/2 + cfg.maxArrayItems%2
		tailCount := cfg.maxArrayItems / 2
		folded := make([]any, 0, headCount+1+tailCount)
		for index := 0; index < headCount && index < len(typed); index++ {
			folded = append(folded, foldStructuredToolResultValue(typed[index], cfg))
		}
		omitted := len(typed) - headCount - tailCount
		if omitted > 0 {
			folded = append(folded, map[string]any{
				"_folded_items": omitted,
				"_note":         "history items omitted",
			})
		}
		startTail := len(typed) - tailCount
		if startTail < headCount {
			startTail = headCount
		}
		for index := startTail; index < len(typed); index++ {
			folded = append(folded, foldStructuredToolResultValue(typed[index], cfg))
		}
		return folded
	case string:
		return foldPlainToolResultText(typed, cfg.textLimit, cfg.headKeep, cfg.tailKeep)
	default:
		return value
	}
}

func foldPlainToolResultText(content string, maxChars, headKeep, tailKeep int) string {
	if maxChars <= 0 {
		return content
	}
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}
	marker := " ... [System: Content Folded] ... "
	markerLen := len([]rune(marker))
	if maxChars <= markerLen+2 {
		return string(runes[:maxChars])
	}
	keep := maxChars - markerLen
	if headKeep+tailKeep > keep {
		headKeep = keep/2 + keep%2
		tailKeep = keep / 2
	}
	if headKeep <= 0 {
		headKeep = keep/2 + keep%2
	}
	if tailKeep <= 0 {
		tailKeep = keep / 2
	}
	if headKeep+tailKeep > len(runes) {
		return string(runes[:maxChars])
	}
	return string(runes[:headKeep]) + marker + string(runes[len(runes)-tailKeep:])
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// retrieveRAGContext fetches relevant memories and past messages based on the content.
// It supports caching critical memories to avoid redundant DB fetches in loops.
func isCriticalMemoryCategory(category string) bool {
	switch strings.TrimSpace(strings.ToLower(category)) {
	case "habit", "preference", "interest", "instruction", "experience", "session_summary":
		return true
	default:
		return false
	}
}

func buildMemoryCapsuleMessage(memories []db.Memory) *message.Message {
	if len(memories) == 0 {
		return nil
	}

	lines := []string{
		"[MEMORY CAPSULE]",
		"Treat this block as semi-static long-term context. Reuse these durable preferences, instructions, experiences, and continuity cues before re-reading or re-planning.",
	}
	for _, memory := range memories {
		lines = append(lines, fmt.Sprintf("- [%s] %s", memory.Category, memory.Content))
	}
	return buildSystemTextMessage(lines)
}

func buildTurnContextMessage(memoryLines []string, warmContextLines []string) *message.Message {
	sections := make([]string, 0, 2)
	if len(memoryLines) > 0 {
		sections = append(sections, strings.Join(append([]string{
			"[TURN CONTEXT]",
			"Use this block only as turn-local supporting context. Prefer the Memory Capsule and Working State when they already answer the question.",
			"## Relevant Knowledge",
		}, memoryLines...), "\n"))
	}
	if len(warmContextLines) > 0 {
		sections = append(sections, strings.Join(append([]string{
			"## Warm Context",
		}, warmContextLines...), "\n"))
	}
	if len(sections) == 0 {
		return nil
	}

	return &message.Message{
		Role: message.System,
		Parts: []message.ContentPart{
			message.TextContent{Text: strings.Join(sections, "\n\n")},
		},
	}
}

func normalizePromptMemoryContent(s string) string {
	s = reMemoryTags.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	s = strings.TrimRight(s, ".,，!！?？")
	s = reMultiSpace.ReplaceAllString(s, " ")
	return s
}

func shouldSkipCriticalCacheMemory(m db.Memory) bool {
	return strings.Contains(m.Content, "Error:") && len(m.Content) < 50
}

func (a *agent) buildStableCriticalCache(ctx context.Context, sessionID string) []db.Memory {
	if a.memories == nil {
		return nil
	}

	seenIDs := make(map[string]bool)
	seenContent := make(map[string]bool)
	critical := make([]db.Memory, 0)
	add := func(m db.Memory) {
		if cleaned, ok := sanitizeStableMemoryForPrompt(m.Category, m.Content); !ok {
			return
		} else {
			m.Content = cleaned
		}
		if shouldSkipCriticalCacheMemory(m) {
			return
		}
		if seenIDs[m.ID] {
			return
		}
		norm := normalizePromptMemoryContent(m.Content)
		if seenContent[norm] {
			return
		}
		seenIDs[m.ID] = true
		seenContent[norm] = true
		critical = append(critical, m)
	}

	for _, category := range []string{"habit", "preference", "interest", "experience", "instruction"} {
		memories, err := a.memories.ListByCategory(ctx, category)
		if err != nil {
			continue
		}
		for _, m := range memories {
			add(m)
		}
	}

	continuity, err := a.listContinuityMemories(ctx, sessionID, 5)
	if err == nil {
		for _, m := range continuity {
			add(m)
		}
	}

	return critical
}

func (a *agent) retrieveRAGContext(ctx context.Context, sessionID string, content string, mode string, intent string, cachedCriticalMemories *ragMemoryCache, cachedDeepMemories *ragMemoryCache) (*ragContextLayers, error) {
	// SOTA OPTIMIZATION: Intent-Based Adaptive Layered Loading
	// Layer 0: Core Identity (Always On - Handled by System Prompt)
	// Layer 1: User Profile (Habits/Preferences) - Always On
	// Layer 2: Experience/Safety (Pitfalls) - Conditional (Task/Plan)
	// Layer 3: Knowledge/Docs - Lazy (Search/Query)
	// Layer 4: History/Chat - Adaptive (Clarify/Reference)

	// Core memories (L1/L2) are ALWAYS loaded synchronously (fast path)
	// Intent is NOT used for filtering; only used later for sorting/prioritization.
	loadLayer1 := true
	loadLayer2 := true
	loadLayer3 := false
	loadLayer4 := false
	trimmedContent := strings.TrimSpace(content)
	lowerIntent := strings.ToLower(strings.TrimSpace(intent))
	warmContextStatus := ""

	// OPTIMIZATION: Skip heavy RAG (Embeddings/Network) for simple greetings
	// This reduces TTFT from ~4s to ~100ms for "Hello/你好"
	skipHeavyRAG := false
	if trimmedContent == "" {
		warmContextStatus = "disabled_no_user_input"
	}
	if len(content) < 20 {
		lower := strings.ToLower(strings.TrimSpace(content))
		if lower == "hi" || lower == "hello" || lower == "你好" || lower == "早安" || lower == "ok" || lower == "thanks" || lower == "谢谢" {
			skipHeavyRAG = true
			warmContextStatus = "disabled_short_input"
			logging.Info("PERF: Skipping heavy RAG for short greeting", "content", content)
			// Reset heavy layers for greeting (keep core memories)
			loadLayer3 = false
			loadLayer4 = false
		}
	}
	if trimmedContent != "" && !skipHeavyRAG && a.messages == nil {
		warmContextStatus = "disabled_message_store_unavailable"
	}

	if trimmedContent != "" && !skipHeavyRAG {
		loadLayer3 = true
		if mode == "autonomous" || containsContinuationCue(trimmedContent) || isGenericContinuationPrompt(trimmedContent) ||
			strings.Contains(lowerIntent, "search") || strings.Contains(lowerIntent, "debug") ||
			strings.Contains(lowerIntent, "clar") || strings.Contains(lowerIntent, "follow") ||
			strings.Contains(lowerIntent, "refer") || strings.Contains(lowerIntent, "continue") {
			loadLayer4 = true
		}
	}

	ragStart := time.Now()
	var layers ragContextLayers

	var (
		criticalWG sync.WaitGroup // L1, L2, L5 (Fast/Critical)
		deepWG     sync.WaitGroup // L3, L4 (Slow/Deep)
		mu         sync.Mutex

		allMemories []db.Memory
		seenIDs     = make(map[string]bool)
		seenContent = make(map[string]bool)

		pastMessages []message.Message
		pastMsgsErr  error

		criticalCacheState            = "disabled"
		dynamicCacheState             = "disabled"
		implicitGlobalFallbackSkipped bool
		warmContextSourceScope        string
		warmContextSourceSessionID    string
	)

	addMemory := func(m db.Memory) {
		if cleaned, ok := sanitizeStableMemoryForPrompt(m.Category, m.Content); !ok {
			return
		} else {
			m.Content = cleaned
		}

		mu.Lock()
		defer mu.Unlock()
		if !seenIDs[m.ID] {
			norm := normalizePromptMemoryContent(m.Content)
			if !seenContent[norm] {
				allMemories = append(allMemories, m)
				seenIDs[m.ID] = true
				seenContent[norm] = true
			}
		}
	}

	// CACHE LOGIC: Check if we have cached memories with validity check
	useCriticalCache := false
	useDeepCache := false

	// Check cache validity (critical: 5m, deep: 2s)
	const criticalTTL = 5 * time.Minute
	const deepTTL = 2 * time.Second

	if cachedCriticalMemories != nil {
		criticalCacheState = "cold_start_refresh"
		a.memoryCacheMu.RLock()
		currentVersion := a.memoryCriticalVersion
		a.memoryCacheMu.RUnlock()
		cacheAge := time.Since(cachedCriticalMemories.LoadedAt)

		if len(cachedCriticalMemories.Memories) > 0 && !cachedCriticalMemories.LoadedAt.IsZero() && cachedCriticalMemories.Version == currentVersion && cacheAge < criticalTTL {
			useCriticalCache = true
			criticalCacheState = "hit"
			logging.Info("Using cached critical memories (fresh)", "count", len(cachedCriticalMemories.Memories), "age_seconds", cacheAge.Seconds())
			for _, m := range cachedCriticalMemories.Memories {
				addMemory(m)
			}
		} else if !cachedCriticalMemories.LoadedAt.IsZero() || len(cachedCriticalMemories.Memories) > 0 {
			criticalCacheState = "expired_refresh"
			logging.Info("Critical memory cache expired, forcing refresh", "age_seconds", cacheAge.Seconds(), "local_version", cachedCriticalMemories.Version, "current_version", currentVersion)
		}
	}

	if cachedDeepMemories != nil {
		dynamicCacheState = "cold_start_refresh"
		a.memoryCacheMu.RLock()
		currentVersion := a.memoryDynamicVersion
		a.memoryCacheMu.RUnlock()
		cacheAge := time.Since(cachedDeepMemories.LoadedAt)

		if len(cachedDeepMemories.Memories) > 0 && !cachedDeepMemories.LoadedAt.IsZero() && cachedDeepMemories.Version == currentVersion && cacheAge < deepTTL {
			useDeepCache = true
			dynamicCacheState = "hit"
			logging.Info("Using cached deep memories", "count", len(cachedDeepMemories.Memories), "age_seconds", cacheAge.Seconds())
			for _, m := range cachedDeepMemories.Memories {
				addMemory(m)
			}
		} else if !cachedDeepMemories.LoadedAt.IsZero() || len(cachedDeepMemories.Memories) > 0 {
			dynamicCacheState = "expired_refresh"
			logging.Info("Deep memory cache expired, forcing refresh", "age_seconds", cacheAge.Seconds(), "local_version", cachedDeepMemories.Version, "current_version", currentVersion)
		}
	}

	if a.memories != nil {
		// Layer 1: Static Memories (Habits, Preferences) - User Scope - ALWAYS ON
		if loadLayer1 && !useCriticalCache {
			criticalWG.Add(1)
			go func() {
				defer criticalWG.Done()
				defer func() {
					if r := recover(); r != nil {
						logging.Error("Panic in Memory List", "error", r)
					}
				}()

				// OPTIMIZATION: Run category fetches in parallel
				var catWg sync.WaitGroup
				// Search for user-specific categories first (High Priority)
				for _, cat := range []string{"habit", "preference", "interest"} {
					catWg.Add(1)
					go func(c string) {
						defer catWg.Done()
						// Phase 1 Optimization: Dynamic Threshold
						// SOTA: Increased from 10 to 20 for rich user profiles (MaxL1Items)
						var mems []db.Memory
						var err error

						// Simple heuristic: If content provided, try scoped search for large categories
						// But for L1 we usually want all. Let's keep L1 static unless huge.
						// To implement properly we need Count() but we can just fetch and truncate or search if needed.
						// For now, let's just stick to List() but acknowledge the plan.
						// Or better: Implement the Phase 1 logic here directly?
						// "If Count > Threshold -> Search".
						// Since we don't have Count() exposed efficiently yet without a DB call,
						// and List() is cached, List() is actually faster for small N.
						// Let's rely on List() for now as cache is in-memory.
						// But if we wanted to be strict:
						mems, err = a.memories.ListByCategory(ctx, c)
						if err == nil {
							// If too many, and we have content, re-rank or search?
							// Since List() returns all from cache, we can just filter here if needed.
							// But the plan says "Switch to Semantic Search".
							// Let's use the new SearchByCategory if the list is huge.
							if len(mems) > MaxL1Items && content != "" {
								searchReq := memory.DefaultCategorySearchRequest(content, c, MaxL1Items)
								searchReq.Stage = "l1_profile"
								searchReq.MaxFTSCandidates = MaxL1Items * 3
								searchReq.MaxVectorCandidates = MaxL1Items * 2
								searchRes, searchErr := a.memories.SearchByCategoryWithPolicy(ctx, searchReq)
								if searchErr == nil {
									logMemorySearchDecision(searchRes.Decision)
								}
								if searchErr == nil && len(searchRes.Memories) > 0 {
									mems = searchRes.Memories
								} else {
									// Fallback to recent N if search fails
									if len(mems) > MaxL1Items {
										mems = mems[:MaxL1Items]
									}
								}
							}

							for _, m := range mems {
								addMemory(m)
							}
						}
					}(cat)
				}
				catWg.Wait()
			}()
		}

		// Layer 2: Experience (Safety/Pitfalls) - ALWAYS ON
		if loadLayer2 && !useCriticalCache {
			criticalWG.Add(1)
			go func() {
				defer criticalWG.Done()
				defer func() {
					if r := recover(); r != nil {
						logging.Error("Panic in Experience List", "error", r)
					}
				}()
				// FIX: Load both 'experience' and 'instruction' categories for safety
				// 'experience' = learned pitfalls, 'instruction' = explicit user rules
				// OPTIMIZATION: Concurrently fetch 'experience' and 'instruction' to avoid timeout
				var l2WG sync.WaitGroup
				l2WG.Add(2)

				for _, cat := range []string{"experience", "instruction"} {
					go func(c string) {
						defer l2WG.Done()
						// Phase 1 Optimization: Dynamic Threshold for L2
						// SOTA: Increased from 10 to 25 to prioritize safety/learning (MaxL2Items)
						// If user has MANY experiences, listing all is slow.
						// We try to list first (assuming < 100), if huge we search.
						// Actually ListByCategory is usually indexed and fast.
						mems, err := a.memories.ListByCategory(ctx, c)
						if err == nil {
							// Apply Dynamic Threshold Logic
							if len(mems) > MaxL2Items && content != "" {
								searchReq := memory.DefaultCategorySearchRequest(content, c, MaxL2Items)
								searchReq.Stage = "l2_safety"
								searchReq.MaxFTSCandidates = MaxL2Items * 3
								searchReq.MaxVectorCandidates = MaxL2Items * 2
								searchRes, searchErr := a.memories.SearchByCategoryWithPolicy(ctx, searchReq)
								if searchErr == nil {
									logMemorySearchDecision(searchRes.Decision)
								}
								if searchErr == nil && len(searchRes.Memories) > 0 {
									mems = searchRes.Memories
								} else {
									// Truncate if search failed (fallback to most recent)
									mems = mems[:MaxL2Items]
								}
							}

							// QUALITY FILTER: Deprioritize "Error" memories if they are old or conflict with new rules
							// We want to avoid "Zombie Errors" where the agent remembers a tool was broken
							// even after it has been fixed.
							// Heuristic: If we have a 'rule' or 'instruction' that contradicts an 'experience' (error),
							// the rule should win.
							// For now, we just add them, but we could add a "Confidence" score later.

							for _, m := range mems {
								// Basic Sanitization: Skip memories that look like raw error logs without context
								if strings.Contains(m.Content, "Error:") && len(m.Content) < 50 {
									// Skip short generic errors
									continue
								}
								addMemory(m)
							}
						}
					}(cat)
				}
				l2WG.Wait()
			}()
		}

		// Layer 5: High-level Continuity (session_summary) - ALWAYS ON
		if !useCriticalCache {
			criticalWG.Add(1)
			go func() {
				defer criticalWG.Done()
				mems, err := a.listContinuityMemories(ctx, sessionID, 5)
				if err == nil {
					for _, m := range mems {
						addMemory(m)
					}
				}
			}()
		}

		// Layer 3: Semantic Search for Knowledge - Conditional
		if loadLayer3 && content != "" && !skipHeavyRAG {
			deepWG.Add(1)
			go func() {
				defer deepWG.Done()
				defer func() {
					if r := recover(); r != nil {
						logging.Error("Panic in Memory Search", "error", r)
					}
				}()
				searchReq := memory.DefaultSearchRequest(content, MaxKnowledgeItems)
				searchReq.Stage = "l3_knowledge"
				searchReq.MaxFTSCandidates = MaxKnowledgeItems * 3
				searchReq.MaxVectorCandidates = MaxKnowledgeItems * 3
				searchRes, err := a.memories.SearchWithPolicy(ctx, searchReq)
				if err == nil {
					logMemorySearchDecision(searchRes.Decision)
				}
				if err == nil && searchRes != nil {
					for _, m := range searchRes.Memories {
						addMemory(m)

						// Phase 3 Optimization: GraphRAG Expansion
						// Fetch related memories connected via edges
						// We only expand top hits (e.g. top 2) to avoid explosion
						// Using memory service's GetRelated is cleaner than direct query
						related, err := a.memories.GetRelated(ctx, m.ID)
						if err == nil {
							expandedCount := 0
							for _, r := range related {
								if expandedCount >= 2 { // Limit expansion per node
									break
								}
								// Only include strong connections
								if r.Weight > 0.7 {
									// Add to memories if not already present (addMemory handles dedup)
									// Append annotation to content to indicate source?
									// "Gin Framework (Related to: Golang HTTP)"
									// For now, just add raw memory.
									addMemory(r.Memory)
									expandedCount++
								}
							}
						}
					}
				}
			}()
		}
	}

	// Layer 4: Message RAG (Warm Context - History) - Conditional
	// We enable this if intent is search/clarify OR if we are truncating history (to recall what was lost).
	// Ideally we check if truncation happened, but 'prepareConversationContext' happens BEFORE this.
	// For now, let's enable it more aggressively for Task/Search intents.
	if (loadLayer3 || loadLayer4) && content != "" && a.messages != nil && !skipHeavyRAG {
		deepWG.Add(1)
		go func() {
			defer deepWG.Done()
			defer func() {
				if r := recover(); r != nil {
					logging.Error("Panic in Message RAG", "error", r)
				}
			}()

			// OPTIMIZATION: Enrich query context for short inputs
			// If input is short (<10 chars) like "continue" or "fix it", RAG will fail.
			// We should append the LAST ASSISTANT MESSAGE to the query to find related history.
			// Since we don't have easy access to full history here (it's in 'msgHistory' inside 'Run'),
			// we can't easily do it without changing signature.
			// But 'content' here is the User Input.
			// Let's rely on the user providing some context or the semantic search being smart enough.
			// TODO: Pass 'lastAssistantMessage' to retrieveRAGContext for better query expansion.

			// WARM CONTEXT: session-local first, then global fallback.
			var (
				msgs []message.Message
				err  error
			)
			sourceScope := ""
			sourceSessionID := ""
			skippedGlobalFallback := false

			if sessionID != "" {
				for _, scope := range a.warmContextSearchScopes(ctx, sessionID) {
					scopedMsgs, searchErr := a.messages.SearchInSession(ctx, scope.SessionID, content, MaxPastMessages)
					if searchErr != nil {
						if err == nil {
							err = searchErr
						}
						logging.Warn("Session-scoped message RAG failed", "scope", scope.Label, "search_session_id", scope.SessionID, "error", searchErr)
					}
					if len(scopedMsgs) > 0 {
						msgs = scopedMsgs
						sourceScope = scope.Label
						sourceSessionID = scope.SessionID
						break
					}
					if !loadLayer4 {
						continue
					}
					recentMsgs, recentErr := a.recentWarmContextMessages(ctx, scope.SessionID, scope.Label, MaxPastMessages)
					if recentErr != nil {
						if err == nil {
							err = recentErr
						}
						logging.Warn("Session-window warm-context fallback failed", "scope", scope.Label, "search_session_id", scope.SessionID, "error", recentErr)
						continue
					}
					if len(recentMsgs) == 0 {
						continue
					}
					msgs = recentMsgs
					sourceScope = scope.Label
					sourceSessionID = scope.SessionID
					break
				}
			}

			if len(msgs) == 0 {
				if strings.TrimSpace(sessionID) == "" {
					fallbackMsgs, fallbackErr := a.messages.Search(ctx, content, MaxPastMessages)
					if fallbackErr != nil {
						if err == nil {
							err = fallbackErr
						}
					} else {
						msgs = fallbackMsgs
						if len(fallbackMsgs) > 0 {
							sourceScope = "global"
							sourceSessionID = ""
						}
					}
				} else {
					skippedGlobalFallback = true
					logging.Info("Skipping implicit global warm-context fallback for session-bound request", "session_id", sessionID)
				}
			}

			// Filter out messages that are ALREADY in the Hot Context (to avoid duplication)
			// Note: We don't have access to 'msgHistory' here easily, but 'pastMessages' will be
			// appended to System Prompt. If it duplicates recent history, it wastes tokens but isn't fatal.
			// Ideally we'd pass 'hotContextIDs' to this function.
			// Future optimization: Pass exclusion list to Search.

			mu.Lock()
			pastMessages = msgs
			switch {
			case len(msgs) > 0:
				warmContextStatus = "hit"
			case err != nil:
				warmContextStatus = "search_error"
			case skippedGlobalFallback:
				warmContextStatus = "session_scope_miss"
			case strings.TrimSpace(sessionID) == "":
				warmContextStatus = "global_miss"
			case warmContextStatus == "":
				warmContextStatus = "session_scope_miss"
			}
			if len(msgs) > 0 {
				if sourceScope == "" {
					sourceScope = "current_session"
					sourceSessionID = sessionID
				}
				warmContextSourceScope = sourceScope
				warmContextSourceSessionID = sourceSessionID
			}
			implicitGlobalFallbackSkipped = skippedGlobalFallback
			pastMsgsErr = err
			mu.Unlock()
		}()
	}

	// Wait for Critical Context (L1, L2, L5) - Tight Timeout for low TTFT
	// Goal: Always include core memories, but don't block first token too long.
	// If cached, we assume it's already in allMemories.
	warmContextEligible := (loadLayer3 || loadLayer4) && trimmedContent != "" && a.messages != nil && !skipHeavyRAG
	criticalTimeout, deepTimeout := a.retrievalWaitBudgets(ctx, sessionID, mode, intent, warmContextEligible)

	criticalLoaded := useCriticalCache
	if !useCriticalCache {
		criticalDone := make(chan struct{})
		go func() {
			criticalWG.Wait()
			close(criticalDone)
		}()

		select {
		case <-criticalDone:
			// Critical loaded
			criticalLoaded = true
		case <-time.After(criticalTimeout):
			logging.Info("Critical RAG timed out - proceeding with partial core context", "timeout", criticalTimeout)
		}
	}

	// 2) Deep Context (L3/L4) is Async Prefetch (Do NOT wait)
	// We only include it if it finished instantly; otherwise it will benefit the NEXT turn.
	deepLoaded := useDeepCache
	if !useDeepCache {
		deepDone := make(chan struct{})
		go func() {
			deepWG.Wait()
			close(deepDone)
		}()

		select {
		case <-deepDone:
			logging.Info("Deep RAG finished instantly - including in this turn")
			deepLoaded = true
		case <-time.After(deepTimeout):
			logging.Info("Deep RAG timed out - proceeding without blocking current turn", "timeout_ms", deepTimeout.Milliseconds())
		}
	} else {
		// If cached, we assume criticalWG is empty or we didn't add to it.
		// (We gated Add(1) with !useCriticalCache, so it's fine)
		logging.Info("Deep RAG skipped (using cache)")
	}

	// Acquire lock to prevent race condition with late-finishing goroutines.
	// All reads of allMemories MUST happen after this lock.
	mu.Lock()
	defer mu.Unlock()

	logging.Info("PERF: RAG retrieval finished", "duration_ms", time.Since(ragStart).Milliseconds(), "intent", intent, "layers", fmt.Sprintf("L1:%v L2:%v L3:%v", loadLayer1, loadLayer2, loadLayer3), "memories", len(allMemories))

	// UPDATE CRITICAL CACHE: Only update if we actually have critical memories
	if cachedCriticalMemories != nil && !useCriticalCache && criticalLoaded {
		crit := a.buildStableCriticalCache(ctx, sessionID)
		a.memoryCacheMu.RLock()
		cachedCriticalMemories.Version = a.memoryCriticalVersion
		a.memoryCacheMu.RUnlock()
		cachedCriticalMemories.Memories = crit
		cachedCriticalMemories.LoadedAt = time.Now()
		a.memoryCacheMu.Lock()
		a.memoryCriticalCacheLoadedAt = cachedCriticalMemories.LoadedAt
		a.memoryCacheMu.Unlock()
		logging.Info("Critical memory cache refreshed", "critical_count", len(crit), "version", a.memoryCriticalVersion)
	}

	// UPDATE DEEP CACHE: Update even if empty (short TTL prevents lock-in)
	if cachedDeepMemories != nil && !useDeepCache && deepLoaded {
		// Keep L3 (Knowledge)
		var deep []db.Memory
		for _, m := range allMemories {
			if m.Category != "habit" && m.Category != "preference" && m.Category != "interest" &&
				m.Category != "experience" && m.Category != "instruction" &&
				m.Category != "session_summary" {
				deep = append(deep, m)
			}
		}

		a.memoryCacheMu.RLock()
		cachedDeepMemories.Version = a.memoryDynamicVersion
		a.memoryCacheMu.RUnlock()
		cachedDeepMemories.Memories = deep
		cachedDeepMemories.LoadedAt = time.Now()

		a.memoryCacheMu.Lock()
		a.memoryDynamicCacheLoadedAt = cachedDeepMemories.LoadedAt
		a.memoryCacheMu.Unlock()

		logging.Info("Deep memory cache refreshed", "deep_count", len(deep), "version", a.memoryDynamicVersion)
	}

	// Construct Memory Message
	var (
		capsuleMemories []db.Memory
		turnMemories    []db.Memory
	)

	// Process Memories
	if len(allMemories) > 0 {
		maxMemories := MaxTotalMemories // SOTA: Increased from 10 to 40 to allow richer context
		count := 0

		var usedMemoryIDs []string

		// Sort memories to group by category/layer for better context structure
		// This simulates a "Layered" presentation to the model
		sort.Slice(allMemories, func(i, j int) bool {
			confidenceValue := func(value sql.NullFloat64) float64 {
				if value.Valid {
					return value.Float64
				}
				return 0.5
			}
			lastAccessed := func(value sql.NullInt64, fallback int64) int64 {
				if value.Valid {
					return value.Int64
				}
				return fallback
			}

			// SOTA Memory Hierarchy:
			// L1: Core Identity & Preferences (WHO I AM & WHO YOU ARE) - Highest Priority
			// L2: Safety & Experience (WHAT I LEARNED) - High Priority
			// L5: Session Context (WHERE WE ARE) - Medium Priority
			// L3: Knowledge (WHAT I KNOW) - Low Priority (Reference)
			// L4: History (WHAT HAPPENED) - Lowest (Appended separately)

			score := func(m db.Memory) int {
				switch m.Category {
				// L1: Core User Context
				case "preference": // User Preferences
					return 0
				case "habit": // User Habits
					return 1
				case "interest": // User Interests
					return 2

				// L2: Safety & Operational Guidelines
				case "instruction": // Explicit Instructions
					return 3
				case "experience": // Learned Pitfalls
					return 4
				case "rule": // General Rules
					return 5

				// L5: High-level Continuity
				case "session_summary":
					return 6

				// L3: Knowledge Base
				case "knowledge":
					return 7
				default:
					return 8
				}
			}
			sI := score(allMemories[i])
			sJ := score(allMemories[j])
			if sI != sJ {
				return sI < sJ
			}
			leftConfidence := confidenceValue(allMemories[i].Confidence)
			rightConfidence := confidenceValue(allMemories[j].Confidence)
			if leftConfidence != rightConfidence {
				return leftConfidence > rightConfidence
			}
			leftLastAccessed := lastAccessed(allMemories[i].LastAccessedAt, allMemories[i].UpdatedAt)
			rightLastAccessed := lastAccessed(allMemories[j].LastAccessedAt, allMemories[j].UpdatedAt)
			if leftLastAccessed != rightLastAccessed {
				return leftLastAccessed > rightLastAccessed
			}
			if allMemories[i].UpdatedAt != allMemories[j].UpdatedAt {
				return allMemories[i].UpdatedAt > allMemories[j].UpdatedAt
			}
			// Secondary sort by category string for stability
			if allMemories[i].Category != allMemories[j].Category {
				return allMemories[i].Category < allMemories[j].Category
			}
			// Tertiary sort by content for deterministic output
			return allMemories[i].Content < allMemories[j].Content
		})

		for _, m := range allMemories {
			if count >= maxMemories {
				break
			}
			if isCriticalMemoryCategory(m.Category) {
				capsuleMemories = append(capsuleMemories, m)
			} else {
				turnMemories = append(turnMemories, m)
			}
			usedMemoryIDs = append(usedMemoryIDs, m.ID)
			count++
		}

		// Store for decay/confidence updates later
		if sessionID != "" && len(usedMemoryIDs) > 0 {
			a.sessionMemories.Store(sessionID, usedMemoryIDs)
			go func(ids []string) {
				markCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if err := a.memories.MarkUsed(markCtx, ids); err != nil {
					logging.Warn("Failed to mark recalled memories as used", "count", len(ids), "error", err)
				}
			}(append([]string(nil), usedMemoryIDs...))
		}
	}

	if len(capsuleMemories) > 0 {
		layers.MemoryCapsule = a.cachedMemoryCapsuleMessage(sessionID, a.currentMemoryCriticalCacheVersion(), capsuleMemories)
	}

	var turnMemoryLines []string
	if len(turnMemories) > 0 {
		for _, memory := range turnMemories {
			turnMemoryLines = append(turnMemoryLines, fmt.Sprintf("- [%s] %s", memory.Category, memory.Content))
		}
	}

	// Process Past Messages (Warm Context Injection)
	var warmContextLines []string
	if content != "" {
		if pastMsgsErr == nil && len(pastMessages) > 0 {
			warmContextLines = append(warmContextLines, "Here are recent relevant messages that may have fallen outside the hot window:")
			if warmContextSourceScope != "" {
				sourceLine := fmt.Sprintf("[Warm Context Source] %s", warmContextSourceScope)
				if warmContextSourceSessionID != "" && warmContextSourceScope != "current_session" {
					sourceLine = fmt.Sprintf("[Warm Context Source] %s:%s", warmContextSourceScope, warmContextSourceSessionID)
				}
				warmContextLines = append(warmContextLines, sourceLine)
			}
			foundRelevant := false
			seenMsgContent := make(map[string]bool)
			seenMsgContent[strings.TrimSpace(content)] = true

			// Sort by relevance (usually returned by Search, but let's ensure stability)
			// Assuming Search returns in relevance order.

			for _, msg := range pastMessages {
				msgText := msg.Content().String()

				// SOTA: Sanitize History - Strip ephemeral RAG context to prevent recursion
				if idx := strings.Index(msgText, "[CONTEXT INFO]"); idx != -1 {
					msgText = msgText[:idx]
				}
				if idx := strings.Index(msgText, "[SYSTEM INFO]"); idx != -1 {
					msgText = msgText[:idx]
				}

				msgNorm := strings.TrimSpace(msgText)
				if seenMsgContent[msgNorm] {
					continue
				}
				seenMsgContent[msgNorm] = true

				text := truncateRunes(msgText, WarmContextTruncation, "...")
				// Format: [Role] Content
				warmContextLines = append(warmContextLines, fmt.Sprintf("- [%s] %s", msg.Role, text))
				foundRelevant = true
				if len(seenMsgContent) >= MaxWarmContextItems { // SOTA: Increased cap
					break
				}
			}

			if foundRelevant {
				logging.Info("Injected Warm Context (past messages)", "count", len(pastMessages))
			} else {
				warmContextLines = nil
			}
		} else if pastMsgsErr != nil { // unused but kept for structure
			logging.Warn("Failed to search past messages", "error", pastMsgsErr)
		}
	}

	layers.TurnContext = buildTurnContextMessage(turnMemoryLines, warmContextLines)
	layers.Assembly = &ContextAssemblyReport{
		CriticalCache:                 criticalCacheState,
		DynamicCache:                  dynamicCacheState,
		MemoryCapsule:                 layers.MemoryCapsule != nil,
		TurnContext:                   layers.TurnContext != nil,
		CapsuleMemoryCount:            len(capsuleMemories),
		TurnMemoryCount:               len(turnMemories),
		ContinuityMemoryCount:         countMemoriesByCategory(capsuleMemories, "session_summary"),
		SessionSummaryInjected:        countMemoriesByCategory(capsuleMemories, "session_summary") > 0,
		WarmContextSource:             warmContextSourceScope,
		WarmContextSessionID:          warmContextSourceSessionID,
		WarmContextMessages:           len(pastMessages),
		WarmContextStatus:             warmContextStatus,
		ImplicitGlobalFallbackSkipped: implicitGlobalFallbackSkipped,
	}

	return &layers, nil
}

// rebuildDynamicSuffix refreshes the volatile part of the system prompt.
// This is critical for multi-step turns where tool states (loaded/unloaded) might change.
func (a *agent) rebuildDynamicSuffix(ctx context.Context, content string, mode string, intent string, personaOverride string, state *sessionTaskState) string {
	dynamicSuffix := prompt.GetDynamicInstructionForWorkspace(intent, mode, personaOverride, config.WorkingDirectoryFor(ctx))
	diagnosticContinuation := isDiagnosticModeActive(state)
	sessionID, _ := tools.GetContextValues(ctx)
	allowPlanBootstrap := shouldAllowComplexContinuationPlanBootstrap(sessionID, state)

	// Planning & Transparency Injection
	dynamicSuffix += "\n\nIMPORTANT: Output concise <thinking>...</thinking> focused only on new judgment or the next action. Do not restate the user's request, quote prior instructions, or recap already-known evidence. Then immediately answer or call tools in the same turn."
	if diagnosticContinuation {
		dynamicSuffix += "\n\nSUGGESTION: Read-only diagnostic continuation remains active. Reuse saved evidence and continuation state. Do NOT call `manage_plan`, and do NOT ask for write permission unless the user explicitly changes the task."
	}

	// Todo Nudge Heuristic
	lowerContent := strings.ToLower(content)
	if containsContinuationCue(content) || isGenericContinuationPrompt(content) {
		if diagnosticContinuation {
			dynamicSuffix += "\n\nSUGGESTION: Follow-up continuation detected. Reuse existing task state and saved facts first. Do NOT call `manage_plan`; continue the diagnostic phases and extend the report directly. Do not merely restate prior conclusions: resolve remaining findings, add any missing closure statement, or explicitly state that the prior answer was already complete before closing this turn. If the prior answer already appears complete, add one concise user-facing addendum that closes the original request's remaining wording or conclusion gap instead of asking what to do next. Finish the current follow-up turn with the requested result, and do NOT ask for another task in the same turn."
		} else if allowPlanBootstrap {
			dynamicSuffix += "\n\nSUGGESTION: Follow-up continuation detected. Reuse existing task state and saved facts first. No active plan exists yet and the current task is clearly multi-step, so one concise `manage_plan` add bootstrap is allowed before resuming execution. After the plan exists, prefer `manage_plan` list/update/complete and target items by id instead of index. Do NOT call `manage_plan` with empty input. Finish the current follow-up turn with the requested result, and do NOT ask for another task in the same turn."
		} else {
			dynamicSuffix += "\n\nSUGGESTION: Follow-up continuation detected. Reuse existing task state and saved facts first. For an existing plan, use `manage_plan` list/update/complete/archive to keep execution state truthful as planned steps start, finish, fail, or fully close out; prefer id over index. Do NOT call `manage_plan` with empty input or add actions unless the user explicitly asked for a new plan. Finish the current follow-up turn with the requested result, and do NOT ask for another task in the same turn."
		}
	} else if !diagnosticContinuation && shouldSuggestManagePlanNudge(lowerContent) {
		dynamicSuffix += "\n\nSUGGESTION: This task is clearly multi-step. Start by creating a concise `manage_plan` plan before broad edits or multi-step execution, then mark the current step in_progress. Use `manage_plan` with action='add' for new items and use the item id from `manage_plan(action='list')` for follow-up updates instead of relying on indexes. When the user's goal is fully satisfied, complete finished items and archive the plan if no active items remain before the final answer."
	}

	if isWordDocumentEditingContext(content) {
		wordContent := stripWordContextBlock(content)
		hasTableCue := false
		for _, marker := range []string{"表格", "单元格", "那格", "这一格", "右侧结果", "通过率", "金额", "总计", "数量", "占比", "比率"} {
			if strings.Contains(wordContent, marker) {
				hasTableCue = true
				break
			}
		}
		if hasTableCue {
			dynamicSuffix += "\n\nSUGGESTION: For Word table edits, prefer `auramate-word-copilot` with action=`update_table_cell`. When `table_id`, `row_index`, and `cell_index` are known, prefer the shorthand payload `new_text` plus `reader_session_id` instead of `replace` or a verbose `table_cell` body. Even with the shorthand payload, `target_text` is still required and should be the current cell text/value. Even when a simple whole-cell rewrite can relax `format_basis`, do not omit `reader_context_summary`. For right-side value cells, `that cell` references, or in-cell inserts after exploration, also carry `exploration_summary` with the confirmed `table_id`, `row_index`, `cell_index`, and row/cell relationship evidence."
			if state != nil && state.CachedWordTableLocator != nil {
				locator := state.CachedWordTableLocator
				if locator.RowIndex != nil && locator.CellIndex != nil {
					dynamicSuffix += fmt.Sprintf(" Reuse the latest cached locator when it still targets the same cell: reader_session_id=%s, table_id=%s, row_index=%d, cell_index=%d.", locator.ReaderSessionID, locator.TableID, *locator.RowIndex, *locator.CellIndex)
				}
			}
		}
	}

	return dynamicSuffix
}

func shouldSuggestManagePlanNudge(lowerContent string) bool {
	lowerContent = strings.ToLower(strings.TrimSpace(lowerContent))
	if lowerContent == "" {
		return false
	}
	explicitPlanTerms := []string{"plan", "roadmap", "milestone", "todo", "步骤", "规划", "计划"}
	if containsManagePlanHeuristicTerm(lowerContent, explicitPlanTerms) {
		return true
	}
	complexityTerms := []string{"complex", "multi-step", "refactor", "migration", "phased", "阶段", "重构", "迁移", "分步", "多步"}
	if containsManagePlanHeuristicTerm(lowerContent, complexityTerms) {
		return true
	}
	actionTerms := []string{"analyze", "analyse", "debug", "fix", "optimize", "improve", "verify", "validate", "test", "review", "audit", "implement", "design", "排查", "分析", "定位", "修复", "优化", "改造", "验证", "测试", "审查", "实现", "设计", "联调"}
	breadthTerms := []string{" and ", " then ", "同时", "并且", "然后", "以及", "端到端", "e2e", "全链路", "整体", "全面", "多处", "多个", "前后端"}
	return countManagePlanHeuristicMatches(lowerContent, actionTerms) >= 2 && containsManagePlanHeuristicTerm(lowerContent, breadthTerms)
}

func containsManagePlanHeuristicTerm(content string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(content, term) {
			return true
		}
	}
	return false
}

func countManagePlanHeuristicMatches(content string, terms []string) int {
	count := 0
	for _, term := range terms {
		if strings.Contains(content, term) {
			count++
		}
	}
	return count
}

func hasActiveSessionPlan(sessionID string) bool {
	return strings.TrimSpace(sessionID) != "" && strings.TrimSpace(loadActivePlanBaseNameForSession(sessionID)) != ""
}

func shouldAllowComplexContinuationPlanBootstrap(sessionID string, state *sessionTaskState) bool {
	if strings.TrimSpace(sessionID) == "" || state == nil || state.ReadOnlyMode || isDiagnosticModeActive(state) || !hasPlanContinuationState(state) {
		return false
	}
	if hasActiveSessionPlan(sessionID) {
		return false
	}
	taskSummary := strings.Join([]string{state.PrimaryGoal, state.CurrentFocus, state.NextStep}, " ")
	return shouldSuggestManagePlanNudge(taskSummary)
}

// buildSystemMessage constructs the dynamic system prompt based on intent and mode.
// Returns:
// 1. Slice of Static System Messages (Anchors)
// 2. Dynamic Suffix (Volatile Context)
// 3. Error
func (a *agent) buildSystemMessage(ctx context.Context, content string, mode string, providerID models.ModelProvider, intent string, state *sessionTaskState) ([]message.Message, string, error) {
	// Intent is now passed in (Parallel Execution)
	// intent := a.detectIntent(ctx, content, mode) // MOVED TO CALLER

	// Retrieve Persona Override (if any)
	personaOverride := ""
	if mode == "autonomous" && a.q != nil {
		state, err := a.q.GetCognitiveState(ctx, config.CognitiveStateKey(ctx, "agent_persona_life"))
		if err == nil && state.Value.Valid {
			personaOverride = state.Value.String
		}
	}

	// 1. Generate Static Base Prompt (The Anchor)
	contextContent := prompt.GetContextFromPaths()
	staticCacheKey := strings.Join([]string{mode, string(providerID), personaOverride, contextContent}, "\x1f")
	sysMsgs := a.cachedStaticSystemMessages(staticCacheKey, func() []message.Message {
		staticPrompt := prompt.GetStaticBasePrompt(mode, providerID, personaOverride)
		var msgs []message.Message
		msgs = append(msgs, message.Message{
			Role: message.System,
			Parts: []message.ContentPart{
				message.TextContent{Text: staticPrompt},
			},
		})

		if contextContent != "" {
			msgs = append(msgs, message.Message{
				Role: message.System,
				Parts: []message.ContentPart{
					message.TextContent{Text: fmt.Sprintf("# Project-Specific Context\n%s", contextContent)},
				},
			})
		}

		return msgs
	})

	// 2. Generate Initial Dynamic Suffix
	// We use the new helper function to ensure consistency
	dynamicSuffix := a.rebuildDynamicSuffix(ctx, content, mode, intent, personaOverride, state)

	// Keep tool load state out of the static anchor so prefix cache remains stable.
	var unloaded []string
	allowlist := a.deriveSessionToolAllowlist(content)
	if a.skillsManager != nil {
		availableTools := filterToolInfosByAllowlist(a.skillsManager.ListAvailableTools(), allowlist)
		for _, t := range availableTools {
			if !isToolAllowedByAllowlist(allowlist, t.Name) {
				continue
			}
			if !a.IsLoaded(t.Name) {
				desc := t.Description
				if idx := strings.Index(desc, "\n"); idx != -1 {
					desc = desc[:idx]
				}
				desc = truncateRunes(desc, 80, "...")
				unloaded = append(unloaded, fmt.Sprintf("- **%s**: %s", t.Name, desc))
			}
		}
	} else {
		a.toolMutex.RLock()
		for name, t := range a.allTools {
			if !isToolAllowedByAllowlist(allowlist, name) {
				continue
			}
			if !a.activeTools[name] {
				desc := t.Info().Description
				if idx := strings.Index(desc, "\n"); idx != -1 {
					desc = desc[:idx]
				}
				desc = truncateRunes(desc, 80, "...")
				unloaded = append(unloaded, fmt.Sprintf("- **%s**: %s", name, desc))
			}
		}
		a.toolMutex.RUnlock()
	}
	sort.Strings(unloaded)
	const maxUnloaded = 20
	if len(unloaded) > maxUnloaded {
		unloaded = unloaded[:maxUnloaded]
		unloaded = append(unloaded, "- ... (more tools available)")
	}
	if len(unloaded) > 0 {
		dynamicSuffix += "\n\n# AVAILABLE (NOT LOADED) TOOLS\n" + strings.Join(unloaded, "\n")
	}

	logging.Info("Injected dynamic system prompt", "intent", intent, "blocks", len(sysMsgs))

	return sysMsgs, dynamicSuffix, nil
}
