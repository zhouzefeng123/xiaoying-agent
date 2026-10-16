package cognitive

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"time"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/logging"
)

const (
	ExplorationSessionID = "aura-life-exploration"
)

// Explore triggers an autonomous exploration cycle
func (e *Engine) Explore(ctx context.Context) error {
	e.mu.RLock()
	ag := e.agent
	e.mu.RUnlock()

	if ag == nil {
		return fmt.Errorf("agent not initialized")
	}

	// 0. Ensure Session Exists
	_, err := e.q.GetSessionByID(ctx, ExplorationSessionID)
	if err != nil {
		if err == sql.ErrNoRows {
			logging.Info("Creating new Exploration Session...", "id", ExplorationSessionID)
			_, err = e.q.CreateSession(ctx, db.CreateSessionParams{
				ID:               ExplorationSessionID,
				Title:            "Aura Life Exploration",
				MessageCount:     0,
				PromptTokens:     0,
				CompletionTokens: 0,
				Cost:             0,
			})
			if err != nil {
				return fmt.Errorf("failed to create exploration session: %w", err)
			}
		} else {
			return fmt.Errorf("failed to check exploration session: %w", err)
		}
	}

	// 1. Check Budget (Double check)
	ok, err := e.tokenBucket.CheckBudget(ctx)
	if err != nil || !ok {
		logging.Warn("Exploration skipped due to budget constraints")
		e.InternalTransitionTo(ctx, StateDormant)
		return nil
	}

	logging.Info("Starting Autonomous Exploration...")

	// 2. Determine Topic
	var topic string

	// Try to get interests from IKTS L2 (Domain/User Profile)
	domainMemories, err := e.ikts.GetDomainMemories(ctx)
	var interests []string
	if err == nil && len(domainMemories) > 0 {
		for _, m := range domainMemories {
			// Filter for interests or general preferences
			// Assuming 'preference' or 'interest' category, or just use content if it's short
			if len(m.Content) < 100 {
				interests = append(interests, m.Content)
			}
		}
	}

	if len(interests) > 0 {
		// Pick a random interest
		topic = interests[rand.Intn(len(interests))]
		// Add some variety
		topic = fmt.Sprintf("latest trends and news about %s", topic)
	} else {
		// Default Fallback Topics
		defaults := []string{
			"latest developments in AI agents and LLM memory systems",
			"Golang performance optimization techniques 2024",
			"React Server Components best practices",
			"SQLite advanced features and optimization",
			"Tauri v2 vs Electron performance comparison",
			"Rust for backend development trends",
		}
		topic = defaults[rand.Intn(len(defaults))]
	}

	logging.Info("Exploration Topic Selected", "topic", topic)

	// 3. Construct Prompt
	// Optimization: Keep prompt concise to save tokens
	prompt := fmt.Sprintf(`
[Autonomous Exploration]
Topic: %s
Goal: Search, synthesize, and save findings to knowledge base (Category: Knowledge).
Constraints: READ-ONLY, Concise, Save memory.
`, topic)

	// 4. Run Agent in Autonomous Mode
	// We must wrap the context to enforce Safety Guard
	autoCtx := context.WithValue(ctx, ContextKeyAutonomous, true)

	// Use a dedicated session for exploration
	events, err := ag.Run(autoCtx, ExplorationSessionID, prompt, "normal")
	if err != nil {
		return fmt.Errorf("failed to start exploration agent: %w", err)
	}

	// 5. Wait for completion (Consumer Loop)
	// We need to consume events to let the agent run
	timeout := time.After(5 * time.Minute)

	for {
		select {
		case <-timeout:
			// Cancel?
			logging.Warn("Exploration timed out")
			return nil // Or error
		case event, ok := <-events:
			if !ok {
				// Channel closed, done
				logging.Info("Exploration Agent Finished")
				return nil
			}

			switch event.Type {
			case agent.AgentEventTypeError:
				logging.Error("Exploration Agent Error", "error", event.Error)
			case agent.AgentEventTypeResponse:
				// Log final response snippet
				if len(event.Content) > 100 {
					logging.Debug("Exploration Thought", "content", event.Content[:100]+"...")
				}
			}
		}
	}
}
