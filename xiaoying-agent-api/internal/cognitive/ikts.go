package cognitive

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/memory"
	"github.com/google/uuid"
)

type IKTS struct {
	q   *db.Queries
	mem memory.Service
}

func NewIKTS(q *db.Queries, mem memory.Service) *IKTS {
	return &IKTS{q: q, mem: mem}
}

// Layer Constants
const (
	LayerCore      = 1
	LayerDomain    = 2
	LayerKnowledge = 3
	LayerEpisodic  = 4
)

func (s *IKTS) AddMemory(ctx context.Context, layer int64, category, content string, summary, keywords string) (db.KnowledgeTree, error) {
	id := uuid.New().String()
	now := time.Now().Unix()

	params := db.CreateKnowledgeNodeParams{
		ID:        id,
		Layer:     layer,
		Category:  category,
		Content:   content,
		Summary:   sql.NullString{String: summary, Valid: summary != ""},
		Keywords:  sql.NullString{String: keywords, Valid: keywords != ""},
		CreatedAt: now,
		UpdatedAt: now,
		// Defaults
		Confidence: sql.NullFloat64{Float64: 1.0, Valid: true},
		Source:     sql.NullString{String: "user", Valid: true},
	}

	return s.q.CreateKnowledgeNode(ctx, params)
}

func (s *IKTS) GetCoreMemories(ctx context.Context) ([]db.KnowledgeTree, error) {
	return s.q.ListKnowledgeByLayer(ctx, LayerCore)
}

func (s *IKTS) GetDomainMemories(ctx context.Context) ([]db.KnowledgeTree, error) {
	return s.q.ListKnowledgeByLayer(ctx, LayerDomain)
}

func (s *IKTS) GetEpisodicMemories(ctx context.Context) ([]db.KnowledgeTree, error) {
	return s.q.ListKnowledgeByLayer(ctx, LayerEpisodic)
}

func (s *IKTS) DeleteNode(ctx context.Context, id string) error {
	return s.q.DeleteKnowledgeNode(ctx, id)
}

func (s *IKTS) UpdateNode(ctx context.Context, id, content, summary string) error {
	now := time.Now().Unix()
	params := db.UpdateKnowledgeNodeParams{
		ID:        id,
		Content:   sql.NullString{String: content, Valid: true},
		Summary:   sql.NullString{String: summary, Valid: summary != ""},
		UpdatedAt: now,
	}
	_, err := s.q.UpdateKnowledgeNode(ctx, params)
	return err
}

func (s *IKTS) Link(ctx context.Context, sourceID, targetID, relation string, weight float64) error {
	return s.mem.Link(ctx, sourceID, targetID, relation, weight)
}

func (s *IKTS) ListAll(ctx context.Context) ([]db.Memory, error) {
	return s.mem.List(ctx)
}

func (s *IKTS) Search(ctx context.Context, query string, limit int) ([]db.Memory, error) {
	return s.mem.Search(ctx, query, limit)
}

func (s *IKTS) RetrieveContext(ctx context.Context, query string) (string, error) {
	// Progressive Disclosure Logic

	// 1. Get L1 (Always)
	l1Nodes, err := s.q.ListKnowledgeByLayer(ctx, LayerCore)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("=== CORE IDENTITY (L1) ===\n")
	for _, n := range l1Nodes {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", n.Category, n.Content))
	}

	// 2. Search L2 & L3 Indices
	if query != "" {
		// Map parameters for SearchKnowledge
		// SQL: (layer = ? OR ? = 0) AND (content LIKE ... OR keywords LIKE ...)

		// Use memory service Search for hybrid search if available, but here we use direct DB for now
		// Or assume IKTS has access to Service? IKTS currently wraps Queries only.
		// Let's stick to DB queries for IKTS struct.

		searchResults, err := s.q.SearchKnowledge(ctx, db.SearchKnowledgeParams{
			Layer:           0, // 0 = all layers
			ContentPattern:  "%" + query + "%",
			KeywordsPattern: sql.NullString{String: "%" + query + "%", Valid: true},
			Limit:           10,
		})

		if err == nil && len(searchResults) > 0 {
			sb.WriteString("\n=== RELEVANT KNOWLEDGE (L2/L3) ===\n")
			graphExpandCount := 0
			for _, n := range searchResults {
				sb.WriteString(fmt.Sprintf("- [%s] %s (Confidence: %.2f)\n", n.Category, n.Content, n.Confidence.Float64))

				// GraphRAG: Fetch related edges if confidence is high
				// Performance Optimization: Limit expansion to top 3 most relevant nodes to avoid N+1 query explosion
				if n.Confidence.Float64 > 0.8 && graphExpandCount < 3 {
					edges, err := s.q.GetKnowledgeEdges(ctx, n.ID)
					if err == nil && len(edges) > 0 {
						sb.WriteString("  -> Related:\n")
						for _, e := range edges {
							sb.WriteString(fmt.Sprintf("     - [%s] %s (%.2f)\n", e.Relation, e.TargetContent, e.Weight.Float64))
						}
					}
					graphExpandCount++
				}
			}
		}
	}

	return sb.String(), nil
}

// DecayMemories reduces confidence of stale/unused memories to maintain high signal-to-noise.
func (s *IKTS) DecayMemories(ctx context.Context) error {
	now := time.Now().Unix()
	// Apply decay only to L3/L4 to keep identity/domain stable.
	layers := []int64{LayerKnowledge, LayerEpisodic}
	for _, layer := range layers {
		nodes, err := s.q.ListKnowledgeByLayer(ctx, layer)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if !n.Confidence.Valid {
				continue
			}
			lastTouched := n.UpdatedAt
			if n.LastAccessedAt.Valid && n.LastAccessedAt.Int64 > lastTouched {
				lastTouched = n.LastAccessedAt.Int64
			}
			ageDays := float64(now-lastTouched) / 86400.0
			if ageDays < 3 {
				continue
			}

			// Base decay by age; stronger penalty for never-accessed memories.
			decay := 0.98
			accessCount := int64(0)
			if n.AccessCount.Valid {
				accessCount = n.AccessCount.Int64
			}
			if accessCount < 1 {
				decay = 0.95
			} else if accessCount < 3 {
				decay = 0.97
			}
			newConf := n.Confidence.Float64 * math.Pow(decay, ageDays/3.0)
			if newConf < 0.05 {
				// Remove low-confidence noise.
				if err := s.q.DeleteKnowledgeNode(ctx, n.ID); err != nil {
					return err
				}
				continue
			}
			if newConf > n.Confidence.Float64 {
				continue
			}
			if err := s.q.UpdateKnowledgeNodeConfidence(ctx, db.UpdateKnowledgeNodeConfidenceParams{
				Confidence: sql.NullFloat64{Float64: newConf, Valid: true},
				ID:         n.ID,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
