package memory

import (
	"regexp"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/db"
)

var memoryIdentityTokenPattern = regexp.MustCompile(`[a-zA-Z0-9_./\\-]{8,}`)
var stableMemoryReplyTailPattern = regexp.MustCompile(`(?is)[，,。；;\s]*(?:不要调用无关工具[，,。；;\s]*)?(?:最终|只|仅)?(?:回复|输出|回答)(?:为|成)?[：:\s"“”'']*[a-z0-9_ -]{1,64}[”"'']*[，,。；;\s]*$`)
var stableMemoryTestMarkerTailPattern = regexp.MustCompile(`(?is)[，,。；;\s]*标识\s+(?:mem_|overwrite_|reject_|noise_|ack_|ok_)[a-z0-9_:-]*.*$`)

const memoryContractVersion = "v1"

var stableMemoryHabitPhrases = []string{
	"记住这个习惯", "我习惯", "我的习惯", "i usually", "i tend to",
}

var stableMemoryInstructionPhrases = []string{
	"以后默认这样做", "以后默认这样", "默认这样做", "默认这样", "以后都这样", "下次都这样",
	"今后都这样", "以后不要这样", "以后别这样", "请保持这个风格", "请统一按这个风格",
	"以后请", "今后请", "from now on", "by default", "please always", "always use", "always answer",
}

var stableMemoryPreferencePhrases = []string{
	"记住这个偏好", "我更喜欢这种风格", "我更喜欢这个风格", "我更喜欢这样", "我喜欢这种风格",
	"我喜欢这样", "我偏好", "我更偏好", "i prefer", "my preference", "use this style",
}

type MemoryWriteAction string

const (
	MemoryWriteActionInserted          MemoryWriteAction = "inserted"
	MemoryWriteActionReusedExisting    MemoryWriteAction = "reused_existing"
	MemoryWriteActionOverwroteExisting MemoryWriteAction = "overwrote_existing"
	MemoryWriteActionRejectedConflict  MemoryWriteAction = "rejected_conflict"
)

type MemoryWriteReasonCode string

const (
	MemoryWriteReasonContractApplied      MemoryWriteReasonCode = "contract_applied"
	MemoryWriteReasonSourceTrustEvaluated MemoryWriteReasonCode = "source_trust_evaluated"
	MemoryWriteReasonConflictKeyDetected  MemoryWriteReasonCode = "conflict_key_detected"
	MemoryWriteReasonConflictDetected     MemoryWriteReasonCode = "conflict_detected"
	MemoryWriteReasonExactDuplicate       MemoryWriteReasonCode = "exact_duplicate"
	MemoryWriteReasonSemanticDuplicate    MemoryWriteReasonCode = "semantic_duplicate"
	MemoryWriteReasonOverwritePerformed   MemoryWriteReasonCode = "overwrite_performed"
	MemoryWriteReasonConflictRejected     MemoryWriteReasonCode = "conflict_rejected"
	MemoryWriteReasonInsertPerformed      MemoryWriteReasonCode = "insert_performed"
	MemoryWriteReasonEmbeddingGenerated   MemoryWriteReasonCode = "embedding_generated"
	MemoryWriteReasonEmbeddingUnavailable MemoryWriteReasonCode = "embedding_unavailable"
	MemoryWriteReasonSourcePreferred      MemoryWriteReasonCode = "existing_source_preferred"
	MemoryWriteReasonIncomingPreferred    MemoryWriteReasonCode = "incoming_source_preferred"
	MemoryWriteReasonExplicitOverwrite    MemoryWriteReasonCode = "explicit_overwrite_requested"
	MemoryWriteReasonEvidenceAttached     MemoryWriteReasonCode = "evidence_attached"
)

type MemorySearchReasonCode string

const (
	MemorySearchReasonContractApplied    MemorySearchReasonCode = "contract_applied"
	MemorySearchReasonBudgetApplied      MemorySearchReasonCode = "budget_applied"
	MemorySearchReasonExactMatchUsed     MemorySearchReasonCode = "exact_match_used"
	MemorySearchReasonFTSUsed            MemorySearchReasonCode = "fts_used"
	MemorySearchReasonVectorUsed         MemorySearchReasonCode = "vector_used"
	MemorySearchReasonVectorUnavailable  MemorySearchReasonCode = "vector_unavailable"
	MemorySearchReasonCacheWaitTimedOut  MemorySearchReasonCode = "cache_wait_timed_out"
	MemorySearchReasonCacheWaitCancelled MemorySearchReasonCode = "cache_wait_cancelled"
	MemorySearchReasonFTSFallback        MemorySearchReasonCode = "fts_fallback"
	MemorySearchReasonListFallback       MemorySearchReasonCode = "list_fallback"
	MemorySearchReasonCategoryFiltered   MemorySearchReasonCode = "category_filtered"
	MemorySearchReasonNoResults          MemorySearchReasonCode = "no_results"
)

type MemoryWriteRequest struct {
	Category           string
	Content            string
	Source             string
	Trigger            string
	SessionID          string
	Evidence           []string
	ConflictKey        string
	AllowOverwrite     bool
	AllowSemanticDedup bool
}

type MemoryWriteDecision struct {
	Contract            string                  `json:"contract"`
	Action              MemoryWriteAction       `json:"action"`
	Category            string                  `json:"category"`
	Layer               int64                   `json:"layer"`
	Trigger             string                  `json:"trigger,omitempty"`
	ConflictKey         string                  `json:"conflict_key,omitempty"`
	ReasonCodes         []MemoryWriteReasonCode `json:"reason_codes,omitempty"`
	ExistingMemoryID    string                  `json:"existing_memory_id,omitempty"`
	ExistingSource      string                  `json:"existing_source,omitempty"`
	IncomingSource      string                  `json:"incoming_source,omitempty"`
	IncomingSourceTrust int                     `json:"incoming_source_trust,omitempty"`
	ExistingSourceTrust int                     `json:"existing_source_trust,omitempty"`
	EvidenceCount       int                     `json:"evidence_count,omitempty"`
}

type MemoryWriteResult struct {
	Memory   *db.Memory          `json:"memory,omitempty"`
	Decision MemoryWriteDecision `json:"decision"`
}

type MemorySearchRequest struct {
	Query                string
	Category             string
	Stage                string
	Limit                int
	MaxFTSCandidates     int
	MaxVectorCandidates  int
	WaitForCache         time.Duration
	AllowFTS             bool
	AllowVector          bool
	AllowListFallback    bool
	VectorScoreThreshold float32
}

type MemorySearchDecision struct {
	Contract          string                   `json:"contract"`
	Stage             string                   `json:"stage,omitempty"`
	Category          string                   `json:"category,omitempty"`
	Limit             int                      `json:"limit"`
	FTSCandidateLimit int                      `json:"fts_candidate_limit,omitempty"`
	VecCandidateLimit int                      `json:"vector_candidate_limit,omitempty"`
	UsedChannels      []string                 `json:"used_channels,omitempty"`
	FallbackPath      string                   `json:"fallback_path,omitempty"`
	ReasonCodes       []MemorySearchReasonCode `json:"reason_codes,omitempty"`
	DurationMs        int64                    `json:"duration_ms"`
}

type MemorySearchResult struct {
	Memories []db.Memory          `json:"memories"`
	Decision MemorySearchDecision `json:"decision"`
}

func DefaultWriteRequest(category, content, source string) MemoryWriteRequest {
	return MemoryWriteRequest{
		Category:           strings.TrimSpace(category),
		Content:            content,
		Source:             strings.TrimSpace(source),
		Trigger:            strings.TrimSpace(source),
		AllowOverwrite:     false,
		AllowSemanticDedup: true,
	}
}

func DefaultSearchRequest(query string, limit int) MemorySearchRequest {
	if limit <= 0 {
		limit = 10
	}
	return MemorySearchRequest{
		Query:                strings.TrimSpace(query),
		Stage:                "global_search",
		Limit:                limit,
		MaxFTSCandidates:     limit * 2,
		MaxVectorCandidates:  limit * 2,
		WaitForCache:         2 * time.Second,
		AllowFTS:             true,
		AllowVector:          true,
		AllowListFallback:    false,
		VectorScoreThreshold: 0.3,
	}
}

func DefaultCategorySearchRequest(query, category string, limit int) MemorySearchRequest {
	req := DefaultSearchRequest(query, limit)
	req.Stage = "category_search"
	req.Category = strings.TrimSpace(category)
	req.AllowListFallback = true
	req.VectorScoreThreshold = 0.25
	return req
}

func normalizeStableMemoryContent(content string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(content), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cleaned = append(cleaned, line)
	}
	normalized = strings.Join(cleaned, "\n")
	runes := []rune(normalized)
	if len(runes) > 280 {
		normalized = strings.TrimSpace(string(runes[:280])) + "..."
	}
	return normalized
}

func containsStableMemoryPhrase(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func stripStableMemoryTriggerPrefix(content string) string {
	lower := strings.ToLower(content)
	trimPrefix := func(phrases []string) string {
		for _, phrase := range phrases {
			if strings.HasPrefix(lower, phrase) {
				trimmed := strings.TrimSpace(content[len(phrase):])
				trimmed = strings.TrimLeft(trimmed, "：:，,。；; ")
				if trimmed != "" {
					return trimmed
				}
			}
		}
		return content
	}

	if trimmed := trimPrefix(stableMemoryInstructionPhrases); trimmed != content {
		return trimmed
	}
	if trimmed := trimPrefix(stableMemoryPreferencePhrases); trimmed != content {
		return trimmed
	}
	if trimmed := trimPrefix(stableMemoryHabitPhrases); trimmed != content {
		return trimmed
	}
	return content
}

func sanitizeStableMemoryContent(content string) string {
	normalized := normalizeStableMemoryContent(content)
	if normalized == "" {
		return ""
	}

	cleaned := stableMemoryReplyTailPattern.ReplaceAllString(normalized, "")
	cleaned = stableMemoryTestMarkerTailPattern.ReplaceAllString(cleaned, "")
	hadTransientTail := cleaned != normalized
	if !hadTransientTail {
		return normalized
	}

	cleaned = strings.TrimSpace(strings.Trim(cleaned, "，,。；;"))
	if cleaned == "" {
		return ""
	}

	cleaned = stripStableMemoryTriggerPrefix(cleaned)
	cleaned = strings.TrimSpace(strings.Trim(cleaned, "，,。；;"))

	return cleaned
}

func SanitizeMemoryContentForStorage(category, content string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(category)) {
	case "instruction", "preference", "habit":
		cleaned := sanitizeStableMemoryContent(content)
		if cleaned == "" {
			return "", false
		}
		return cleaned, true
	default:
		return content, true
	}
}

func DetectStableMemoryCategory(content string) string {
	normalized := normalizeStableMemoryContent(content)
	if normalized == "" {
		return ""
	}
	lower := strings.ToLower(normalized)
	switch {
	case containsStableMemoryPhrase(lower, stableMemoryHabitPhrases):
		return "habit"
	case containsStableMemoryPhrase(lower, stableMemoryInstructionPhrases):
		return "instruction"
	case containsStableMemoryPhrase(lower, stableMemoryPreferencePhrases):
		return "preference"
	default:
		return ""
	}
}

func canonicalMemorySource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "inference"
	}
	return source
}

func memorySourceTrust(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "user_input", "user", "api_user", "summarizer":
		return 5
	case "reflection", "manage_plan_tool", "system", "treesummarizer", "tree_summarizer":
		return 4
	case "tool", "tool_output", "external_tool":
		return 3
	case "inference", "assistant", "model_inference":
		return 2
	default:
		return 1
	}
}

func appendUniqueWriteReason(codes []MemoryWriteReasonCode, code MemoryWriteReasonCode) []MemoryWriteReasonCode {
	for _, existing := range codes {
		if existing == code {
			return codes
		}
	}
	return append(codes, code)
}

func appendUniqueSearchReason(codes []MemorySearchReasonCode, code MemorySearchReasonCode) []MemorySearchReasonCode {
	for _, existing := range codes {
		if existing == code {
			return codes
		}
	}
	return append(codes, code)
}

func normalizeMemoryConflictKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.Trim(key, "[](){}*` ")
	key = strings.TrimRight(key, ".,，!！?？:：=")
	key = strings.Join(strings.Fields(key), " ")
	return key
}

func extractTaggedValue(content, prefix string) string {
	start := strings.Index(content, prefix)
	if start == -1 {
		return ""
	}
	start += len(prefix)
	end := strings.Index(content[start:], "]")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(content[start : start+end])
}

func extractMarkdownTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		start := strings.Index(line, "**")
		if start == -1 {
			continue
		}
		rest := line[start+2:]
		end := strings.Index(rest, "**")
		if end == -1 {
			continue
		}
		title := strings.TrimSpace(rest[:end])
		if title != "" {
			return title
		}
		break
	}
	return ""
}

func extractKeyValuePrefix(content string) string {
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			continue
		}
		for _, sep := range []string{"：", ":", "="} {
			if idx := strings.Index(line, sep); idx > 0 {
				key := strings.TrimSpace(line[:idx])
				if key != "" {
					return key
				}
			}
		}
		break
	}
	return ""
}

func buildMemoryConflictKey(category, content, override string) string {
	if normalized := normalizeMemoryConflictKey(override); normalized != "" {
		return normalized
	}
	category = strings.ToLower(strings.TrimSpace(category))
	if sessionID := extractTaggedValue(content, "[session_id:"); sessionID != "" {
		return normalizeMemoryConflictKey(category + ":session_id:" + sessionID)
	}
	scope := extractTaggedValue(content, "[Scope:")
	if title := extractMarkdownTitle(content); title != "" {
		if scope != "" {
			return normalizeMemoryConflictKey(category + ":scope:" + scope + ":title:" + title)
		}
		return normalizeMemoryConflictKey(category + ":title:" + title)
	}
	if key := extractKeyValuePrefix(content); key != "" {
		if scope != "" {
			return normalizeMemoryConflictKey(category + ":scope:" + scope + ":field:" + key)
		}
		return normalizeMemoryConflictKey(category + ":field:" + key)
	}
	return ""
}

func shouldOverwriteExistingMemory(category string, incomingTrust, existingTrust int, allowOverwrite bool) bool {
	if allowOverwrite {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(category), "session_summary") {
		return incomingTrust >= existingTrust
	}
	return incomingTrust > existingTrust
}

func extractMemoryIdentityTokens(content string) map[string]struct{} {
	matches := memoryIdentityTokenPattern.FindAllString(strings.ToLower(content), -1)
	if len(matches) == 0 {
		return nil
	}

	tokens := make(map[string]struct{})
	for _, match := range matches {
		token := strings.Trim(match, "[](){}<>\"'`.,，!！?？:：;；")
		if len(token) < 8 {
			continue
		}

		hasDigit := false
		hasSeparator := false
		for _, r := range token {
			if r >= '0' && r <= '9' {
				hasDigit = true
			}
			if r == '_' || r == '-' || r == '.' || r == '/' || r == '\\' {
				hasSeparator = true
			}
		}
		if hasDigit && hasSeparator {
			tokens[token] = struct{}{}
		}
	}

	if len(tokens) == 0 {
		return nil
	}
	return tokens
}

func memoryIdentityTokensCompatible(incomingContent, existingContent string) bool {
	incomingTokens := extractMemoryIdentityTokens(incomingContent)
	existingTokens := extractMemoryIdentityTokens(existingContent)
	if len(incomingTokens) == 0 || len(existingTokens) == 0 {
		return true
	}
	for token := range incomingTokens {
		if _, ok := existingTokens[token]; ok {
			return true
		}
	}
	return false
}

func memoryIdentityTokensOverlap(incomingContent, existingContent string) bool {
	incomingTokens := extractMemoryIdentityTokens(incomingContent)
	existingTokens := extractMemoryIdentityTokens(existingContent)
	if len(incomingTokens) == 0 || len(existingTokens) == 0 {
		return false
	}
	for token := range incomingTokens {
		if _, ok := existingTokens[token]; ok {
			return true
		}
	}
	return false
}

func semanticDedupAllowed(incomingConflictKey, existingConflictKey, incomingContent, existingContent string) bool {
	if incomingConflictKey != "" || existingConflictKey != "" {
		if incomingConflictKey == "" || existingConflictKey == "" {
			return false
		}
		if incomingConflictKey != existingConflictKey {
			return false
		}
		return memoryIdentityTokensCompatible(incomingContent, existingContent)
	}
	return memoryIdentityTokensOverlap(incomingContent, existingContent)
}
