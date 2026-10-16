package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/embedding"
	"github.com/auramate/auramate/internal/logging"
	"github.com/google/uuid"
)

type Service interface {
	Add(ctx context.Context, category, content, source string) (*db.Memory, error)
	AddWithPolicy(ctx context.Context, req MemoryWriteRequest) (*MemoryWriteResult, error)
	List(ctx context.Context) ([]db.Memory, error)
	ListByCategory(ctx context.Context, category string) ([]db.Memory, error)
	// Phase 1 Optimization: Scoped Semantic Search
	SearchByCategory(ctx context.Context, query string, category string, limit int) ([]db.Memory, error)
	SearchByCategoryWithPolicy(ctx context.Context, req MemorySearchRequest) (*MemorySearchResult, error)
	Deduplicate(ctx context.Context, threshold float32, dryRun bool) (DedupStats, error)
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*db.Memory, error)
	Update(ctx context.Context, id, category, content, source string) (*db.Memory, error)
	MarkUsed(ctx context.Context, ids []string) error
	UpdateConfidence(ctx context.Context, id string, delta float64) error
	Search(ctx context.Context, query string, limit int) ([]db.Memory, error)
	SearchWithPolicy(ctx context.Context, req MemorySearchRequest) (*MemorySearchResult, error)
	// GraphRAG Support
	Link(ctx context.Context, sourceID, targetID, relation string, weight float64) error
	GetRelated(ctx context.Context, id string) ([]RelatedMemory, error)
}

type RelatedMemory struct {
	Memory   db.Memory
	Relation string
	Weight   float64
}

type DedupStats struct {
	TotalMemories       int `json:"total_memories"`
	ScannedCategories   int `json:"scanned_categories"`
	DuplicateByText     int `json:"duplicate_by_text"`
	DuplicateBySemantic int `json:"duplicate_by_semantic"`
	Deleted             int `json:"deleted"`
}

type service struct {
	q        *db.Queries
	db       *sql.DB
	embedder embedding.Embedder
	mu       sync.Mutex

	// Cache fields
	cacheMu     sync.RWMutex
	cache       []CachedMemory
	cacheLoaded bool
	cacheWait   sync.WaitGroup // Add WaitGroup for cache loading
}

type CachedMemory struct {
	Memory      db.Memory
	Vector      []float32
	Layer       int64
	AccessCount int64
}

type memoryUsageSignal struct {
	AccessCount    int64
	LastAccessedAt sql.NullInt64
	Confidence     sql.NullFloat64
}

func NewService(q *db.Queries, conn *sql.DB, embedder embedding.Embedder) Service {
	s := &service{q: q, db: conn, embedder: embedder}
	// Start loading cache asynchronously
	s.cacheWait.Add(1) // Initialize WaitGroup
	go s.loadCache()
	return s
}

func (s *service) loadCache() {
	defer s.cacheWait.Done() // Signal completion
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use a large limit to fetch all memories
	params := db.SearchKnowledgeParams{
		Layer:           0, // 0 = search all layers
		ContentPattern:  "%%",
		KeywordsPattern: sql.NullString{String: "%%", Valid: true},
		Limit:           5000, // Fetch up to 5000 memories
	}
	nodes, err := s.q.SearchKnowledge(ctx, params)
	if err != nil {
		logging.Error("Failed to load memory cache", "error", err)
		return
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.cache = make([]CachedMemory, 0, len(nodes))

	for _, n := range nodes {
		mem := s.toMemory(n)
		var vec []float32
		if mem.Embedding.Valid {
			// Parse embedding once during load
			_ = json.Unmarshal([]byte(mem.Embedding.String), &vec)
		}
		s.cache = append(s.cache, CachedMemory{
			Memory:      *mem,
			Vector:      vec,
			Layer:       n.Layer,
			AccessCount: nullInt64Value(n.AccessCount),
		})
	}
	s.cacheLoaded = true
	logging.Info("Memory cache loaded", "count", len(s.cache), "duration_ms", time.Since(start).Milliseconds())
}

func layerForCategory(category string) int64 {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "identity", "habit", "preference", "interest":
		return 1
	case "fact", "knowledge":
		return 3
	default:
		return 2
	}
}

func nullInt64Value(value sql.NullInt64) int64 {
	if value.Valid {
		return value.Int64
	}
	return 0
}

func nullFloat64Value(value sql.NullFloat64, fallback float64) float64 {
	if value.Valid {
		return value.Float64
	}
	return fallback
}

func recencyBoost(lastAccessed sql.NullInt64, now int64) float64 {
	if !lastAccessed.Valid || lastAccessed.Int64 <= 0 || now <= lastAccessed.Int64 {
		return 0
	}
	ageHours := float64(now-lastAccessed.Int64) / 3600.0
	switch {
	case ageHours <= 1:
		return 0.06
	case ageHours <= 24:
		return 0.04
	case ageHours <= 24*7:
		return 0.02
	default:
		return 0
	}
}

func accessBoost(accessCount int64) float64 {
	if accessCount <= 0 {
		return 0
	}
	return math.Min(0.08, math.Log1p(float64(accessCount))*0.02)
}

func confidenceBoost(confidence sql.NullFloat64) float64 {
	return math.Max(0, math.Min(0.05, nullFloat64Value(confidence, 0.5)*0.05))
}

func categoryListSortScore(item CachedMemory, category string, now int64) float64 {
	base := recencyBoost(item.Memory.LastAccessedAt, now) + accessBoost(item.AccessCount) + confidenceBoost(item.Memory.Confidence)
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "session_summary":
		return float64(item.Memory.UpdatedAt)/1e12 + recencyBoost(item.Memory.LastAccessedAt, now)
	case "experience", "instruction":
		return confidenceBoost(item.Memory.Confidence) + recencyBoost(item.Memory.LastAccessedAt, now) + accessBoost(item.AccessCount)
	default:
		return base
	}
}

func sortCategoryCachedMemories(items []CachedMemory, category string) {
	now := time.Now().Unix()
	sort.Slice(items, func(i, j int) bool {
		left := categoryListSortScore(items[i], category, now)
		right := categoryListSortScore(items[j], category, now)
		if left != right {
			return left > right
		}
		if items[i].Memory.UpdatedAt != items[j].Memory.UpdatedAt {
			return items[i].Memory.UpdatedAt > items[j].Memory.UpdatedAt
		}
		if items[i].Memory.CreatedAt != items[j].Memory.CreatedAt {
			return items[i].Memory.CreatedAt > items[j].Memory.CreatedAt
		}
		return items[i].Memory.ID < items[j].Memory.ID
	})
}

func sortCategoryMemories(items []db.Memory, category string) {
	now := time.Now().Unix()
	sort.Slice(items, func(i, j int) bool {
		left := recencyBoost(items[i].LastAccessedAt, now) + confidenceBoost(items[i].Confidence)
		right := recencyBoost(items[j].LastAccessedAt, now) + confidenceBoost(items[j].Confidence)
		if strings.EqualFold(category, "session_summary") {
			left = float64(items[i].UpdatedAt)/1e12 + recencyBoost(items[i].LastAccessedAt, now)
			right = float64(items[j].UpdatedAt)/1e12 + recencyBoost(items[j].LastAccessedAt, now)
		}
		if left != right {
			return left > right
		}
		if items[i].UpdatedAt != items[j].UpdatedAt {
			return items[i].UpdatedAt > items[j].UpdatedAt
		}
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt > items[j].CreatedAt
		}
		return items[i].ID < items[j].ID
	})
}

func isDefaultHiddenMemoryCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "todo_snapshot", "plan_snapshot", "session_summary":
		return true
	default:
		return false
	}
}

func filterDefaultVisibleMemories(items []db.Memory) []db.Memory {
	if len(items) == 0 {
		return items
	}
	filtered := make([]db.Memory, 0, len(items))
	for _, item := range items {
		if isDefaultHiddenMemoryCategory(item.Category) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func (s *service) usageSignalsByID() map[string]memoryUsageSignal {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if !s.cacheLoaded || len(s.cache) == 0 {
		return nil
	}
	signals := make(map[string]memoryUsageSignal, len(s.cache))
	for _, item := range s.cache {
		signals[item.Memory.ID] = memoryUsageSignal{
			AccessCount:    item.AccessCount,
			LastAccessedAt: item.Memory.LastAccessedAt,
			Confidence:     item.Memory.Confidence,
		}
	}
	return signals
}

func getEnvFloat32(key string, defaultValue float32) float32 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}
	v, err := strconv.ParseFloat(raw, 32)
	if err != nil {
		logging.Warn("Invalid float env value, fallback to default", "key", key, "value", raw, "error", err)
		return defaultValue
	}
	return float32(v)
}

func (s *service) Add(ctx context.Context, category, content, source string) (*db.Memory, error) {
	result, err := s.AddWithPolicy(ctx, DefaultWriteRequest(category, content, source))
	if err != nil {
		return nil, err
	}
	return result.Memory, nil
}

func (s *service) AddWithPolicy(ctx context.Context, req MemoryWriteRequest) (*MemoryWriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	req.Category = strings.TrimSpace(req.Category)
	req.Source = canonicalMemorySource(req.Source)
	if strings.TrimSpace(req.Trigger) == "" {
		req.Trigger = req.Source
	}
	if strings.TrimSpace(req.Category) == "" {
		return nil, fmt.Errorf("memory category is required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, fmt.Errorf("memory content is required")
	}
	cleanedContent, ok := SanitizeMemoryContentForStorage(req.Category, req.Content)
	if !ok || strings.TrimSpace(cleanedContent) == "" {
		return nil, fmt.Errorf("memory content is empty after sanitization")
	}
	req.Content = cleanedContent

	decision := MemoryWriteDecision{
		Contract:            memoryContractVersion,
		Action:              MemoryWriteActionInserted,
		Category:            req.Category,
		Layer:               layerForCategory(req.Category),
		Trigger:             strings.TrimSpace(req.Trigger),
		IncomingSource:      req.Source,
		IncomingSourceTrust: memorySourceTrust(req.Source),
		EvidenceCount:       len(req.Evidence),
	}
	decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonContractApplied)
	decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonSourceTrustEvaluated)
	if len(req.Evidence) > 0 {
		decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonEvidenceAttached)
	}

	conflictKey := buildMemoryConflictKey(req.Category, req.Content, req.ConflictKey)
	decision.ConflictKey = conflictKey
	if conflictKey != "" {
		decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonConflictKeyDetected)
	}

	normalizedNew := normalizeMemoryContent(req.Content)
	existing, err := s.ListByCategory(ctx, req.Category)
	if err == nil {
		for _, m := range existing {
			if normalizeMemoryContent(m.Content) != normalizedNew {
				continue
			}
			decision.Action = MemoryWriteActionReusedExisting
			decision.ExistingMemoryID = m.ID
			decision.ExistingSource = m.Source.String
			decision.ExistingSourceTrust = memorySourceTrust(m.Source.String)
			decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonExactDuplicate)
			if decision.ExistingSourceTrust >= decision.IncomingSourceTrust {
				decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonSourcePreferred)
			}
			logging.Info("Memory deduplication: exact content match, skipping insert", "id", m.ID, "category", req.Category, "decision", decision.Action)
			return &MemoryWriteResult{Memory: &m, Decision: decision}, nil
		}

		if conflictKey != "" {
			for _, m := range existing {
				if buildMemoryConflictKey(m.Category, m.Content, "") != conflictKey {
					continue
				}

				decision.ExistingMemoryID = m.ID
				decision.ExistingSource = m.Source.String
				decision.ExistingSourceTrust = memorySourceTrust(m.Source.String)
				decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonConflictDetected)

				if shouldOverwriteExistingMemory(req.Category, decision.IncomingSourceTrust, decision.ExistingSourceTrust, req.AllowOverwrite) {
					updated, updateErr := s.Update(ctx, m.ID, req.Category, req.Content, req.Source)
					if updateErr != nil {
						return nil, updateErr
					}
					decision.Action = MemoryWriteActionOverwroteExisting
					decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonOverwritePerformed)
					if req.AllowOverwrite {
						decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonExplicitOverwrite)
					}
					if decision.IncomingSourceTrust >= decision.ExistingSourceTrust {
						decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonIncomingPreferred)
					}
					logging.Info("Memory conflict resolved by overwrite", "id", m.ID, "category", req.Category, "conflict_key", conflictKey, "incoming_source", req.Source, "existing_source", m.Source.String)
					return &MemoryWriteResult{Memory: updated, Decision: decision}, nil
				}

				decision.Action = MemoryWriteActionRejectedConflict
				decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonConflictRejected)
				decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonSourcePreferred)
				logging.Info("Memory conflict rejected", "id", m.ID, "category", req.Category, "conflict_key", conflictKey, "incoming_source", req.Source, "existing_source", m.Source.String)
				return &MemoryWriteResult{Memory: &m, Decision: decision}, nil
			}
		}
	}

	semanticThreshold := getEnvFloat32("MEMORY_ADD_SEMANTIC_DEDUP_THRESHOLD", 0.92)
	if semanticThreshold < 0.0 {
		semanticThreshold = 0.0
	}
	if semanticThreshold > 1.0 {
		semanticThreshold = 1.0
	}

	if req.AllowSemanticDedup && s.embedder != nil {
		newEmbeddings, embErr := s.embedder.Embed(ctx, []string{req.Content})
		if embErr != nil {
			logging.Warn("Memory semantic dedup: embedding generation failed, fallback to insert", "error", embErr)
		} else if len(newEmbeddings) > 0 {
			newVec := newEmbeddings[0]
			bestScore := float32(-1.0)
			var bestMatch *db.Memory

			for _, m := range existing {
				existingConflictKey := buildMemoryConflictKey(m.Category, m.Content, "")
				if !semanticDedupAllowed(conflictKey, existingConflictKey, req.Content, m.Content) {
					continue
				}
				if !m.Embedding.Valid {
					continue
				}
				var oldVec []float32
				if err := json.Unmarshal([]byte(m.Embedding.String), &oldVec); err != nil || len(oldVec) == 0 {
					continue
				}
				score := cosineSimilarity(newVec, oldVec)
				if score > bestScore {
					mCopy := m
					bestScore = score
					bestMatch = &mCopy
				}
			}

			if bestMatch != nil && bestScore >= semanticThreshold {
				decision.Action = MemoryWriteActionReusedExisting
				decision.ExistingMemoryID = bestMatch.ID
				decision.ExistingSource = bestMatch.Source.String
				decision.ExistingSourceTrust = memorySourceTrust(bestMatch.Source.String)
				decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonSemanticDuplicate)
				if decision.ExistingSourceTrust >= decision.IncomingSourceTrust {
					decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonSourcePreferred)
				}
				logging.Info("Memory deduplication: semantic match found, skipping insert", "id", bestMatch.ID, "category", req.Category, "score", bestScore, "threshold", semanticThreshold)
				return &MemoryWriteResult{Memory: bestMatch, Decision: decision}, nil
			}
		}
	}

	id := uuid.New().String()
	now := time.Now().Unix()

	var embeddingJSON sql.NullString
	if s.embedder != nil {
		embeddings, embedErr := s.embedder.Embed(ctx, []string{req.Content})
		if embedErr == nil && len(embeddings) > 0 {
			b, _ := json.Marshal(embeddings[0])
			embeddingJSON = sql.NullString{String: string(b), Valid: true}
			decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonEmbeddingGenerated)
		} else if embedErr != nil {
			logging.Error("Failed to generate embedding for memory", "error", embedErr)
			decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonEmbeddingUnavailable)
		}
	} else {
		decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonEmbeddingUnavailable)
	}

	params := db.CreateKnowledgeNodeParams{
		ID:             id,
		Layer:          layerForCategory(req.Category),
		Category:       req.Category,
		Content:        req.Content,
		Source:         sql.NullString{String: req.Source, Valid: true},
		Embedding:      embeddingJSON,
		CreatedAt:      now,
		UpdatedAt:      now,
		Confidence:     sql.NullFloat64{Float64: 1.0, Valid: true},
		LastAccessedAt: sql.NullInt64{Int64: now, Valid: true},
	}

	kt, err := s.q.CreateKnowledgeNode(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create memory: %w", err)
	}

	if embeddingJSON.Valid {
		_, execErr := s.db.ExecContext(ctx, "UPDATE knowledge_tree SET embedding = ? WHERE id = ?", embeddingJSON, id)
		if execErr != nil {
			logging.Error("Failed to save embedding", "error", execErr)
		}
		kt.Embedding = embeddingJSON
	}

	mem := s.toMemory(kt)
	var vec []float32
	if embeddingJSON.Valid {
		_ = json.Unmarshal([]byte(embeddingJSON.String), &vec)
	}

	s.cacheMu.Lock()
	if s.cacheLoaded {
		s.cache = append(s.cache, CachedMemory{
			Memory:      *mem,
			Vector:      vec,
			Layer:       params.Layer,
			AccessCount: 0,
		})
	}
	s.cacheMu.Unlock()

	go func(newMem *db.Memory) {
		time.Sleep(100 * time.Millisecond)
		linkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		candidates, searchErr := s.Search(linkCtx, newMem.Content, 3)
		if searchErr == nil {
			for _, c := range candidates {
				if c.ID == newMem.ID {
					continue
				}
				logging.Info("Auto-Linking memories", "source", newMem.ID, "target", c.ID)
				_ = s.Link(linkCtx, newMem.ID, c.ID, "related_to", 0.8)
			}
		}
	}(mem)

	decision.ReasonCodes = appendUniqueWriteReason(decision.ReasonCodes, MemoryWriteReasonInsertPerformed)
	return &MemoryWriteResult{Memory: mem, Decision: decision}, nil
}

func (s *service) toMemory(kt db.KnowledgeTree) *db.Memory {
	return &db.Memory{
		ID:             kt.ID,
		Category:       kt.Category,
		Content:        kt.Content,
		Source:         kt.Source,
		Embedding:      kt.Embedding,
		CreatedAt:      kt.CreatedAt,
		UpdatedAt:      kt.UpdatedAt,
		Confidence:     kt.Confidence,
		LastAccessedAt: kt.LastAccessedAt,
	}
}

func (s *service) List(ctx context.Context) ([]db.Memory, error) {
	// Fallback to searching all (layer=0, query='') if no ListAll method
	// Using SearchKnowledge with empty query
	params := db.SearchKnowledgeParams{
		Layer:           0,
		ContentPattern:  "%%",
		KeywordsPattern: sql.NullString{String: "%%", Valid: true},
		Limit:           1000,
	}
	nodes, err := s.q.SearchKnowledge(ctx, params)
	if err != nil {
		return nil, err
	}

	mems := make([]db.Memory, 0)
	for _, n := range nodes {
		mems = append(mems, *s.toMemory(n))
	}
	return mems, nil
}

func (s *service) ListByCategory(ctx context.Context, category string) ([]db.Memory, error) {
	// Try to use cache first
	s.cacheMu.RLock()
	if s.cacheLoaded {
		defer s.cacheMu.RUnlock()
		var filtered []CachedMemory
		for _, m := range s.cache {
			if m.Memory.Category == category {
				filtered = append(filtered, m)
			}
		}
		sortCategoryCachedMemories(filtered, category)
		result := make([]db.Memory, 0, len(filtered))
		for _, item := range filtered {
			result = append(result, item.Memory)
		}
		return result, nil
	}
	s.cacheMu.RUnlock()

	// Fallback to DB if cache not loaded
	// SearchKnowledge doesn't filter by category directly in SQL
	// We have to filter in code or use ListMemoriesByCategory if we kept the table.
	// But we are moving to KnowledgeTree.
	// We can iterate List() results or implement a custom query.
	// For now, let's fetch all and filter (inefficient but works for small datasets)
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}

	var filtered []db.Memory
	for _, m := range all {
		if m.Category == category {
			filtered = append(filtered, m)
		}
	}
	sortCategoryMemories(filtered, category)
	return filtered, nil
}

func normalizeMemoryContent(content string) string {
	content = strings.ToLower(strings.TrimSpace(content))
	content = strings.TrimRight(content, ".,，!！?？")
	content = strings.Join(strings.Fields(content), " ")
	return content
}

func (s *service) Deduplicate(ctx context.Context, threshold float32, dryRun bool) (DedupStats, error) {
	if threshold <= 0 {
		threshold = 0.92
	}

	memories, err := s.List(ctx)
	if err != nil {
		return DedupStats{}, err
	}

	stats := DedupStats{TotalMemories: len(memories)}
	byCategory := make(map[string][]db.Memory)
	for _, m := range memories {
		byCategory[m.Category] = append(byCategory[m.Category], m)
	}
	stats.ScannedCategories = len(byCategory)

	deleteSet := make(map[string]struct{})

	for _, catMems := range byCategory {
		sort.Slice(catMems, func(i, j int) bool {
			if catMems[i].CreatedAt == catMems[j].CreatedAt {
				return catMems[i].ID < catMems[j].ID
			}
			return catMems[i].CreatedAt < catMems[j].CreatedAt
		})

		seenNorm := make(map[string]string)
		for _, m := range catMems {
			norm := normalizeMemoryContent(m.Content)
			if keepID, ok := seenNorm[norm]; ok && keepID != m.ID {
				if _, exists := deleteSet[m.ID]; !exists {
					deleteSet[m.ID] = struct{}{}
					stats.DuplicateByText++
				}
				continue
			}
			seenNorm[norm] = m.ID
		}

		type memVec struct {
			id          string
			vec         []float32
			content     string
			conflictKey string
		}
		var vectors []memVec
		for _, m := range catMems {
			if _, dup := deleteSet[m.ID]; dup || !m.Embedding.Valid {
				continue
			}
			var vec []float32
			if err := json.Unmarshal([]byte(m.Embedding.String), &vec); err != nil || len(vec) == 0 {
				continue
			}
			vectors = append(vectors, memVec{
				id:          m.ID,
				vec:         vec,
				content:     m.Content,
				conflictKey: buildMemoryConflictKey(m.Category, m.Content, ""),
			})
		}

		for i := 0; i < len(vectors); i++ {
			if _, dup := deleteSet[vectors[i].id]; dup {
				continue
			}
			for j := i + 1; j < len(vectors); j++ {
				if _, dup := deleteSet[vectors[j].id]; dup {
					continue
				}
				if !semanticDedupAllowed(vectors[i].conflictKey, vectors[j].conflictKey, vectors[i].content, vectors[j].content) {
					continue
				}
				if cosineSimilarity(vectors[i].vec, vectors[j].vec) >= threshold {
					deleteSet[vectors[j].id] = struct{}{}
					stats.DuplicateBySemantic++
				}
			}
		}
	}

	if dryRun {
		stats.Deleted = len(deleteSet)
		return stats, nil
	}

	for id := range deleteSet {
		if err := s.Delete(ctx, id); err != nil {
			logging.Warn("Memory dedup delete failed", "id", id, "error", err)
			continue
		}
		stats.Deleted++
	}

	return stats, nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	err := s.q.DeleteKnowledgeNode(ctx, id)
	if err != nil {
		return err
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cacheLoaded {
		for i, m := range s.cache {
			if m.Memory.ID == id {
				s.cache = append(s.cache[:i], s.cache[i+1:]...)
				break
			}
		}
	}
	return nil
}

func (s *service) Get(ctx context.Context, id string) (*db.Memory, error) {
	kt, err := s.q.GetKnowledgeNode(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.toMemory(kt), nil
}

func (s *service) Update(ctx context.Context, id, category, content, source string) (*db.Memory, error) {
	cleanedContent, ok := SanitizeMemoryContentForStorage(category, content)
	if !ok || strings.TrimSpace(cleanedContent) == "" {
		return nil, fmt.Errorf("memory content is empty after sanitization")
	}
	content = cleanedContent

	now := time.Now().Unix()
	layer := layerForCategory(category)

	var embeddingJSON sql.NullString
	if s.embedder != nil {
		embeddings, err := s.embedder.Embed(ctx, []string{content})
		if err == nil && len(embeddings) > 0 {
			b, _ := json.Marshal(embeddings[0])
			embeddingJSON = sql.NullString{String: string(b), Valid: true}
		} else if err != nil {
			logging.Error("Failed to generate embedding for update", "error", err)
		}
	}

	// NOTE: plain `?` placeholders so the statement runs on MySQL and SQLite
	// alike; MySQL has no RETURNING, so the row is read back afterwards.
	updateQuery := `UPDATE knowledge_tree
	SET category = ?,
	    layer = ?,
	    content = ?,
	    source = coalesce(?, source),
	    embedding = coalesce(?, embedding),
	    updated_at = ?
	WHERE id = ?`

	if _, err := s.db.ExecContext(ctx, updateQuery,
		category,
		layer,
		content,
		sql.NullString{String: source, Valid: strings.TrimSpace(source) != ""},
		embeddingJSON,
		now,
		id,
	); err != nil {
		return nil, err
	}

	kt, err := s.q.GetKnowledgeNode(ctx, id)
	if err != nil {
		return nil, err
	}

	mem := s.toMemory(kt)

	var vec []float32
	if mem.Embedding.Valid {
		_ = json.Unmarshal([]byte(mem.Embedding.String), &vec)
	}

	s.cacheMu.Lock()
	if s.cacheLoaded {
		for i, m := range s.cache {
			if m.Memory.ID == id {
				s.cache[i] = CachedMemory{
					Memory:      *mem,
					Vector:      vec,
					Layer:       kt.Layer,
					AccessCount: nullInt64Value(kt.AccessCount),
				}
				break
			}
		}
	}
	s.cacheMu.Unlock()

	return mem, nil
}

func (s *service) MarkUsed(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(ids))
	now := time.Now().Unix()
	var firstErr error

	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		if _, err := s.db.ExecContext(ctx, `UPDATE knowledge_tree SET access_count = access_count + 1, last_accessed_at = ? WHERE id = ?`, now, id); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			logging.Warn("Failed to mark memory as used", "id", id, "error", err)
			continue
		}

		s.cacheMu.Lock()
		if s.cacheLoaded {
			for i := range s.cache {
				if s.cache[i].Memory.ID != id {
					continue
				}
				s.cache[i].AccessCount++
				s.cache[i].Memory.LastAccessedAt = sql.NullInt64{Int64: now, Valid: true}
				break
			}
		}
		s.cacheMu.Unlock()
	}

	return firstErr
}

func (s *service) UpdateConfidence(ctx context.Context, id string, delta float64) error {
	node, err := s.q.GetKnowledgeNode(ctx, id)
	if err != nil {
		return err
	}

	newConf := 1.0
	if node.Confidence.Valid {
		newConf = node.Confidence.Float64 + delta
	} else {
		newConf = 1.0 + delta
	}

	if newConf > 1.0 {
		newConf = 1.0
	} else if newConf < 0.0 {
		newConf = 0.0
	}

	// Check demotion logic (L2 -> L3 if confidence < 0.2)
	layer := node.Layer
	if node.Layer == 2 && newConf < 0.2 {
		layer = 3 // Demote to Knowledge (L3)
		logging.Info("Memory demoted from L2 to L3 due to low confidence", "id", id, "confidence", newConf)
	}

	params := db.UpdateKnowledgeNodeConfidenceParams{
		Confidence:     sql.NullFloat64{Float64: newConf, Valid: true},
		Layer:          sql.NullInt64{Int64: layer, Valid: true},
		LastAccessedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		ID:             id,
	}

	err = s.q.UpdateKnowledgeNodeConfidence(ctx, params)
	if err != nil {
		return err
	}

	// Update cache
	s.cacheMu.Lock()
	if s.cacheLoaded {
		for i, m := range s.cache {
			if m.Memory.ID == id {
				s.cache[i].Memory.Confidence = sql.NullFloat64{Float64: newConf, Valid: true}
				s.cache[i].Memory.LastAccessedAt = sql.NullInt64{Int64: time.Now().Unix(), Valid: true}
				s.cache[i].Layer = layer
				break
			}
		}
	}
	s.cacheMu.Unlock()

	return nil
}

type ScoredMemory struct {
	Memory db.Memory
	Score  float32
}

func (s *service) searchFTS(ctx context.Context, query string, limit int) ([]db.KnowledgeTree, error) {
	// The SQLite build used an fts5 virtual table (knowledge_tree_fts) with
	// `MATCH` + `bm25()`. MySQL indexes knowledge_tree directly with an ngram
	// FULLTEXT index, so this is a MATCH ... AGAINST query instead.
	// Optimize for Recall: BOOLEAN MODE ORs space separated terms, so we return
	// ANY document containing ANY of the keywords. The Reranker then sorts them
	// by semantic similarity.
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return s.searchLexicalFallback(ctx, query, limit)
	}

	// Basic sanitization: BOOLEAN MODE gives these characters special meaning.
	cleanTerms := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.Trim(term, `+-<>()~*"@`)
		if term != "" {
			cleanTerms = append(cleanTerms, term)
		}
	}
	if len(cleanTerms) == 0 {
		return s.searchLexicalFallback(ctx, query, limit)
	}
	ftsQuery := strings.Join(cleanTerms, " ")

	q := `SELECT k.id, k.parent_id, k.layer, k.category, k.content, k.summary, k.keywords, k.confidence, k.source, k.embedding, k.access_count, k.created_at, k.updated_at
	FROM knowledge_tree k
	WHERE MATCH(k.content, k.summary, k.keywords) AGAINST (? IN BOOLEAN MODE)
	ORDER BY MATCH(k.content, k.summary, k.keywords) AGAINST (? IN BOOLEAN MODE) DESC
	LIMIT ?`

	rows, err := s.db.QueryContext(ctx, q, ftsQuery, ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []db.KnowledgeTree
	for rows.Next() {
		var i db.KnowledgeTree
		if err := rows.Scan(
			&i.ID,
			&i.ParentID,
			&i.Layer,
			&i.Category,
			&i.Content,
			&i.Summary,
			&i.Keywords,
			&i.Confidence,
			&i.Source,
			&i.Embedding,
			&i.AccessCount,
			&i.CreatedAt,
			&i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if len(items) == 0 {
		return s.searchLexicalFallback(ctx, query, limit)
	}
	return items, nil
}

func (s *service) searchLexicalFallback(ctx context.Context, query string, limit int) ([]db.KnowledgeTree, error) {
	terms := extractLexicalSearchTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}

	items, err := s.q.SearchKnowledge(ctx, db.SearchKnowledgeParams{
		Layer:           0,
		ContentPattern:  "%%",
		KeywordsPattern: sql.NullString{String: "%%", Valid: true},
		Limit:           int32(lexicalFallbackCandidateLimit(limit)),
	})
	if err != nil {
		return nil, err
	}

	type lexicalHit struct {
		item  db.KnowledgeTree
		hits  int
		score float64
	}

	matched := make([]lexicalHit, 0, limit)
	for _, item := range items {
		corpus := strings.ToLower(strings.Join([]string{item.Content, item.Summary.String, item.Keywords.String}, " "))
		hits := lexicalMatchCount(corpus, terms)
		if hits == 0 {
			continue
		}
		matched = append(matched, lexicalHit{
			item:  item,
			hits:  hits,
			score: nullFloat64Value(item.Confidence, 0.5),
		})
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].hits != matched[j].hits {
			return matched[i].hits > matched[j].hits
		}
		if matched[i].score != matched[j].score {
			return matched[i].score > matched[j].score
		}
		return matched[i].item.UpdatedAt > matched[j].item.UpdatedAt
	})

	if len(matched) > limit {
		matched = matched[:limit]
	}
	result := make([]db.KnowledgeTree, 0, len(matched))
	for _, hit := range matched {
		result = append(result, hit.item)
	}
	return result, nil
}

func lexicalFallbackCandidateLimit(limit int) int {
	if limit <= 0 {
		limit = 1
	}
	candidateLimit := limit * 80
	if candidateLimit < 200 {
		candidateLimit = 200
	}
	if candidateLimit > 2000 {
		candidateLimit = 2000
	}
	return candidateLimit
}

func extractLexicalSearchTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}

	seen := map[string]struct{}{}
	addTerm := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" {
			return
		}
		if _, ok := seen[term]; ok {
			return
		}
		seen[term] = struct{}{}
	}

	for _, field := range strings.Fields(query) {
		addTerm(field)
	}
	for _, alias := range expandCrossLingualLexicalTerms(query) {
		addTerm(alias)
	}

	runes := []rune(query)
	for start := 0; start < len(runes); start++ {
		if !isCJKRune(runes[start]) {
			continue
		}
		end := start
		for end < len(runes) && isCJKRune(runes[end]) {
			end++
		}
		segment := runes[start:end]
		if len(segment) <= 4 {
			addTerm(string(segment))
		}
		for size := 2; size <= 3; size++ {
			if len(segment) < size {
				continue
			}
			for index := 0; index+size <= len(segment); index++ {
				addTerm(string(segment[index : index+size]))
			}
		}
		start = end - 1
	}

	terms := make([]string, 0, len(seen))
	for term := range seen {
		terms = append(terms, term)
	}
	sort.Slice(terms, func(i, j int) bool {
		if len([]rune(terms[i])) != len([]rune(terms[j])) {
			return len([]rune(terms[i])) > len([]rune(terms[j]))
		}
		return terms[i] < terms[j]
	})
	return terms
}

func expandCrossLingualLexicalTerms(query string) []string {
	bilingualLexicon := map[string][]string{
		"图片": {"image", "images", "picture", "pictures"},
		"照片": {"image", "images", "photo", "photos"},
		"项目": {"project", "projects"},
		"工具": {"tool", "tools"},
		"文档": {"document", "documents", "doc", "docs", "docx", "word"},
		"目录": {"directory", "directories", "folder", "folders"},
		"路径": {"path", "paths"},
		"位置": {"location", "locations"},
		"回答": {"answer", "answers", "response", "responses", "reply", "replies"},
		"默认": {"default"},
	}

	aliases := make([]string, 0, 8)
	seen := map[string]struct{}{}
	for phrase, terms := range bilingualLexicon {
		if !strings.Contains(query, phrase) {
			continue
		}
		for _, term := range terms {
			if _, ok := seen[term]; ok {
				continue
			}
			seen[term] = struct{}{}
			aliases = append(aliases, term)
		}
	}
	return aliases
}

func lexicalMatchCount(corpus string, terms []string) int {
	hits := 0
	for _, term := range terms {
		if strings.Contains(corpus, term) {
			hits++
		}
	}
	return hits
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

func queryContainsCJK(query string) bool {
	for _, r := range query {
		if isCJKRune(r) {
			return true
		}
	}
	return false
}

func exactSearchTokens(query string) []string {
	tokenSet := extractMemoryIdentityTokens(query)
	if len(tokenSet) == 0 {
		return nil
	}
	tokens := make([]string, 0, len(tokenSet))
	for token := range tokenSet {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}

func memoryContainsAllTokens(content string, tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	normalized := strings.ToLower(content)
	for _, token := range tokens {
		if !strings.Contains(normalized, token) {
			return false
		}
	}
	return true
}

func (s *service) searchExactMatches(ctx context.Context, req MemorySearchRequest) ([]db.Memory, error) {
	tokens := exactSearchTokens(req.Query)
	if len(tokens) == 0 {
		return nil, nil
	}

	var (
		items []db.Memory
		err   error
	)
	if req.Category != "" {
		items, err = s.ListByCategory(ctx, req.Category)
	} else {
		items, err = s.List(ctx)
		if err == nil {
			items = filterDefaultVisibleMemories(items)
		}
	}
	if err != nil {
		return nil, err
	}

	matches := make([]db.Memory, 0, req.Limit)
	for _, item := range items {
		if memoryContainsAllTokens(item.Content, tokens) {
			matches = append(matches, item)
		}
	}
	if len(matches) > req.Limit {
		matches = matches[:req.Limit]
	}
	return matches, nil
}

func (s *service) Search(ctx context.Context, query string, limit int) ([]db.Memory, error) {
	result, err := s.SearchWithPolicy(ctx, DefaultSearchRequest(query, limit))
	if err != nil {
		return nil, err
	}
	return result.Memories, nil
}

func (s *service) SearchWithPolicy(ctx context.Context, req MemorySearchRequest) (*MemorySearchResult, error) {
	return s.searchWithPolicy(ctx, req)
}

func (s *service) searchVector(ctx context.Context, query string, limit int) ([]db.Memory, error) {
	result, _, err := s.searchVectorWithPolicy(ctx, DefaultSearchRequest(query, limit))
	return result, err
}

func (s *service) searchVectorWithPolicy(ctx context.Context, req MemorySearchRequest) ([]db.Memory, []MemorySearchReasonCode, error) {
	var reasons []MemorySearchReasonCode
	if !req.AllowVector || s.embedder == nil {
		reasons = appendUniqueSearchReason(reasons, MemorySearchReasonVectorUnavailable)
		return nil, reasons, nil
	}

	if !s.cacheLoaded {
		done := make(chan struct{})
		go func() {
			s.cacheWait.Wait()
			close(done)
		}()

		waitForCache := req.WaitForCache
		if waitForCache <= 0 {
			waitForCache = 2 * time.Second
		}

		select {
		case <-done:
		case <-ctx.Done():
			reasons = appendUniqueSearchReason(reasons, MemorySearchReasonCacheWaitCancelled)
			return nil, reasons, ctx.Err()
		case <-time.After(waitForCache):
			logging.Warn("Memory cache load timed out during search, proceeding with empty/partial cache", "stage", req.Stage)
			reasons = appendUniqueSearchReason(reasons, MemorySearchReasonCacheWaitTimedOut)
			return nil, reasons, nil
		}
	}

	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	if !s.cacheLoaded || len(s.cache) == 0 {
		return nil, reasons, nil
	}

	// Generate query embedding
	embeddings, err := s.embedder.Embed(ctx, []string{req.Query})
	if err != nil || len(embeddings) == 0 {
		return nil, reasons, fmt.Errorf("embedding generation failed: %w", err)
	}
	queryVec := embeddings[0]

	// Calculate scores
	var scored []ScoredMemory
	now := time.Now().Unix()
	for _, m := range s.cache {
		if req.Category != "" && m.Memory.Category != req.Category {
			continue
		}
		if req.Category == "" && isDefaultHiddenMemoryCategory(m.Memory.Category) {
			continue
		}
		if len(m.Vector) == 0 {
			continue
		}

		// Use cached vector directly
		score := cosineSimilarity(queryVec, m.Vector)
		if score > req.VectorScoreThreshold {
			composite := score + float32(accessBoost(m.AccessCount)+recencyBoost(m.Memory.LastAccessedAt, now)+confidenceBoost(m.Memory.Confidence))
			scored = append(scored, ScoredMemory{Memory: m.Memory, Score: composite})
		}
	}

	// Sort by semantic score
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Top K
	result := make([]db.Memory, 0, req.Limit)
	for i := 0; i < len(scored) && i < req.Limit; i++ {
		result = append(result, scored[i].Memory)
	}
	reasons = appendUniqueSearchReason(reasons, MemorySearchReasonVectorUsed)
	if req.Category != "" {
		reasons = appendUniqueSearchReason(reasons, MemorySearchReasonCategoryFiltered)
	}
	return result, reasons, nil
}

func (s *service) fuseResults(fts []db.Memory, vec []db.Memory, limit int) []db.Memory {
	return s.fuseResultsWithLexicalBias(fts, vec, limit, 0)
}

func (s *service) fuseResultsWithLexicalBias(fts []db.Memory, vec []db.Memory, limit int, lexicalBias float64) []db.Memory {
	// RRF Constant k (usually 60)
	k := float64(60)
	scores := make(map[string]float64)
	memoryMap := make(map[string]db.Memory)
	signals := s.usageSignalsByID()

	// Score FTS results
	for rank, m := range fts {
		scores[m.ID] += 1.0 / (k + float64(rank+1))
		if lexicalBias > 0 {
			scores[m.ID] += lexicalBias
		}
		memoryMap[m.ID] = m
	}

	// Score Vector results
	for rank, m := range vec {
		scores[m.ID] += 1.0 / (k + float64(rank+1))
		memoryMap[m.ID] = m
	}

	// Convert to list
	type fusedItem struct {
		ID    string
		Score float64
	}
	var fused []fusedItem
	for id, score := range scores {
		fused = append(fused, fusedItem{ID: id, Score: score})
	}

	// Sort by RRF score
	now := time.Now().Unix()
	sort.Slice(fused, func(i, j int) bool {
		left := fused[i].Score
		right := fused[j].Score
		if signal, ok := signals[fused[i].ID]; ok {
			left += accessBoost(signal.AccessCount) + recencyBoost(signal.LastAccessedAt, now) + confidenceBoost(signal.Confidence)
		}
		if signal, ok := signals[fused[j].ID]; ok {
			right += accessBoost(signal.AccessCount) + recencyBoost(signal.LastAccessedAt, now) + confidenceBoost(signal.Confidence)
		}
		if left != right {
			return left > right
		}
		return memoryMap[fused[i].ID].UpdatedAt > memoryMap[fused[j].ID].UpdatedAt
	})

	// Return Top K
	result := make([]db.Memory, 0, limit)
	for i := 0; i < len(fused) && i < limit; i++ {
		result = append(result, memoryMap[fused[i].ID])
	}
	return result
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float32
	for i := 0; i < len(a); i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

// Phase 1: SearchByCategory implements scoped semantic search
func (s *service) SearchByCategory(ctx context.Context, query string, category string, limit int) ([]db.Memory, error) {
	result, err := s.SearchByCategoryWithPolicy(ctx, DefaultCategorySearchRequest(query, category, limit))
	if err != nil {
		return nil, err
	}
	return result.Memories, nil
}

func (s *service) SearchByCategoryWithPolicy(ctx context.Context, req MemorySearchRequest) (*MemorySearchResult, error) {
	if strings.TrimSpace(req.Category) == "" {
		return nil, fmt.Errorf("category is required for category search")
	}
	return s.searchWithPolicy(ctx, req)
}

func (s *service) searchWithPolicy(ctx context.Context, req MemorySearchRequest) (*MemorySearchResult, error) {
	start := time.Now()
	if req.Category != "" && strings.TrimSpace(req.Stage) == "" {
		req.Stage = "category_search"
	}
	if strings.TrimSpace(req.Stage) == "" {
		req.Stage = "global_search"
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}
	if req.MaxFTSCandidates < req.Limit {
		req.MaxFTSCandidates = req.Limit * 2
	}
	if req.MaxVectorCandidates < req.Limit {
		req.MaxVectorCandidates = req.Limit * 2
	}
	if req.WaitForCache <= 0 {
		req.WaitForCache = 2 * time.Second
	}
	if !req.AllowFTS && !req.AllowVector {
		req.AllowFTS = true
	}
	if req.VectorScoreThreshold <= 0 {
		if req.Category != "" {
			req.VectorScoreThreshold = 0.25
		} else {
			req.VectorScoreThreshold = 0.3
		}
	}

	decision := MemorySearchDecision{
		Contract:          memoryContractVersion,
		Stage:             req.Stage,
		Category:          req.Category,
		Limit:             req.Limit,
		FTSCandidateLimit: req.MaxFTSCandidates,
		VecCandidateLimit: req.MaxVectorCandidates,
	}
	decision.ReasonCodes = appendUniqueSearchReason(decision.ReasonCodes, MemorySearchReasonContractApplied)
	decision.ReasonCodes = appendUniqueSearchReason(decision.ReasonCodes, MemorySearchReasonBudgetApplied)

	exactMatches, exactErr := s.searchExactMatches(ctx, req)
	if exactErr == nil && len(exactMatches) > 0 {
		decision.UsedChannels = append(decision.UsedChannels, "exact")
		decision.FallbackPath = "exact_only"
		decision.ReasonCodes = appendUniqueSearchReason(decision.ReasonCodes, MemorySearchReasonExactMatchUsed)
		if req.Category != "" {
			decision.ReasonCodes = appendUniqueSearchReason(decision.ReasonCodes, MemorySearchReasonCategoryFiltered)
		}
		decision.DurationMs = time.Since(start).Milliseconds()
		return &MemorySearchResult{Memories: exactMatches, Decision: decision}, nil
	}

	var (
		ftsResults []db.Memory
		vecResults []db.Memory
		ftsErr     error
		vecErr     error
		vecReasons []MemorySearchReasonCode
		wg         sync.WaitGroup
	)

	if req.AllowFTS {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ftsCandidates, err := s.searchFTS(ctx, req.Query, req.MaxFTSCandidates)
			if err != nil {
				ftsErr = err
				return
			}
			for _, candidate := range ftsCandidates {
				if req.Category != "" && candidate.Category != req.Category {
					continue
				}
				if req.Category == "" && isDefaultHiddenMemoryCategory(candidate.Category) {
					continue
				}
				ftsResults = append(ftsResults, *s.toMemory(candidate))
			}
		}()
	}

	if req.AllowVector {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vecResults, vecReasons, vecErr = s.searchVectorWithPolicy(ctx, req)
		}()
	} else {
		vecReasons = appendUniqueSearchReason(vecReasons, MemorySearchReasonVectorUnavailable)
	}

	wg.Wait()
	for _, reason := range vecReasons {
		decision.ReasonCodes = appendUniqueSearchReason(decision.ReasonCodes, reason)
	}

	if req.AllowFTS && ftsErr == nil {
		decision.ReasonCodes = appendUniqueSearchReason(decision.ReasonCodes, MemorySearchReasonFTSUsed)
	}

	if ftsErr != nil && vecErr != nil {
		decision.DurationMs = time.Since(start).Milliseconds()
		return nil, fmt.Errorf("both search methods failed: fts=%v, vec=%v", ftsErr, vecErr)
	}

	var result []db.Memory
	switch {
	case len(ftsResults) > 0 && len(vecResults) > 0:
		decision.UsedChannels = append(decision.UsedChannels, "fts", "vector")
		if queryContainsCJK(req.Query) {
			result = s.fuseResultsWithLexicalBias(ftsResults, vecResults, req.Limit, 0.18)
		} else {
			result = s.fuseResults(ftsResults, vecResults, req.Limit)
		}
	case len(vecResults) > 0:
		decision.UsedChannels = append(decision.UsedChannels, "vector")
		if ftsErr != nil || len(ftsResults) == 0 {
			decision.FallbackPath = "vector_only"
			decision.ReasonCodes = appendUniqueSearchReason(decision.ReasonCodes, MemorySearchReasonFTSFallback)
		}
		result = vecResults
	case len(ftsResults) > 0:
		decision.UsedChannels = append(decision.UsedChannels, "fts")
		if vecErr != nil || len(vecResults) == 0 {
			decision.FallbackPath = "fts_only"
		}
		result = ftsResults
	default:
		if req.Category != "" && req.AllowListFallback {
			fallback, err := s.ListByCategory(ctx, req.Category)
			if err != nil {
				decision.DurationMs = time.Since(start).Milliseconds()
				return nil, err
			}
			if len(fallback) > req.Limit {
				fallback = fallback[:req.Limit]
			}
			result = fallback
			if len(result) > 0 {
				decision.FallbackPath = "category_list"
				decision.ReasonCodes = appendUniqueSearchReason(decision.ReasonCodes, MemorySearchReasonListFallback)
			}
		}
	}

	if len(result) == 0 {
		decision.ReasonCodes = appendUniqueSearchReason(decision.ReasonCodes, MemorySearchReasonNoResults)
	}
	if req.Category != "" {
		decision.ReasonCodes = appendUniqueSearchReason(decision.ReasonCodes, MemorySearchReasonCategoryFiltered)
	}
	decision.DurationMs = time.Since(start).Milliseconds()
	return &MemorySearchResult{Memories: result, Decision: decision}, nil
}
