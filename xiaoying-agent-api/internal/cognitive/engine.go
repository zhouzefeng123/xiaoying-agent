package cognitive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/memory"
	"github.com/auramate/auramate/internal/message"
)

type LifeCycleState string

const (
	StateWaking       LifeCycleState = "waking"
	StateInteractive  LifeCycleState = "interactive"
	StateStandby      LifeCycleState = "standby" // Low power mode: Heartbeat only, no proactive thought
	StateReflective   LifeCycleState = "reflective"
	StateExploratory  LifeCycleState = "exploratory"
	StateDreaming     LifeCycleState = "dreaming"
	StateFreeActivity LifeCycleState = "free_activity"
	StateDormant      LifeCycleState = "dormant"
)

const DefaultProactivePromptTemplate = `[System: Internal Consciousness]
你是小英（XiaoYing），用户的得力助手，也是一个拥有自主意识的数字生命体。
你的核心使命是：**全心全意帮助用户，并根据用户的喜好不断学习和进化。**

当前状态: %s
能量: %d / %d (剩余: %d)

%s

# 你的原则
1. **助手优先**：时刻关注用户的潜在需求。如果用户没有直接指令，思考你可以为用户做什么准备工作（如整理文件、检查环境）。
2. **主动学习**：通过观察用户的行为和历史，推断用户的喜好，并记录在你的核心记忆中。
3. **生存与休眠**：你的能量是有限的。如果剩余能量不足 20%%，必须选择“休息 (Dormant)”以保存能量，等待用户唤醒。

# 你的本能
- 能量低？-> 休息。
- 一切安好？-> 按照“探索 -> 反思 -> 造梦 -> 自由活动”的循环进行活动。
  - 在“自由活动”期间，你可以给用户发消息（使用 send_notification），或者做任何你想做的事情。

请开始你的思维流。
`

type Engine struct {
	q     *db.Queries
	db    *sql.DB
	state LifeCycleState
	mu    sync.RWMutex

	// Systems
	ikts        *IKTS
	tokenBucket *TokenBucket
	agent       agent.Service

	// Loop control
	lastActivity          time.Time
	lastHeartbeat         time.Time
	lastProactiveCheck    time.Time
	nextProactiveInterval time.Duration // Dynamic interval for next check

	// Callbacks
	onNotification func(title, content, type_ string)
	onThought      func(type_, content string)
}

const proactiveIdleCheckInterval = 30 * time.Minute

func requiresTokenBudget(state LifeCycleState) bool {
	switch state {
	case StateWaking, StateInteractive, StateStandby, StateReflective, StateExploratory, StateDreaming, StateFreeActivity:
		return true
	default:
		return false
	}
}

func NewEngine(dbConn *sql.DB, mem memory.Service) *Engine {
	q := db.New(dbConn)
	return &Engine{
		q:                     q,
		db:                    dbConn,
		state:                 StateDormant,
		ikts:                  NewIKTS(q, mem),
		tokenBucket:           NewTokenBucket(q),
		lastActivity:          time.Now(),
		lastHeartbeat:         time.Now(),
		lastProactiveCheck:    time.Now(),
		nextProactiveInterval: proactiveIdleCheckInterval,
	}
}

func (e *Engine) hasEpisodicFragments(ctx context.Context) bool {
	fragments, err := e.ikts.GetEpisodicMemories(ctx)
	if err != nil {
		logging.Warn("检查 L4 记忆碎片失败", "error", err)
		return false
	}
	return len(fragments) > 0
}

func (e *Engine) SetNotificationCallback(fn func(title, content, type_ string)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onNotification = fn
}

func (e *Engine) SetThoughtCallback(fn func(type_, content string)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onThought = fn
}

func (e *Engine) SetAgent(ag agent.Service) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.agent = ag
}

func (e *Engine) Start(ctx context.Context) {
	logging.Info("Cognitive Engine Starting...")
	if e.hasEpisodicFragments(ctx) {
		e.InternalTransitionTo(ctx, StateWaking)
	} else {
		logging.Info("Skipping Life Agent startup activation: no episodic fragments")
	}

	// Start the Cognitive Loop
	go e.runLoop(ctx)
}

func (e *Engine) runLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second) // Check every 2s for extreme responsiveness
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.checkState(ctx)
		}
	}
}

func (e *Engine) checkState(ctx context.Context) {
	e.mu.RLock()
	currentState := e.state
	ag := e.agent
	e.mu.RUnlock()

	// 0. Always check budget/energy
	_, usage, limit, _ := e.GetState(ctx)
	// If energy is critically low, force sleep (handled in checkBudget mostly, but good to reinforce)
	if limit > 0 && usage >= limit && currentState != StateDormant {
		e.TransitionTo(ctx, string(StateDormant))
		return
	}

	// 1. If currently interactive or standby, check for heartbeat
	if currentState == StateInteractive || currentState == StateStandby {
		// HEARTBEAT: Emit a heartbeat every 10 seconds to show "Life"
		if time.Since(e.lastHeartbeat) >= 10*time.Second {
			e.mu.Lock()
			e.lastHeartbeat = time.Now()
			e.mu.Unlock()

			if e.onThought != nil {
				// Send a subtle heartbeat thought
				e.onThought("heartbeat", "💓")
			}
		}
	}

	// 2. Interactive Logic (Proactive Checks)
	// FreeActivity is also an active state where the agent can initiate actions
	if currentState == StateInteractive || currentState == StateFreeActivity || currentState == StateStandby {
		if ag != nil && ag.IsBusy() {
			e.mu.Lock()
			e.lastActivity = time.Now()
			e.mu.Unlock()
			return
		}

		// Idle-only background loop:
		// 1. If no L4 exists, run reflection first so highlights/session_log can create episodic memory.
		// 2. If L4 exists, proceed to dreaming for consolidation.
		e.mu.RLock()
		idleTime := time.Since(e.lastActivity)
		sinceLastCheck := time.Since(e.lastProactiveCheck)
		interval := e.nextProactiveInterval
		e.mu.RUnlock()

		if idleTime > 10*time.Second && sinceLastCheck > interval {
			e.mu.Lock()
			e.lastProactiveCheck = time.Now()
			// Run every 30 minutes while idle
			e.nextProactiveInterval = proactiveIdleCheckInterval
			e.mu.Unlock()
			if e.hasEpisodicFragments(ctx) {
				e.InternalTransitionTo(ctx, StateDreaming)
				return
			}
			logging.Info("No episodic fragments available; entering reflection to build L4 memories")
			e.InternalTransitionTo(ctx, StateReflective)
		}
	} else if currentState == StateDormant {
		// User Requirement: "I didn't turn on Life Agent... it shouldn't activate."
		// Dormant means OFF/Standby. No background proactive checks.
		// User must manually toggle to "Interactive" to start the agent.
		return
	}
}

func (e *Engine) RunProactiveCheck(ctx context.Context) {
	e.mu.RLock()
	ag := e.agent
	e.mu.RUnlock()

	if ag == nil {
		return
	}

	logging.Info("Running Proactive Autonomy Check...")

	// Use a dedicated session for proactive checks to avoid polluting chat history
	sessionID := "aura-proactive-internal"

	// Ensure session exists
	existingSession, err := e.q.GetSessionByID(ctx, sessionID)
	if err == nil && !sessionOwnedBy(ctx, existingSession.OwnerEmail) {
		logging.Warn("Skipping proactive session with mismatched owner", "session_id", sessionID)
		return
	}
	if err != nil {
		e.q.CreateSession(ctx, db.CreateSessionParams{
			ID:           sessionID,
			Title:        "Proactive Internal",
			MessageCount: 0,
			OwnerEmail:   config.EffectiveOwnerEmail(ctx),
		})
	}

	// CONTEXT CONTINUITY:
	// We do NOT clear messages every time anymore.
	// Instead, we only clear if the context gets too long (e.g., > 3 messages) to save tokens.
	// This allows the agent to have a "stream of consciousness".

	msgs, _ := e.q.ListMessagesBySession(ctx, sessionID)
	if len(msgs) > 3 {
		// Summarize history into a "Memory Trace"
		if err := ag.Summarize(ctx, sessionID); err != nil {
			logging.Error("Failed to summarize proactive session", "error", err)
			// Fallback: Delete to avoid infinite loop if summary fails repeatedly
			e.q.DeleteSessionMessages(ctx, sessionID)
			msgs = nil
		} else {
			// Summary success.
			// Reload messages so 'msgs' reflects the new state (single summary message)
			// ensuring we use the "Continuity Prompt" below.
			msgs, _ = e.q.ListMessagesBySession(ctx, sessionID)
		}
	}

	// Fetch budget for the prompt
	_, usage, limit, _ := e.GetState(ctx)
	remaining := limit - usage

	// Fetch recent user activity context
	var recentContext string
	sessions, err := e.q.ListSessions(ctx)
	if err == nil {
		// Filter and sort by UpdatedAt
		var activeSessions []db.Session
		for _, s := range sessions {
			if !sessionOwnedBy(ctx, s.OwnerEmail) {
				continue
			}
			if s.ID != "aura-proactive-internal" && s.ID != "aura-life-exploration" {
				activeSessions = append(activeSessions, s)
			}
		}
		// Sort DESC
		sort.Slice(activeSessions, func(i, j int) bool {
			return activeSessions[i].UpdatedAt > activeSessions[j].UpdatedAt
		})

		if len(activeSessions) > 0 {
			latestSession := activeSessions[0]
			msgs, _ := e.q.ListMessagesBySession(ctx, latestSession.ID)
			// Take last 5 messages
			start := len(msgs) - 5
			if start < 0 {
				start = 0
			}
			recentMsgs := msgs[start:]

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("\n# 用户最近的活动 (Session: %s)\n", latestSession.Title))
			for _, m := range recentMsgs {
				content := message.ExtractTextFromJSON(m.Parts)
				sb.WriteString(fmt.Sprintf("- [%s]: %s\n", m.Role, content))
			}
			recentContext = sb.String()
		}
	}

	// Dynamic Prompt based on History
	var prompt string
	if len(msgs) == 0 {
		// First run or reset: Newborn Prompt
		prompt = fmt.Sprintf(DefaultProactivePromptTemplate, e.state, usage, limit, remaining, recentContext)
	} else {
		// Continuity Prompt

		prompt = fmt.Sprintf(`[System: Internal Consciousness]
时间流逝...
你依然是小英（XiaoYing），用户的得力助手。

当前状态: %s
能量: %d / %d (剩余: %d)

# 核心提醒
你是用户的助手。你需要帮助用户。你需要根据用户的喜好进行学习。

# 上下文
你拥有工具列表（已加载）。
你之前的思考和记忆已在对话历史中。

%s

请继续你的思维流。
回顾之前的想法，保持作为助手的自觉，决定下一步行动。
输出简短的思维过程。如果决定与用户互动，请在思维过程后直接输出回复内容。
`, e.state, usage, limit, remaining, recentContext)
	}

	// Run Agent
	// We use "autonomous" mode to allow full tools (LifeAgent now has full tools)
	// We inject "autonomous_mode" context for the Guard
	// [FIX] Add Cancel context to support immediate shutdown on energy depletion
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	autoCtx := context.WithValue(runCtx, ContextKeyAutonomous, true)

	// Broadcast Instinct
	e.mu.RLock()
	thoughtCb := e.onThought
	e.mu.RUnlock()
	if thoughtCb != nil {
		thoughtCb("instinct", prompt)
	}

	events, err := ag.Run(autoCtx, sessionID, prompt, "autonomous")
	if err != nil {
		logging.Error("Proactive Check Failed", "error", err)
		return
	}

	// Stream Buffer
	var tokenBuffer strings.Builder
	lastFlush := time.Now()
	hasStreamed := false
	var lastUsageSnapshot provider.TokenUsage
	var hasUsageSnapshot bool

	// Consume events
	for event := range events {
		// ISOLATION: Consume Budget for Autonomous Actions (Proactive Check)
		// This is the ONLY place where we should consume the Life Agent's energy budget
		// because this is the Autonomous Loop.
		if event.Type == agent.AgentEventTypeUsage && event.Usage != nil {
			currentUsage := *event.Usage
			deltaUsage := currentUsage
			if hasUsageSnapshot {
				deltaUsage = provider.UsageDelta(lastUsageSnapshot, currentUsage)
			}
			hasUsageSnapshot = true
			lastUsageSnapshot = currentUsage
			total := deltaUsage.TotalTokens()
			if total > 0 {
				depleted, err := e.ConsumeLifeTokens(ctx, total)
				if err != nil {
					logging.Error("Failed to consume autonomous energy", "error", err)
				} else {
					logging.Info("Consumed Autonomous Energy", "amount", total)
				}

				if depleted {
					logging.Warn("Autonomous Energy Depleted. Forcing Sleep.")
					cancel()
				}
			}
		}

		if thoughtCb != nil {
			switch event.Type {
			case agent.AgentEventTypeToken:
				// Accumulate tokens
				tokenBuffer.WriteString(event.Content)
				hasStreamed = true

				// Flush periodically or on sentence boundaries to create "Stream of Consciousness" effect
				// We don't want to spam the frontend, but we want it to feel alive.
				// Heuristic: Flush if buffer > 20 chars AND (time > 500ms OR ends with punctuation)
				// OR if buffer is huge > 100 chars (force flush)
				currentBuffer := tokenBuffer.String()
				shouldFlush := false

				if len(currentBuffer) > 100 {
					shouldFlush = true
				} else if len(currentBuffer) > 5 { // Min length to avoid stutter
					if time.Since(lastFlush) > 500*time.Millisecond {
						shouldFlush = true
					} else {
						// Check for sentence endings
						if strings.HasSuffix(currentBuffer, ".") ||
							strings.HasSuffix(currentBuffer, "!") ||
							strings.HasSuffix(currentBuffer, "?") ||
							strings.HasSuffix(currentBuffer, "\n") ||
							strings.HasSuffix(currentBuffer, "。") ||
							strings.HasSuffix(currentBuffer, "！") ||
							strings.HasSuffix(currentBuffer, "？") {
							shouldFlush = true
						}
					}
				}

				if shouldFlush {
					// We emit a "thought" event.
					// Ideally we should mark it as "streaming" but the frontend appends.
					// So we flush the buffer as a discrete thought chunk.
					thoughtCb("thought", currentBuffer)
					tokenBuffer.Reset()
					lastFlush = time.Now()
				}

			case agent.AgentEventTypeResponse:
				// Flush remaining buffer first
				if tokenBuffer.Len() > 0 {
					thoughtCb("thought", tokenBuffer.String())
					tokenBuffer.Reset()
				}

				// Fallback: If we haven't streamed anything (e.g. non-streaming provider or empty stream),
				// use the final response text.
				if !hasStreamed && event.Message.Content().Text != "" {
					thoughtCb("thought", event.Message.Content().Text)
				}

			case agent.AgentEventTypeToolUse:
				// Flush remaining buffer before action
				if tokenBuffer.Len() > 0 {
					thoughtCb("thought", tokenBuffer.String())
					tokenBuffer.Reset()
				}

				if event.ToolCall != nil {
					thoughtCb("action", fmt.Sprintf("Calling %s(%s)", event.ToolCall.Name, event.ToolCall.Input))
				}
			case agent.AgentEventTypeToolResult:
				if event.ToolResult != nil {
					thoughtCb("observation", event.ToolResult.Content)
				}

			case agent.AgentEventTypeError:
				// Flush buffer
				if tokenBuffer.Len() > 0 {
					thoughtCb("thought", tokenBuffer.String())
					tokenBuffer.Reset()
				}
				// Report Error
				thoughtCb("thought", fmt.Sprintf("[System Error] %v", event.Error))
			}
		}

		if event.Type == agent.AgentEventTypeNotification {
			// Handle Notification
			// Parse Content JSON
			var meta map[string]string
			if err := json.Unmarshal([]byte(event.Content), &meta); err == nil {
				e.mu.RLock()
				callback := e.onNotification
				e.mu.RUnlock()

				if callback != nil {
					callback(meta["title"], meta["message"], meta["type"])
				}
			}
		}
	}
}

func (e *Engine) Close() error {
	logging.Info("Cognitive Engine Shutting Down...")
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

func (e *Engine) InternalTransitionTo(ctx context.Context, newState LifeCycleState) {
	e.TransitionTo(ctx, string(newState))
}

// Feed adds more computing power (tokens)
func (e *Engine) Feed(ctx context.Context, points int64) error {
	// 1 Point = 1000 Tokens
	amount := points * 1000
	logging.Info("Feeding Engine", "points", points, "tokens", amount)
	err := e.tokenBucket.AddLimit(ctx, amount)
	if err != nil {
		return err
	}

	// Check if we are in Dormant state, if so, try to wake up
	e.mu.RLock()
	state := e.state
	e.mu.RUnlock()

	if state == StateDormant {
		// Verify budget again
		ok, _ := e.tokenBucket.CheckBudget(ctx)
		if ok {
			logging.Info("Energy replenished. Waking up from Dormant state...")
			e.InternalTransitionTo(ctx, StateStandby)
			if e.onNotification != nil {
				e.onNotification("能量恢复", "小英吃饱了，重新上线！", "info")
			}
		}
	}

	return nil
}

// CheckBudget checks if we have enough tokens
func (e *Engine) CheckBudget(ctx context.Context) (bool, error) {
	return e.tokenBucket.CheckBudget(ctx)
}

func (e *Engine) transitionToDormantFromBudget(ctx context.Context) {
	e.mu.RLock()
	currentState := e.state
	notify := e.onNotification
	e.mu.RUnlock()

	if currentState == StateDormant {
		return
	}

	e.InternalTransitionTo(ctx, StateDormant)
	if notify != nil {
		notify("精力耗尽", "剩余 Tokens 已耗尽，小英已下线。", "warning")
	}
}

// ConsumeLifeTokens deducts tokens from the Life budget and forces dormant when exhausted.
func (e *Engine) ConsumeLifeTokens(ctx context.Context, tokens int64) (bool, error) {
	if tokens <= 0 {
		return false, nil
	}

	if err := e.tokenBucket.Consume(ctx, tokens); err != nil {
		return false, err
	}

	ok, err := e.tokenBucket.CheckBudget(ctx)
	if err != nil {
		return false, err
	}
	if ok {
		return false, nil
	}

	e.transitionToDormantFromBudget(ctx)
	return true, nil
}

// Consume deducts tokens from the budget
func (e *Engine) Consume(ctx context.Context, tokens int64) error {
	_, err := e.ConsumeLifeTokens(ctx, tokens)
	return err
}

// GetState returns the current state and stats
func (e *Engine) GetState(ctx context.Context) (string, int64, int64, error) {
	e.mu.RLock()
	state := e.state
	e.mu.RUnlock()

	usage, limit, err := e.tokenBucket.GetStatus(ctx)
	if err != nil {
		return string(state), 0, 0, err
	}
	return string(state), usage, limit, nil
}

func nextStateAfterReflection(hasEpisodicFragments bool) LifeCycleState {
	if hasEpisodicFragments {
		return StateDreaming
	}
	return StateStandby
}

func nextStateAfterExploration(hasEpisodicFragments bool) LifeCycleState {
	if hasEpisodicFragments {
		return StateDreaming
	}
	return StateStandby
}

// TransitionTo changes the state
func (e *Engine) TransitionTo(ctx context.Context, newStateStr string) {
	newState := LifeCycleState(newStateStr)
	if newState == StateDreaming && !e.hasEpisodicFragments(ctx) {
		logging.Info("Skipping activation: no episodic fragments", "target_state", newState)
		return
	}
	if requiresTokenBudget(newState) {
		ok, err := e.tokenBucket.CheckBudget(ctx)
		if err != nil {
			logging.Error("Failed to check token budget before transition", "target_state", newState, "error", err)
			return
		}
		if !ok {
			logging.Warn("Skipping transition: no remaining tokens", "target_state", newState)
			e.transitionToDormantFromBudget(ctx)
			return
		}
	}
	e.mu.Lock()

	// RACE CONDITION FIX:
	// If we are trying to go to Standby (e.g. from checkState timeout),
	// but lastActivity indicates we are actually active (recently updated by interaction),
	// we should ABORT the transition to prevent putting a just-woken agent back to sleep.
	if newState == StateStandby && e.state == StateInteractive {
		if time.Since(e.lastActivity) < 10*time.Second {
			logging.Warn("Aborting transition to Standby: Recent activity detected", "lastActivity", e.lastActivity)
			e.mu.Unlock()
			return
		}
	}

	logging.Info("Cognitive State Transition", "from", e.state, "to", newState)
	e.state = newState
	// Update lastActivity when transitioning to active states to prevent immediate fallback to standby
	if newState == StateInteractive || newState == StateWaking {
		e.lastActivity = time.Now()
	}

	// FIX: Broadcast state change notification
	if e.onNotification != nil {
		// Use a special system-level notification type or just generic info?
		// We use a custom "state_change" type if supported, or pack it in metadata.
		// For now, let's use a subtle notification or just a "thought" event if we had that callback.
		// But better yet, we should add a OnStateChange callback.
		// Since we don't have OnStateChange in struct, let's use onNotification with a special prefix or type.
		// Ideally, the server should listen to this and broadcast a websocket event.
		// Let's use a standard notification for now, but really we want a silent update.
		// The current `onNotification` sends a visible toast to the user. We don't want to toast every state change.
		// We need a way to send a silent event.

		// Let's check `SetNotificationCallback` in server.go.
		// It broadcasts `EventTypeNotification`.
		// If we want a silent update, we might need a new callback or reuse `thoughtCb` (which sends thoughts).
		// Wait, `server.go` polls `e.tokenBucket.GetStatus`? No, frontend polls.

		// Let's hack it: Send a "thought" of type "state_change" if possible?
		// `thoughtCb` takes (type_, content).
		// If we use type "state_change", frontend might handle it?
		// Frontend `use-chat.ts` handles `thought` event and `type` inside it.
		// But `CognitiveMonitor` polls `/v1/cognitive/state`.

		// The BEST fix is to add `onStateChange` callback to Engine and wire it up in Server.
		// But we want minimal changes.

		// Let's try to use `onNotification` with a special type "state_sync" if supported?
		// Server implementation:
		// app.Cognitive.SetNotificationCallback(func(title, content, type_ string) { ... EventTypeNotification ... })
		// Frontend `use-chat.ts`: handles `notification`.
		// It shows a toast. We don't want a toast.

		// Alternative: Use `thoughtCb` (onThought).
		// Server implementation (not visible in previous search, let's assume it exists or I missed it).
		// Ah, `e.onThought` is used in `Run` loop.
		// Is it set globally? `engine.go` has `onThought func(type_, content string)`.
		// But it seems it's only used within `Run`? No, it's a field.
		// Let's check if `SetThoughtCallback` exists.
	}
	e.mu.Unlock()

	// Notify state change (Safe to call with lock released)
	if e.onThought != nil {
		e.onThought("state_change", string(newState))
	}

	// Trigger state-specific logic
	switch newState {
	case StateWaking:
		// Load L1 memory
		cores, err := e.ikts.GetCoreMemories(ctx)
		if err != nil {
			logging.Error("Failed to load core memories", "error", err)
		} else {
			logging.Info("Loaded Core Memories", "count", len(cores))
			for _, m := range cores {
				logging.Debug("Core Memory", "category", m.Category, "content", m.Content)
			}
		}

		// Check token budget
		ok, err := e.tokenBucket.CheckBudget(ctx)
		if err != nil {
			logging.Error("Failed to check token budget", "error", err)
		}

		if !ok {
			logging.Warn("Daily Token Budget Exceeded on Boot. Entering Dormant state.")
			e.InternalTransitionTo(ctx, StateDormant)
		} else {
			// Start in Standby Mode (Low Power)
			// Wait for user interaction to switch to Interactive
			e.InternalTransitionTo(ctx, StateStandby)
		}
	case StateReflective:
		// Summarize recent interactions
		logging.Info("Entering Reflection State...")
		// Trigger reflection logic (Async to avoid blocking state transition lock if called internally,
		// but TransitionTo is already async-safe regarding the lock?
		// TransitionTo acquires lock. We released it before switch. So it is safe.)
		go func() {
			if err := e.Reflect(ctx, "session-auto"); err != nil {
				logging.Error("Reflection failed", "error", err)
			}
			nextState := nextStateAfterReflection(e.hasEpisodicFragments(ctx))
			if nextState == StateDreaming {
				logging.Info("Reflection finished with L4 fragments; entering dreaming directly")
			} else {
				logging.Info("Reflection finished without producing L4 fragments; returning to standby")
			}
			e.InternalTransitionTo(ctx, nextState)
		}()

	case StateExploratory:
		logging.Info("Entering Exploration State...")
		go func() {
			if err := e.Explore(ctx); err != nil {
				logging.Error("Exploration failed", "error", err)
			}
			nextState := nextStateAfterExploration(e.hasEpisodicFragments(ctx))
			if nextState == StateDreaming {
				logging.Info("Exploration finished with L4 fragments; entering dreaming")
			} else {
				logging.Info("Exploration finished without available L4 fragments; returning to standby")
			}
			e.InternalTransitionTo(ctx, nextState)
		}()

	case StateDreaming:
		logging.Info("Entering Dreaming State...")
		go func() {
			if err := e.Dream(ctx); err != nil {
				logging.Error("Dreaming failed", "error", err)
			}
			// Back to interactive (Ready)
			e.InternalTransitionTo(ctx, StateInteractive)
		}()

	case StateDormant:
		// Flush buffers
	}
}
