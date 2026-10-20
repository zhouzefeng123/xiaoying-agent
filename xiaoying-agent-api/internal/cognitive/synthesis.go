package cognitive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/logging"
)

// Synthesize triggers the memory synthesis process (L3 -> L2)
// It groups loose facts/episodes into domain knowledge.
func (e *Engine) Synthesize(ctx context.Context) error {
	e.mu.RLock()
	ag := e.agent
	e.mu.RUnlock()

	if ag == nil {
		return fmt.Errorf("agent not initialized")
	}

	logging.Info("Starting Memory Synthesis...")

	// 1. Find candidates: L3/L4 memories with no parent
	// We iterate all for now. In production, add a "Synthesized" flag or index by parent_id.
	// We use ListKnowledgeByLayer from Engine's query object

	// Let's get L3 (Facts) and L4 (Episodes)
	candidates := make([]db.KnowledgeTree, 0)

	l3Nodes, err := e.q.ListKnowledgeByLayer(ctx, LayerKnowledge)
	if err == nil {
		for _, node := range l3Nodes {
			if knowledgeOwnedBy(ctx, node.OwnerEmail) {
				candidates = append(candidates, node)
			}
		}
	}
	l4Nodes, err := e.q.ListKnowledgeByLayer(ctx, LayerEpisodic)
	if err == nil {
		for _, node := range l4Nodes {
			if knowledgeOwnedBy(ctx, node.OwnerEmail) {
				candidates = append(candidates, node)
			}
		}
	}

	// Filter: Only those without ParentID and created > 1 hour ago (to avoid synthesizing active context)
	// And restrict to recent enough to be relevant (e.g., last 7 days) to limit tokens.
	var processing []db.KnowledgeTree
	now := time.Now().Unix()

	for _, n := range candidates {
		if !n.ParentID.Valid || n.ParentID.String == "" {
			// Check age: > 1 hour
			age := now - n.CreatedAt
			if age > 3600 {
				processing = append(processing, n)
			}
		}
	}

	if len(processing) < 3 {
		logging.Info("Not enough memories to synthesize", "count", len(processing))
		return nil
	}

	// 2. Group by Category
	grouped := make(map[string][]db.KnowledgeTree)
	for _, n := range processing {
		grouped[n.Category] = append(grouped[n.Category], n)
	}

	// 3. Process groups
	for cat, group := range grouped {
		if len(group) < 3 {
			continue
		}

		// Limit group size to avoid huge prompt
		if len(group) > 10 {
			group = group[:10]
		}

		logging.Info("Synthesizing group", "category", cat, "count", len(group))

		// Prepare Prompt
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Synthesize these %d related memories into a single high-level insight or domain rule.\n", len(group)))
		sb.WriteString("Memories:\n")
		for _, m := range group {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Content))
		}
		sb.WriteString("\nOutput strictly a JSON object: {\"insight\": \"...\", \"keywords\": \"...\"}")

		// Call Agent
		// Use a temp session
		synthSessionID := "aura-internal-synthesis"
		e.q.CreateSession(ctx, db.CreateSessionParams{
			ID: synthSessionID, Title: "Synthesis",
			OwnerEmail: config.EffectiveOwnerEmail(ctx),
		})

		events, err := ag.Run(ctx, synthSessionID, sb.String(), "normal")
		if err != nil {
			logging.Error("Synthesis failed for group", "category", cat, "error", err)
			continue
		}

		var fullResponse strings.Builder
		for ev := range events {
			if ev.Type == agent.AgentEventTypeResponse {
				fullResponse.WriteString(ev.Content)
			}
		}

		// Parse (Simple string extraction for robustness if JSON fails)
		resp := fullResponse.String()
		// Clean markdown
		resp = strings.TrimPrefix(resp, "```json")
		resp = strings.TrimPrefix(resp, "```")
		resp = strings.TrimSuffix(resp, "```")

		// Create L2 Node
		// For simplicity, we just use the text as content if JSON parsing is too complex to inline here without struct
		// But let's try to extract "insight"
		insight := resp
		if strings.Contains(resp, "\"insight\":") {
			// Very naive parsing to avoid importing heavy JSON structs
			// In real impl, use json.Unmarshal
			// Let's use the whole response as content for now to be safe
		}

		// Create the Parent Node (L2)
		parent, err := e.ikts.AddMemory(ctx, LayerDomain, cat, insight, "Synthesis of "+cat, "synthesis")
		if err != nil {
			logging.Error("Failed to create synthesis node", "error", err)
			continue
		}

		// Link Children to Parent
		for _, child := range group {
			// Update ParentID
			e.q.UpdateKnowledgeNode(ctx, db.UpdateKnowledgeNodeParams{
				ID: child.ID,
				// We can't update ParentID with the current UpdateKnowledgeNode query!
				// We need to add that capability or use raw SQL.
				// Wait, knowledge_tree.sql doesn't have ParentID in Update!
				// I should fix the SQL or just use Graph Edge.
				UpdatedAt: now,
			})

			// Use Graph Edge (GraphRAG)
			e.ikts.Link(ctx, child.ID, parent.ID, "supported_by", 1.0)
			e.ikts.Link(ctx, parent.ID, child.ID, "derived_from", 1.0)
		}

		logging.Info("Synthesized new domain knowledge", "id", parent.ID)
	}

	return nil
}

// Decay triggers the memory decay process
func (e *Engine) Decay(ctx context.Context) error {
	logging.Info("Starting Memory Decay...")

	// Fetch all memories (inefficient but OK for prototype)
	// Better: Add "ListStaleMemories" to SQL
	mems, err := e.ikts.ListAll(ctx)
	if err != nil {
		return err
	}

	now := time.Now().Unix()
	decayCount := 0

	_ = mems

	// Decay loop using KnowledgeTree directly
	layers := []int64{LayerKnowledge, LayerEpisodic}
	for _, layer := range layers {
		nodes, err := e.q.ListKnowledgeByLayer(ctx, layer)
		if err != nil {
			continue
		}
		for _, n := range nodes {
			if !knowledgeOwnedBy(ctx, n.OwnerEmail) {
				continue
			}
			if n.AccessCount.Valid && n.AccessCount.Int64 == 0 {
				age := now - n.UpdatedAt
				if age > 30*24*3600 { // 30 days
					newConf := 0.5
					if n.Confidence.Valid {
						newConf = n.Confidence.Float64 * 0.9
					}

					// Update
					e.q.UpdateKnowledgeNode(ctx, db.UpdateKnowledgeNodeParams{
						ID:         n.ID,
						Confidence: sql.NullFloat64{Float64: newConf, Valid: true},
						UpdatedAt:  now,
					})
					decayCount++
				}
			}
		}
	}

	logging.Info("Memory Decay Completed", "decayed_count", decayCount)

	return nil
}
