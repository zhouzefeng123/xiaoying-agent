package cognitive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/logging"
)

func (e *Engine) emitDreamThought(type_, content string) {
	e.mu.RLock()
	cb := e.onThought
	e.mu.RUnlock()
	if cb != nil {
		cb(type_, content)
	}
}

// Dream triggers the dreaming process (Deep Consolidation)
func (e *Engine) Dream(ctx context.Context) error {
	logging.Info("Entering REM Cycle (Dreaming)...")
	e.emitDreamThought("state_change", "dreaming")
	e.emitDreamThought("thought", "进入梦境：开始整理近期记忆碎片。")

	// 1. Fragment Organization
	logging.Info("Consolidating memory fragments...", "phase", "fragment_organization")
	e.emitDreamThought("action", "Consolidating L4 episodic fragments -> L2/L3")
	outcome, err := e.consolidateMemories(ctx)
	if err != nil {
		logging.Error("Failed to consolidate memories", "error", err)
		e.emitDreamThought("observation", fmt.Sprintf("梦境整合出现异常：%v", err))
		// Don't fail the whole dream, continue to maintenance
	} else {
		switch {
		case outcome.LongTermCount > 0:
			e.emitDreamThought("observation", fmt.Sprintf("梦境整合完成：新增 %d 条长期记忆，实际清理 %d 条碎片。", outcome.LongTermCount, outcome.DeletedCount))
		case outcome.DeletedCount > 0:
			e.emitDreamThought("observation", fmt.Sprintf("梦境整理完成：未新增长期记忆，已清理 %d 条碎片。", outcome.DeletedCount))
		default:
			e.emitDreamThought("observation", "梦境整理完成：本轮没有需要新增或清理的记忆。")
		}
	}

	// 2. Decay (Forgetting)
	logging.Info("Applying memory decay...", "phase", "decay")
	e.emitDreamThought("action", "Applying decay to stale low-confidence memories")
	if err := e.ikts.DecayMemories(ctx); err != nil {
		logging.Error("Failed to apply memory decay", "error", err)
		e.emitDreamThought("observation", fmt.Sprintf("遗忘阶段出现异常：%v", err))
	} else {
		e.emitDreamThought("observation", "遗忘完成：低价值记忆已衰减/清理。")
	}

	// Simulate deep processing / Sleep cycle
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
		// Short pause to simulate cycle
	}

	logging.Info("Dreaming Cycle Completed. Knowledge consolidated.")
	e.emitDreamThought("thought", "梦境周期结束，返回待机。")
	return nil
}

type ConsolidationResult struct {
	NewMemories []struct {
		Layer     int64    `json:"layer"`
		Category  string   `json:"category"`
		Content   string   `json:"content"`
		Summary   string   `json:"summary"`
		Keywords  string   `json:"keywords"`
		SourceIDs []string `json:"source_ids"`
	} `json:"new_memories"`
	ObsoleteIDs []string `json:"obsolete_ids"`
}

type ConsolidationOutcome struct {
	NewMemoryCount int
	LongTermCount  int
	DeletedCount   int
}

func (e *Engine) consolidateMemories(ctx context.Context) (ConsolidationOutcome, error) {
	outcome := ConsolidationOutcome{}

	// 1. Fetch Episodic Memories (L4)
	fragments, err := e.ikts.GetEpisodicMemories(ctx)
	if err != nil {
		return outcome, fmt.Errorf("failed to fetch fragments: %w", err)
	}

	if len(fragments) == 0 {
		logging.Info("No episodic fragments to consolidate.")
		e.emitDreamThought("observation", "没有可整合的 L4 记忆碎片。")
		return outcome, nil
	}

	e.emitDreamThought("thought", fmt.Sprintf("检测到 %d 条 L4 记忆碎片，准备进行整合。", len(fragments)))

	// Limit batch size to avoid context overflow (e.g., 20 items)
	batchSize := 20
	if len(fragments) > batchSize {
		fragments = fragments[:batchSize]
	}

	// 2. Prepare Prompt
	var sb strings.Builder
	sb.WriteString("Analyze the following episodic memory fragments (Layer 4). \n")
	sb.WriteString("Your goal is to CONSOLIDATE them into long-term knowledge (Layer 3 - Knowledge) or Domain Context (Layer 2 - Domain).\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("1. Identify patterns. If multiple fragments describe the same topic, MERGE them into a single comprehensive L3 memory.\n")
	sb.WriteString("2. Extract recurring user preferences or important project facts.\n")
	sb.WriteString("3. Summarize trivial events into a single timeline entry if necessary, or discard them if they have no long-term value.\n")
	sb.WriteString("4. Create 'NewMemories' for high-value information.\n")
	sb.WriteString("5. Every NewMemory MUST include source_ids listing the fragment IDs absorbed into that L2/L3 memory.\n")
	sb.WriteString("6. List IDs of fragments that are useless or already fully covered in 'ObsoleteIDs'.\n")
	sb.WriteString("7. Return ONLY valid JSON.\n\n")
	sb.WriteString("Fragments:\n")

	for _, f := range fragments {
		// Format: [ID] (Time) Content
		tm := time.Unix(f.CreatedAt, 0).Format(time.RFC3339)
		sb.WriteString(fmt.Sprintf("- [%s] (%s) %s\n", f.ID, tm, f.Content))
	}

	sb.WriteString("\nResponse Format (JSON):\n")
	sb.WriteString(`{
  "new_memories": [
    { "layer": 3, "category": "tech|preference|summary", "content": "...", "summary": "...", "keywords": "...", "source_ids": ["id1", "id2"] }
  ],
  "obsolete_ids": ["id1", "id2"]
}`)

	// 3. Call dedicated Dreaming LLM path
	// Use a lightweight provider call instead of the full agent pipeline.
	// This avoids RAG/thinking/tool orchestration contaminating the JSON payload.
	agentCtx, agentCancel := context.WithTimeout(ctx, 3*time.Minute)
	defer agentCancel()

	respText, err := e.runDreamingLLM(agentCtx, sb.String())
	if err != nil {
		// Graceful degradation: dedicated provider + agent fallback both failed
		// (e.g. empty response, network error, safety stop). Instead of aborting the
		// whole dreaming cycle, treat this as a parse failure so the downstream
		// cleanup path will trim the oldest L4 fragments and avoid an infinite
		// reprocessing loop on the same batch.
		logging.Warn("Dreaming LLM unavailable, degrading to cleanup-only mode", "error", err)
		e.emitDreamThought("observation", fmt.Sprintf("梦境整合模型不可用（%v），转入仅清理模式。", err))
		respText = ""
	}

	// 4. Parse JSON
	respText = extractDreamingJSONCandidate(respText)

	var result ConsolidationResult
	var parseErr error
	if err := json.Unmarshal([]byte(respText), &result); err != nil {
		repairedText, repairErr := e.repairDreamingJSON(agentCtx, respText)
		if repairErr == nil {
			repairedText = extractDreamingJSONCandidate(repairedText)
			if repairedText != "" {
				if repairParseErr := json.Unmarshal([]byte(repairedText), &result); repairParseErr == nil {
					respText = repairedText
					logging.Info("Dreaming JSON repaired successfully after malformed response")
				} else {
					parseErr = fmt.Errorf("failed to parse dreaming result: %w", err)
					logging.Warn("Failed to parse repaired dreaming result", "text", repairedText, "error", repairParseErr)
				}
			} else {
				parseErr = fmt.Errorf("failed to parse dreaming result: %w", err)
				logging.Warn("Dreaming JSON repair returned empty content", "raw_text", respText)
			}
		} else {
			parseErr = fmt.Errorf("failed to parse dreaming result: %w", err)
			logging.Warn("Failed to parse dreaming result", "text", respText, "repair_error", repairErr, "error", err)
		}
		if parseErr != nil {
			e.emitDreamThought("observation", "梦境整合结果解析失败，回退到碎片清理策略。")
		}
	}

	// 5. Apply Changes
	fragmentIDSet := make(map[string]struct{}, len(fragments))
	for _, f := range fragments {
		fragmentIDSet[f.ID] = struct{}{}
	}

	// Track which fragments were processed (either absorbed or marked obsolete)
	processedIDs := make(map[string]struct{}, len(fragments))

	// Collect obsolete IDs
	for _, id := range result.ObsoleteIDs {
		if _, ok := fragmentIDSet[id]; ok {
			processedIDs[id] = struct{}{}
		}
	}

	successfulLongTermCount := 0
	outcome.NewMemoryCount = len(result.NewMemories)

	for _, m := range result.NewMemories {
		newNode, err := e.ikts.AddMemory(ctx, m.Layer, m.Category, m.Content, m.Summary, m.Keywords)
		if err != nil {
			logging.Error("Failed to add consolidated memory", "error", err)
			continue
		}
		if m.Layer == LayerDomain || m.Layer == LayerKnowledge {
			successfulLongTermCount++
			for _, id := range m.SourceIDs {
				if _, ok := fragmentIDSet[id]; ok {
					processedIDs[id] = struct{}{}
				}
			}
		}

		// GraphRAG Integration: Auto-link to related memories
		// Find semantically similar memories
		related, err := e.ikts.Search(ctx, m.Content, 3)
		if err == nil {
			for _, r := range related {
				// Avoid self-linking (though ID should be different)
				if r.ID != newNode.ID {
					// Create an edge
					logging.Info("GraphRAG: Linking new memory to related concept", "new_id", newNode.ID, "related_id", r.ID)
					if err := e.ikts.Link(ctx, newNode.ID, r.ID, "related_to", 0.8); err != nil {
						logging.Warn("Failed to link memories", "error", err)
					}
				}
			}
		}
	}
	outcome.LongTermCount = successfulLongTermCount

	// CRITICAL FIX: Delete ALL fragments that were part of this batch
	// Rationale: If we've processed them (tried consolidation), they should be removed
	// to prevent infinite loop of re-processing the same fragments.
	// Only exception: if no memories were created AND no obsolete IDs, keep for next cycle.
	deleteIDSet := make(map[string]struct{}, len(fragments))

	if successfulLongTermCount > 0 {
		// Successfully created long-term memories - delete all processed fragments
		// This includes both explicitly marked and absorbed ones
		for id := range fragmentIDSet {
			deleteIDSet[id] = struct{}{}
		}
		logging.Info("Dreaming: Deleting entire batch after successful consolidation",
			"batch_size", len(fragmentIDSet),
			"processed", len(processedIDs),
			"new_memories", successfulLongTermCount)
	} else if len(processedIDs) > 0 {
		// No long-term memories created, but some fragments were marked obsolete/absorbed
		// Delete only those explicitly processed
		for id := range processedIDs {
			deleteIDSet[id] = struct{}{}
		}
		logging.Info("Dreaming: Deleting only processed fragments (no L2/L3 created)",
			"processed", len(processedIDs))
	} else if parseErr != nil {
		// Parse failure means this batch cannot be trusted for a future retry.
		// Delete the entire batch to stop the same L4 fragments from looping forever.
		for id := range fragmentIDSet {
			deleteIDSet[id] = struct{}{}
		}
		logging.Warn("Dreaming: Parse failed, deleting entire fragment batch to prevent reprocessing loop",
			"batch_size", len(fragmentIDSet))
	} else {
		// No memories created, no fragments processed by model
		// Fallback: delete oldest 5 fragments to prevent accumulation
		// This handles cases where model returns empty JSON without parse failure details.
		deleteCount := 0
		maxDelete := 5
		if len(fragments) < maxDelete {
			maxDelete = len(fragments)
		}
		for _, f := range fragments[:maxDelete] {
			deleteIDSet[f.ID] = struct{}{}
			deleteCount++
		}
		logging.Warn("Dreaming: No consolidation result, deleting oldest fragments as fallback",
			"deleted", deleteCount)
	}

	logging.Info("Applying consolidated memories...", "new_count", len(result.NewMemories), "delete_count", len(deleteIDSet))
	e.emitDreamThought("observation", fmt.Sprintf("整合计划：新增 %d 条长期记忆，清理 %d 条碎片。", len(result.NewMemories), len(deleteIDSet)))

	deleteFailures := 0
	for id := range deleteIDSet {
		if err := e.ikts.DeleteNode(ctx, id); err != nil {
			deleteFailures++
			logging.Error("Failed to delete obsolete fragment", "id", id, "error", err)
		}
	}
	outcome.DeletedCount = len(deleteIDSet) - deleteFailures

	if parseErr != nil {
		if deleteFailures > 0 {
			return outcome, fmt.Errorf("%w; cleanup failed for %d/%d fragments", parseErr, deleteFailures, len(deleteIDSet))
		}
		logging.Warn("Dreaming consolidation fell back to cleanup after parse failure", "cleanup_count", len(deleteIDSet), "error", parseErr)
		return outcome, nil
	}

	if deleteFailures > 0 {
		return outcome, fmt.Errorf("failed to delete %d/%d obsolete fragments", deleteFailures, len(deleteIDSet))
	}

	return outcome, nil
}
