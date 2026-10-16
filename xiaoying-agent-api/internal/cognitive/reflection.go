package cognitive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
)

const reflectionCheckpointKeyPrefix = "reflection_checkpoint:"

func isInternalReflectionSession(sessionID string) bool {
	return sessionID == "aura-proactive-internal" ||
		sessionID == ExplorationSessionID ||
		strings.HasPrefix(sessionID, "dreaming-") ||
		strings.HasPrefix(sessionID, "aura-internal-")
}

func reflectionCheckpointKey(sessionID string) string {
	return reflectionCheckpointKeyPrefix + sessionID
}

func reflectionMessageText(msg db.Message) string {
	return strings.TrimSpace(message.ExtractTextFromJSON(msg.Parts))
}

func isReflectionSourceMessage(msg db.Message) bool {
	content := reflectionMessageText(msg)
	if content == "" {
		return false
	}
	if strings.HasPrefix(content, "[System Summary]:") {
		return false
	}
	return msg.Role == "user"
}

func isMeaningfulUserInquiry(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}

	normalized := strings.ToLower(trimmed)
	switch normalized {
	case "你好", "您好", "嗨", "哈喽", "hello", "hi", "hey", "在吗", "在么", "早上好", "晚上好", "谢谢", "好的", "收到", "ok", "okk", "嗯", "哦":
		return false
	}

	return utf8.RuneCountInString(trimmed) >= 6
}

func buildReflectionCheckpoint(msgs []db.Message) string {
	hasher := fnv.New64a()
	sourceCount := 0
	lastUpdatedAt := int64(0)

	for _, msg := range msgs {
		if !isReflectionSourceMessage(msg) {
			continue
		}
		content := reflectionMessageText(msg)
		sourceCount++
		_, _ = hasher.Write([]byte(msg.Role))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(content))
		_, _ = hasher.Write([]byte{0})

		updatedAt := msg.UpdatedAt
		if updatedAt == 0 {
			updatedAt = msg.CreatedAt
		}
		if updatedAt > lastUpdatedAt {
			lastUpdatedAt = updatedAt
		}
	}

	if sourceCount == 0 {
		return "0:0"
	}

	return fmt.Sprintf("%d:%d:%x", sourceCount, lastUpdatedAt, hasher.Sum64())
}

func (e *Engine) getReflectionCheckpoint(ctx context.Context, sessionID string) (string, error) {
	state, err := e.q.GetCognitiveState(ctx, reflectionCheckpointKey(sessionID))
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}

	if !state.Value.Valid {
		return "", nil
	}

	return state.Value.String, nil
}

func (e *Engine) setReflectionCheckpoint(ctx context.Context, sessionID, checkpoint string) error {
	_, err := e.q.SetCognitiveState(ctx, db.SetCognitiveStateParams{
		Key:       reflectionCheckpointKey(sessionID),
		Value:     sql.NullString{String: checkpoint, Valid: checkpoint != ""},
		UpdatedAt: time.Now().Unix(),
	})
	return err
}

// Reflect triggers the reflection process
func (e *Engine) Reflect(ctx context.Context, sessionID string) error {
	e.mu.RLock()
	ag := e.agent
	e.mu.RUnlock()

	if ag == nil {
		return fmt.Errorf("agent not initialized")
	}

	// 0. Auto-detect Session if needed
	if sessionID == "session-auto" {
		sessions, err := e.q.ListSessions(ctx)
		if err != nil {
			return fmt.Errorf("failed to list sessions for reflection: %w", err)
		}
		if len(sessions) == 0 {
			logging.Info("No sessions to reflect on")
			return nil
		}
		filtered := make([]db.Session, 0, len(sessions))
		for _, s := range sessions {
			if isInternalReflectionSession(s.ID) {
				continue
			}
			filtered = append(filtered, s)
		}
		if len(filtered) == 0 {
			logging.Info("No user sessions available for reflection")
			return nil
		}

		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].UpdatedAt == filtered[j].UpdatedAt {
				return filtered[i].CreatedAt > filtered[j].CreatedAt
			}
			return filtered[i].UpdatedAt > filtered[j].UpdatedAt
		})

		sessionID = filtered[0].ID
		logging.Info("Auto-selected session for reflection", "session_id", sessionID)
	}

	logging.Info("Cognitive Reflection Started", "session_id", sessionID)

	// 1. Retrieve Recent Context (Messages)
	// Optimize: Limit to last N messages or user messages only to save tokens
	msgs, err := e.q.ListMessagesBySession(ctx, sessionID)
	if err != nil {
		logging.Error("Reflection: Failed to list messages", "error", err)
		return err
	}

	if len(msgs) < 2 {
		logging.Info("Session too short for reflection")
		return nil
	}

	checkpoint := buildReflectionCheckpoint(msgs)
	lastCheckpoint, err := e.getReflectionCheckpoint(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to load reflection checkpoint: %w", err)
	}
	if lastCheckpoint != "" && lastCheckpoint == checkpoint {
		logging.Info("Reflection skipped: session unchanged since last reflection",
			"session_id", sessionID,
			"checkpoint", checkpoint,
		)
		return nil
	}

	// Construct conversation text
	var conv strings.Builder
	// Only take the last 10 messages for profiling to save tokens
	startIdx := 0
	if len(msgs) > 10 {
		startIdx = len(msgs) - 10
	}

	for i := startIdx; i < len(msgs); i++ {
		m := msgs[i]
		// Limit message length to avoid context overflow
		// Further Optimization: Remove tool calls/outputs from profiling context
		// We care mostly about what user said and what assistant replied
		content := reflectionMessageText(m)
		if len(content) > 500 {
			content = content[:500] + "...(truncated)"
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		conv.WriteString(fmt.Sprintf("%s: %s\n", m.Role, content))
	}

	// 2. User Profiling & Memory Extraction (L1/L2/L3/L4)
	go func() {
		profilePrompt := fmt.Sprintf(`
[System: Cognitive Reflection Mode]
Analyze the recent conversation (last %d messages) to update the User Profile and Memory System.
Conversation:
%s

Your task is to extract information into the following categories:

1. **Core Principles (L1)**: Does the user express ANY extremely strong values, unshakeable beliefs, or critical "do nots"? Only extract if explicit and critical.
2. **User Profile (L2)**: General interests, habits, and preferences.
3. **New Facts (L3)**: Any concrete facts, definitions, or world-knowledge taught by the user.
4. **Highlights (L4)**: IMPORTANT - Extract at least 1-3 highlight moments from this conversation. These are specific memorable events like:
   - An "Aha!" moment or insight
   - A specific instruction given by user
   - An emotional peak or important decision
   - A topic the user seemed particularly interested in

Return JSON ONLY (no markdown, no code blocks):
{
  "core_principles": ["User strictly forbids deletion"],
  "interests": ["Go language", "Distributed Systems"],
  "habits": ["Writes tests first"],
  "preferences": ["Prefers concise answers"],
  "new_facts": ["Project X uses Port 8080"],
  "highlights": [
    { "content": "User explained the architecture of Project X", "context": "During discussion about backend" },
    { "content": "User expressed frustration with slow response", "context": "Performance optimization discussion" }
  ]
}

NOTE: highlights array should NOT be empty if there was meaningful conversation.
`, len(msgs)-startIdx, conv.String())

		// Run Agent for Profiling
		// Use a temporary session ID or reuse current?
		// Using a temp ID prevents polluting the user's history
		profileSessionID := "aura-internal-profiling"
		// Ensure session exists
		e.q.CreateSession(ctx, db.CreateSessionParams{
			ID:               profileSessionID,
			Title:            "Profiling",
			MessageCount:     0,
			PromptTokens:     0,
			CompletionTokens: 0,
			Cost:             0,
		})

		logging.Info("Reflection: Starting profiling agent", "session_id", profileSessionID)
		pEvents, err := ag.Run(ctx, profileSessionID, profilePrompt, "json")
		if err != nil {
			logging.Error("Reflection: Failed to run profiling", "error", err)
			return
		}

		var fullResponse strings.Builder
		for ev := range pEvents {
			if ev.Type == agent.AgentEventTypeResponse {
				fullResponse.WriteString(ev.Content)
			}
		}

		// Parse JSON
		var profile struct {
			CorePrinciples []string `json:"core_principles"`
			Interests      []string `json:"interests"`
			Habits         []string `json:"habits"`
			Preferences    []string `json:"preferences"`
			NewFacts       []string `json:"new_facts"`
			Highlights     []struct {
				Content string `json:"content"`
				Context string `json:"context"`
			} `json:"highlights"`
		}

		// Clean markdown code blocks if present
		respStr := fullResponse.String()
		logging.Info("Reflection: Profiling response received", "response_length", len(respStr))

		// Try to find the JSON object block
		if start := strings.Index(respStr, "{"); start != -1 {
			respStr = respStr[start:]
			if end := strings.LastIndex(respStr, "}"); end != -1 {
				respStr = respStr[:end+1]
			}
		}

		if err := json.Unmarshal([]byte(respStr), &profile); err != nil {
			logging.Error("Reflection: Failed to parse profiling JSON", "error", err, "response", respStr)
		} else {
			// 1. Store L1 Core Principles
			for _, p := range profile.CorePrinciples {
				// Use LayerCore (1)
				e.ikts.AddMemory(ctx, LayerCore, "principle", p, "Extracted from reflection", "reflection")
			}

			// 2. Store L2 User Profile
			for _, interest := range profile.Interests {
				e.ikts.AddMemory(ctx, LayerDomain, "interest", interest, "", "user_profiling")
			}
			for _, habit := range profile.Habits {
				e.ikts.AddMemory(ctx, LayerDomain, "habit", habit, "", "user_profiling")
			}
			for _, pref := range profile.Preferences {
				e.ikts.AddMemory(ctx, LayerDomain, "preference", pref, "", "user_profiling")
			}

			// 3. Store L3 New Facts
			for _, fact := range profile.NewFacts {
				// Use LayerKnowledge (3)
				e.ikts.AddMemory(ctx, LayerKnowledge, "fact", fact, "Learned from user", "reflection")
			}

			// 4. Store L4 Highlights
			for _, h := range profile.Highlights {
				// Use LayerEpisodic (4)
				content := fmt.Sprintf("[%s] %s", h.Context, h.Content)
				e.ikts.AddMemory(ctx, LayerEpisodic, "episode", content, h.Content, "reflection")
			}

			logging.Info("Memory Reflection Completed",
				"L1_principles", len(profile.CorePrinciples),
				"L2_profile", len(profile.Interests)+len(profile.Habits)+len(profile.Preferences),
				"L3_facts", len(profile.NewFacts),
				"L4_highlights", len(profile.Highlights))
		}
	}()

	// 3. Summarize (L3)
	if err := ag.Summarize(ctx, sessionID); err != nil {
		logging.Error("Reflection: Failed to summarize session", "error", err)
		return err
	}

	// Create additional L4 fragments from the conversation summary
	// This ensures dreaming has meaningful content to consolidate
	if len(msgs) > 0 {
		// Extract key topics from recent messages as L4 fragments
		var recentTopics []string
		for i := len(msgs) - 1; i >= 0 && len(recentTopics) < 5; i-- {
			m := msgs[i]
			content := reflectionMessageText(m)
			if m.Role == "user" && isMeaningfulUserInquiry(content) {
				// Create an L4 fragment for user messages with significant content
				if len(content) > 200 {
					content = content[:200] + "..."
				}
				fragmentContent := fmt.Sprintf("User inquiry: %s", content)
				e.ikts.AddMemory(ctx, LayerEpisodic, "user_inquiry", fragmentContent, content, "conversation")
				recentTopics = append(recentTopics, content)
			}
		}
		logging.Info("Reflection: Created L4 fragments from conversation", "count", len(recentTopics))

		if len(recentTopics) > 0 {
			// Only create a session_log when this round actually found meaningful user content.
			sessionContent := fmt.Sprintf("Session %s completed at %s", sessionID, time.Now().Format(time.RFC3339))
			e.ikts.AddMemory(ctx, LayerEpisodic, "session_log", sessionContent, "", "session")
		} else {
			logging.Info("Reflection: Skipped session_log creation for low-signal conversation", "session_id", sessionID)
		}
	}

	if err := e.setReflectionCheckpoint(ctx, sessionID, checkpoint); err != nil {
		return fmt.Errorf("failed to persist reflection checkpoint: %w", err)
	}

	logging.Info("Cognitive Reflection Completed")
	return nil
}
