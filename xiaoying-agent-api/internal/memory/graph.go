package memory

import (
	"context"
	"database/sql"
	"time"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/logging"
)

// GraphRAG Implementation

func (s *service) Link(ctx context.Context, sourceID, targetID, relation string, weight float64) error {
	now := time.Now().Unix()
	params := db.CreateKnowledgeEdgeParams{
		SourceID:  sourceID,
		TargetID:  targetID,
		Relation:  relation,
		Weight:    sql.NullFloat64{Float64: weight, Valid: true},
		CreatedAt: now,
	}
	return s.q.CreateKnowledgeEdge(ctx, params)
}

func (s *service) GetRelated(ctx context.Context, id string) ([]RelatedMemory, error) {
	// Get outgoing edges
	edges, err := s.q.GetKnowledgeEdges(ctx, id)
	if err != nil {
		return nil, err
	}

	var related []RelatedMemory
	for _, edge := range edges {
		// We only have limited fields from the JOIN in SQL (content, category).
		// Ideally we should hydrate the full memory object if needed,
		// but for now let's construct a partial one or fetch it.
		// Fetching fully is safer for consistency.
		mem, err := s.Get(ctx, edge.TargetID)
		if err != nil {
			logging.Warn("Failed to fetch related memory", "id", edge.TargetID, "error", err)
			continue
		}
		related = append(related, RelatedMemory{
			Memory:   *mem,
			Relation: edge.Relation,
			Weight:   edge.Weight.Float64,
		})
	}
	return related, nil
}
