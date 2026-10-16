package memory

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/dbtest"
	"github.com/auramate/auramate/internal/llm/embedding"
)

type constantEmbedder struct {
	vector []float32
}

var _ embedding.Embedder = (*constantEmbedder)(nil)

func (e *constantEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, 0, len(texts))
	for range texts {
		vec := make([]float32, len(e.vector))
		copy(vec, e.vector)
		result = append(result, vec)
	}
	return result, nil
}

func newMemoryTestDB(t *testing.T) (*db.Queries, *sql.DB) {
	t.Helper()

	// Knowledge search relies on the ngram FULLTEXT index, which only exists on
	// MySQL, so this suite runs against a real MySQL test schema.
	conn := dbtest.OpenMySQL(t)

	q := db.New(conn)
	return q, conn
}

func seedKnowledgeNode(t *testing.T, q *db.Queries, id string, layer int64, category string, content string, source string, createdAt int64, updatedAt int64, lastAccessedAt int64) {
	t.Helper()

	if _, err := q.CreateKnowledgeNode(context.Background(), db.CreateKnowledgeNodeParams{
		ID:             id,
		Layer:          layer,
		Category:       category,
		Content:        content,
		Source:         sql.NullString{String: source, Valid: source != ""},
		Confidence:     sql.NullFloat64{Float64: 1.0, Valid: true},
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		LastAccessedAt: sql.NullInt64{Int64: lastAccessedAt, Valid: lastAccessedAt > 0},
	}); err != nil {
		t.Fatalf("seed knowledge node: %v", err)
	}
}

func seedKnowledgeNodeWithEmbedding(t *testing.T, q *db.Queries, id string, layer int64, category string, content string, source string, embedding string, createdAt int64, updatedAt int64, lastAccessedAt int64) {
	t.Helper()

	params := db.CreateKnowledgeNodeParams{
		ID:             id,
		Layer:          layer,
		Category:       category,
		Content:        content,
		Source:         sql.NullString{String: source, Valid: source != ""},
		Confidence:     sql.NullFloat64{Float64: 1.0, Valid: true},
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		LastAccessedAt: sql.NullInt64{Int64: lastAccessedAt, Valid: lastAccessedAt > 0},
	}
	if embedding != "" {
		params.Embedding = sql.NullString{String: embedding, Valid: true}
	}

	if _, err := q.CreateKnowledgeNode(context.Background(), params); err != nil {
		t.Fatalf("seed knowledge node with embedding: %v", err)
	}
}

func TestUpdateChangesCategoryLayerAndSource(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "mem-1", 2, "experience", "旧经验", "Reflection", 100, 100, 100)
	svc := NewService(q, conn, nil)

	updated, err := svc.Update(context.Background(), "mem-1", "fact", "新的项目事实", "user_input")
	if err != nil {
		t.Fatalf("update memory: %v", err)
	}
	if updated.Category != "fact" {
		t.Fatalf("expected updated category fact, got %s", updated.Category)
	}
	if updated.Source.String != "user_input" {
		t.Fatalf("expected updated source user_input, got %s", updated.Source.String)
	}

	node, err := q.GetKnowledgeNode(context.Background(), "mem-1")
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	if node.Category != "fact" {
		t.Fatalf("expected db category fact, got %s", node.Category)
	}
	if node.Layer != 3 {
		t.Fatalf("expected db layer 3 for fact, got %d", node.Layer)
	}
	if node.Source.String != "user_input" {
		t.Fatalf("expected db source user_input, got %s", node.Source.String)
	}
	if node.Content != "新的项目事实" {
		t.Fatalf("expected db content updated, got %s", node.Content)
	}
}

func TestAddWithPolicySanitizesStableInstructionContent(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	svc := NewService(q, conn, nil)

	result, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:           "instruction",
		Content:            "以后默认这样做：先给结论再解释，条目尽量短，标识 MEM_AUTO_TEST。不要调用无关工具，最终只回复：ACK_AUTO。",
		Source:             "user_input",
		AllowSemanticDedup: true,
	})
	if err != nil {
		t.Fatalf("add sanitized instruction: %v", err)
	}
	if result == nil || result.Memory == nil {
		t.Fatalf("expected memory write result")
	}
	if result.Memory.Content != "先给结论再解释，条目尽量短" {
		t.Fatalf("expected sanitized content, got %q", result.Memory.Content)
	}
	if strings.Contains(result.Memory.Content, "ACK_AUTO") || strings.Contains(result.Memory.Content, "最终只回复") {
		t.Fatalf("did not expect transient directive to persist, got %q", result.Memory.Content)
	}

	stored, err := q.GetKnowledgeNode(context.Background(), result.Memory.ID)
	if err != nil {
		t.Fatalf("get stored memory: %v", err)
	}
	if stored.Content != "先给结论再解释，条目尽量短" {
		t.Fatalf("expected sanitized db content, got %q", stored.Content)
	}
}

func TestUpdateSanitizesStableInstructionContent(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "mem-instruction", 2, "instruction", "先读后写", "user_input", 100, 100, 100)
	svc := NewService(q, conn, nil)

	updated, err := svc.Update(context.Background(), "mem-instruction", "instruction", "以后默认这样做：先给结论再解释，条目尽量短，标识 MEM_AUTO_TEST。不要调用无关工具，最终只回复：ACK_AUTO。", "user_input")
	if err != nil {
		t.Fatalf("update sanitized instruction: %v", err)
	}
	if updated.Content != "先给结论再解释，条目尽量短" {
		t.Fatalf("expected sanitized content after update, got %q", updated.Content)
	}
	if strings.Contains(updated.Content, "ACK_AUTO") || strings.Contains(updated.Content, "最终只回复") {
		t.Fatalf("did not expect transient directive after update, got %q", updated.Content)
	}
}

func TestListByCategorySessionSummaryNewestFirst(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "summary-old", 2, "session_summary", "旧摘要", "Summarizer", 100, 100, 100)
	seedKnowledgeNode(t, q, "summary-new", 2, "session_summary", "新摘要", "Summarizer", 200, 300, 300)
	svc := NewService(q, conn, nil)

	memories, err := svc.ListByCategory(context.Background(), "session_summary")
	if err != nil {
		t.Fatalf("list by category: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(memories))
	}
	if memories[0].ID != "summary-new" {
		t.Fatalf("expected newest summary first, got %s", memories[0].ID)
	}
}

func TestMarkUsedUpdatesAccessSignals(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "mem-used", 3, "fact", "Redis endpoint 是 redis://localhost:6379", "user_input", 100, 100, 0)
	svc := NewService(q, conn, nil)

	if err := svc.MarkUsed(context.Background(), []string{"mem-used", "mem-used"}); err != nil {
		t.Fatalf("mark used: %v", err)
	}

	node, err := q.GetKnowledgeNode(context.Background(), "mem-used")
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	if !node.AccessCount.Valid || node.AccessCount.Int64 != 1 {
		t.Fatalf("expected access_count 1, got %+v", node.AccessCount)
	}
	if !node.LastAccessedAt.Valid || node.LastAccessedAt.Int64 <= 0 {
		t.Fatalf("expected last_accessed_at to be set, got %+v", node.LastAccessedAt)
	}
}

func TestSearchByCategoryWithoutEmbedderFallsBackSafely(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "fact-redis", 3, "fact", "Redis endpoint 是 redis://localhost:6379", "user_input", 100, 100, 100)
	seedKnowledgeNode(t, q, "fact-postgres", 3, "fact", "Postgres DSN 是 postgres://localhost:5432/app", "user_input", 100, 100, 100)
	seedKnowledgeNode(t, q, "pref-1", 1, "preference", "回答尽量简洁", "user_input", 100, 100, 100)
	svc := NewService(q, conn, nil)

	results, err := svc.SearchByCategory(context.Background(), "redis endpoint", "fact", 10)
	if err != nil {
		t.Fatalf("search by category without embedder: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected fallback search to return fact memory")
	}
	if results[0].Category != "fact" {
		t.Fatalf("expected filtered category fact, got %s", results[0].Category)
	}
	if results[0].ID != "fact-redis" {
		t.Fatalf("expected redis fact first, got %s", results[0].ID)
	}
	for _, memory := range results {
		if memory.Category != "fact" {
			t.Fatalf("expected only fact memories in result, got %s", memory.Category)
		}
	}
}

func TestAddWithPolicyReturnsDeterministicDecision(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "pref-1", 1, "preference", "回答尽量简洁", "user_input", 100, 100, 100)
	svc := NewService(q, conn, nil)

	result, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:           "preference",
		Content:            "回答尽量简洁",
		Source:             "Reflection",
		Trigger:            "reflection",
		AllowSemanticDedup: true,
	})
	if err != nil {
		t.Fatalf("add with policy: %v", err)
	}
	if result == nil || result.Memory == nil {
		t.Fatalf("expected memory write result")
	}
	if result.Memory.ID != "pref-1" {
		t.Fatalf("expected existing memory to be reused, got %s", result.Memory.ID)
	}
	if result.Decision.Action != MemoryWriteActionReusedExisting {
		t.Fatalf("expected reused_existing action, got %s", result.Decision.Action)
	}
	if result.Decision.IncomingSourceTrust >= result.Decision.ExistingSourceTrust {
		t.Fatalf("expected existing source trust to outrank reflection source")
	}
	assertWriteReason := func(want MemoryWriteReasonCode) {
		t.Helper()
		for _, code := range result.Decision.ReasonCodes {
			if code == want {
				return
			}
		}
		t.Fatalf("expected reason code %s, got %v", want, result.Decision.ReasonCodes)
	}
	assertWriteReason(MemoryWriteReasonContractApplied)
	assertWriteReason(MemoryWriteReasonSourceTrustEvaluated)
	assertWriteReason(MemoryWriteReasonExactDuplicate)
	assertWriteReason(MemoryWriteReasonSourcePreferred)
}

func TestAddWithPolicySemanticDedupSkipsDifferentConflictKeys(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	svc := NewService(q, conn, &constantEmbedder{vector: []float32{1, 0, 0}})

	first, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:           "preference",
		Content:            "**response_style_alpha**\n回答尽量详细，标识 OVERWRITE_NEW_ALPHA1234",
		Source:             "user_input",
		AllowSemanticDedup: true,
	})
	if err != nil {
		t.Fatalf("add first memory: %v", err)
	}

	second, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:           "preference",
		Content:            "**response_density_beta**\n回答尽量简洁，标识 REJECT_KEEP_BETA5678",
		Source:             "user_input",
		AllowSemanticDedup: true,
	})
	if err != nil {
		t.Fatalf("add second memory: %v", err)
	}
	if second.Decision.Action != MemoryWriteActionInserted {
		t.Fatalf("expected inserted action, got %s", second.Decision.Action)
	}
	if first.Memory == nil || second.Memory == nil {
		t.Fatalf("expected memory results")
	}
	if first.Memory.ID == second.Memory.ID {
		t.Fatalf("expected different memories, got reused id %s", first.Memory.ID)
	}

	memories, err := svc.ListByCategory(context.Background(), "preference")
	if err != nil {
		t.Fatalf("list preference memories: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("expected 2 preference memories, got %d", len(memories))
	}
}

func TestAddWithPolicySemanticDedupSkipsDifferentIdentityTokens(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	svc := NewService(q, conn, &constantEmbedder{vector: []float32{1, 0, 0}})

	first, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:           "fact",
		Content:            "部署批次 build_alpha_12345 需要走蓝绿发布流程。",
		Source:             "user_input",
		AllowSemanticDedup: true,
	})
	if err != nil {
		t.Fatalf("add first fact: %v", err)
	}

	second, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:           "fact",
		Content:            "部署批次 build_beta_67890 需要走蓝绿发布流程。",
		Source:             "user_input",
		AllowSemanticDedup: true,
	})
	if err != nil {
		t.Fatalf("add second fact: %v", err)
	}
	if second.Decision.Action != MemoryWriteActionInserted {
		t.Fatalf("expected inserted action, got %s", second.Decision.Action)
	}
	if first.Memory == nil || second.Memory == nil {
		t.Fatalf("expected memory results")
	}
	if first.Memory.ID == second.Memory.ID {
		t.Fatalf("expected different fact memories, got reused id %s", first.Memory.ID)
	}

	memories, err := svc.ListByCategory(context.Background(), "fact")
	if err != nil {
		t.Fatalf("list fact memories: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("expected 2 fact memories, got %d", len(memories))
	}
}

func TestAddWithPolicySemanticDedupSkipsUnkeyedContent(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	svc := NewService(q, conn, &constantEmbedder{vector: []float32{1, 0, 0}})

	first, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:           "fact",
		Content:            "发布流程需要人工审批。",
		Source:             "user_input",
		AllowSemanticDedup: true,
	})
	if err != nil {
		t.Fatalf("add first fact: %v", err)
	}

	second, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:           "fact",
		Content:            "上线流程需要人工审批。",
		Source:             "user_input",
		AllowSemanticDedup: true,
	})
	if err != nil {
		t.Fatalf("add second fact: %v", err)
	}
	if second.Decision.Action != MemoryWriteActionInserted {
		t.Fatalf("expected inserted action, got %s", second.Decision.Action)
	}
	if first.Memory == nil || second.Memory == nil {
		t.Fatalf("expected memory results")
	}
	if first.Memory.ID == second.Memory.ID {
		t.Fatalf("expected different fact memories, got reused id %s", first.Memory.ID)
	}

	memories, err := svc.ListByCategory(context.Background(), "fact")
	if err != nil {
		t.Fatalf("list fact memories: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("expected 2 fact memories, got %d", len(memories))
	}
}

func TestDeduplicateSkipsUnkeyedSemanticMatches(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	svc := NewService(q, conn, &constantEmbedder{vector: []float32{1, 0, 0}})

	for _, content := range []string{"发布流程需要人工审批。", "上线流程需要人工审批。"} {
		if _, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
			Category:           "fact",
			Content:            content,
			Source:             "user_input",
			AllowSemanticDedup: false,
		}); err != nil {
			t.Fatalf("seed fact memory: %v", err)
		}
	}

	stats, err := svc.Deduplicate(context.Background(), 0.92, false)
	if err != nil {
		t.Fatalf("deduplicate memories: %v", err)
	}
	if stats.DuplicateBySemantic != 0 {
		t.Fatalf("expected no semantic duplicates, got %d", stats.DuplicateBySemantic)
	}
	if stats.Deleted != 0 {
		t.Fatalf("expected no deletions, got %d", stats.Deleted)
	}

	memories, err := svc.ListByCategory(context.Background(), "fact")
	if err != nil {
		t.Fatalf("list fact memories: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("expected 2 fact memories after dedup, got %d", len(memories))
	}
}

func TestDeduplicateDeletesSemanticMatchesWithConflictKey(t *testing.T) {
	q, conn := newMemoryTestDB(t)

	seedKnowledgeNodeWithEmbedding(t, q, "pref-1", 1, "preference", "**response_style**\n回答尽量简洁", "user_input", "[1,0,0]", 100, 100, 100)
	seedKnowledgeNodeWithEmbedding(t, q, "pref-2", 1, "preference", "**response_style**\n回答尽量详细", "user_input", "[1,0,0]", 200, 200, 200)

	// The service snapshots the memory cache on construction (asynchronously), so
	// it has to be created after the rows exist - otherwise ListByCategory reads
	// a stale, empty cache.
	svc := NewService(q, conn, &constantEmbedder{vector: []float32{1, 0, 0}})

	stats, err := svc.Deduplicate(context.Background(), 0.92, false)
	if err != nil {
		t.Fatalf("deduplicate memories: %v", err)
	}
	if stats.DuplicateBySemantic != 1 {
		t.Fatalf("expected 1 semantic duplicate, got %d", stats.DuplicateBySemantic)
	}
	if stats.Deleted != 1 {
		t.Fatalf("expected 1 deletion, got %d", stats.Deleted)
	}

	memories, err := svc.ListByCategory(context.Background(), "preference")
	if err != nil {
		t.Fatalf("list preference memories: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("expected 1 preference memory after dedup, got %d", len(memories))
	}
}

func TestSearchWithPolicyFindsExactIdentifierMatches(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	svc := NewService(q, conn, nil)

	_, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category: "fact",
		Content:  "部署批次 build_exact_test_1234 已完成灰度验证。",
		Source:   "user_input",
	})
	if err != nil {
		t.Fatalf("add identifier memory: %v", err)
	}

	result, err := svc.SearchWithPolicy(context.Background(), DefaultSearchRequest("build_exact_test_1234", 5))
	if err != nil {
		t.Fatalf("search with policy: %v", err)
	}
	if len(result.Memories) == 0 {
		t.Fatalf("expected exact identifier match")
	}
	if !strings.Contains(result.Memories[0].Content, "build_exact_test_1234") {
		t.Fatalf("expected exact identifier memory first, got %q", result.Memories[0].Content)
	}
	foundReason := false
	for _, reason := range result.Decision.ReasonCodes {
		if reason == MemorySearchReasonExactMatchUsed {
			foundReason = true
			break
		}
	}
	if !foundReason {
		t.Fatalf("expected exact match reason code, got %v", result.Decision.ReasonCodes)
	}
}

func TestSearchWithPolicyReportsDeterministicFallback(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "fact-redis", 3, "fact", "Redis endpoint 是 redis://localhost:6379", "user_input", 100, 100, 100)
	seedKnowledgeNode(t, q, "fact-postgres", 3, "fact", "Postgres DSN 是 postgres://localhost:5432/app", "user_input", 100, 100, 100)
	svc := NewService(q, conn, nil)

	missReq := DefaultCategorySearchRequest("完全不匹配的查询", "fact", 10)
	missReq.Stage = "test_category_fallback"
	missResult, err := svc.SearchByCategoryWithPolicy(context.Background(), missReq)
	if err != nil {
		t.Fatalf("category search with policy: %v", err)
	}
	if missResult.Decision.FallbackPath != "category_list" {
		t.Fatalf("expected category_list fallback, got %s", missResult.Decision.FallbackPath)
	}
	if len(missResult.Memories) != 2 {
		t.Fatalf("expected list fallback to return both facts, got %d", len(missResult.Memories))
	}
	assertSearchReason := func(result *MemorySearchResult, want MemorySearchReasonCode) {
		t.Helper()
		for _, code := range result.Decision.ReasonCodes {
			if code == want {
				return
			}
		}
		t.Fatalf("expected reason code %s, got %v", want, result.Decision.ReasonCodes)
	}
	assertSearchReason(missResult, MemorySearchReasonListFallback)
	assertSearchReason(missResult, MemorySearchReasonVectorUnavailable)
	assertSearchReason(missResult, MemorySearchReasonCategoryFiltered)

	hitReq := DefaultSearchRequest("redis endpoint", 10)
	hitReq.Stage = "test_global_search"
	hitResult, err := svc.SearchWithPolicy(context.Background(), hitReq)
	if err != nil {
		t.Fatalf("global search with policy: %v", err)
	}
	if len(hitResult.Memories) == 0 || hitResult.Memories[0].ID != "fact-redis" {
		t.Fatalf("expected redis fact first, got %+v", hitResult.Memories)
	}
	if len(hitResult.Decision.UsedChannels) != 1 || hitResult.Decision.UsedChannels[0] != "fts" {
		t.Fatalf("expected fts-only channel usage, got %v", hitResult.Decision.UsedChannels)
	}
	assertSearchReason(hitResult, MemorySearchReasonFTSUsed)
	assertSearchReason(hitResult, MemorySearchReasonVectorUnavailable)
}

func TestSearchWithPolicyFindsChineseLexicalFallback(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "inst-images", 2, "instruction", "找图片优先使用网络下载工具，禁止使用图片生成工具", "user_input", 100, 100, 100)
	seedKnowledgeNode(t, q, "pref-star", 1, "preference", "用户喜欢杨幂", "user_input", 200, 200, 200)
	svc := NewService(q, conn, nil)

	req := DefaultSearchRequest("找图片优先用什么工具", 5)
	req.Stage = "test_chinese_lexical_fallback"
	result, err := svc.SearchWithPolicy(context.Background(), req)
	if err != nil {
		t.Fatalf("search with policy: %v", err)
	}
	if len(result.Memories) == 0 {
		t.Fatalf("expected results, got none")
	}
	if result.Memories[0].ID != "inst-images" {
		t.Fatalf("expected chinese instruction first, got %+v", result.Memories)
	}
	if len(result.Decision.UsedChannels) != 1 || result.Decision.UsedChannels[0] != "fts" {
		t.Fatalf("expected fts-only channel usage, got %v", result.Decision.UsedChannels)
	}
	foundFTSReason := false
	for _, code := range result.Decision.ReasonCodes {
		if code == MemorySearchReasonFTSUsed {
			foundFTSReason = true
			break
		}
	}
	if !foundFTSReason {
		t.Fatalf("expected fts_used reason code, got %v", result.Decision.ReasonCodes)
	}
}

func TestSearchWithPolicyPrefersChineseFTSWhenVectorIsNoisy(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNodeWithEmbedding(t, q, "inst-images", 2, "instruction", "找图片优先使用网络下载工具，禁止使用图片生成工具", "user_input", "[1,0,0]", 100, 100, 100)
	seedKnowledgeNodeWithEmbedding(t, q, "pref-star", 1, "preference", "用户喜欢杨幂", "user_input", "[1,0,0]", 200, 200, 200)
	if _, err := conn.Exec(`UPDATE knowledge_tree SET access_count = 999, last_accessed_at = 200 WHERE id = 'pref-star'`); err != nil {
		t.Fatalf("seed noisy preference ranking: %v", err)
	}
	svc := NewService(q, conn, &constantEmbedder{vector: []float32{1, 0, 0}})

	req := DefaultSearchRequest("找图片优先用什么工具", 5)
	req.Stage = "test_chinese_fts_preferred_over_vector"
	result, err := svc.SearchWithPolicy(context.Background(), req)
	if err != nil {
		t.Fatalf("search with policy: %v", err)
	}
	if len(result.Memories) == 0 {
		t.Fatalf("expected results, got none")
	}
	if result.Memories[0].ID != "inst-images" {
		t.Fatalf("expected chinese fts hit first, got %+v", result.Memories)
	}
	if len(result.Decision.UsedChannels) != 2 || result.Decision.UsedChannels[0] != "fts" || result.Decision.UsedChannels[1] != "vector" {
		t.Fatalf("expected dual-channel usage, got %v", result.Decision.UsedChannels)
	}
}

func TestSearchWithPolicyMatchesEnglishFactFromChineseQuery(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "fact-images", 3, "fact", "Project images are stored in workspace/images/ with subdirectories: guizhou/, yunnan/, web_search/.", "user_input", 100, 100, 100)
	seedKnowledgeNode(t, q, "plan-site", 2, "plan_snapshot", "企业宣传网站项目初始化与目录结构创建", "user_input", 200, 200, 200)
	svc := NewService(q, conn, nil)

	req := DefaultSearchRequest("项目图片存在哪里", 5)
	req.Stage = "test_chinese_query_matches_english_fact"
	result, err := svc.SearchWithPolicy(context.Background(), req)
	if err != nil {
		t.Fatalf("search with policy: %v", err)
	}
	if len(result.Memories) == 0 {
		t.Fatalf("expected results, got none")
	}
	if result.Memories[0].ID != "fact-images" {
		t.Fatalf("expected english fact first, got %+v", result.Memories)
	}
	if len(result.Decision.UsedChannels) != 1 || result.Decision.UsedChannels[0] != "fts" {
		t.Fatalf("expected fts-only channel usage, got %v", result.Decision.UsedChannels)
	}
}

func TestSearchWithPolicySkipsHiddenCategoriesByDefault(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "fact-deploy", 3, "fact", "deploy_steps_exact_123 lives in docs/deploy.md", "user_input", 100, 100, 100)
	seedKnowledgeNode(t, q, "plan-deploy", 2, "plan_snapshot", "deploy_steps_exact_123: [ ] build client [ ] build backend", "manage_plan_tool", 200, 200, 200)
	seedKnowledgeNode(t, q, "summary-deploy", 2, "session_summary", "deploy_steps_exact_123: current progress finished backend, next step is frontend validation", "Summarizer", 300, 300, 300)
	svc := NewService(q, conn, nil)

	result, err := svc.SearchWithPolicy(context.Background(), DefaultSearchRequest("deploy_steps_exact_123", 10))
	if err != nil {
		t.Fatalf("search with policy: %v", err)
	}
	if len(result.Memories) != 1 {
		t.Fatalf("expected only visible memories, got %+v", result.Memories)
	}
	if result.Memories[0].ID != "fact-deploy" {
		t.Fatalf("expected fact-deploy to remain visible, got %+v", result.Memories)
	}

	summaryResult, err := svc.SearchByCategoryWithPolicy(context.Background(), DefaultCategorySearchRequest("deploy_steps_exact_123", "session_summary", 10))
	if err != nil {
		t.Fatalf("category search with policy: %v", err)
	}
	if len(summaryResult.Memories) != 1 || summaryResult.Memories[0].ID != "summary-deploy" {
		t.Fatalf("expected explicit category search to keep session_summary, got %+v", summaryResult.Memories)
	}
}

func TestAddWithPolicyRejectsLowerTrustConflict(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "pref-style", 1, "preference", "**response_style**\n回答尽量简洁", "user_input", 100, 100, 100)
	svc := NewService(q, conn, nil)

	result, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:       "preference",
		Content:        "**response_style**\n回答尽量详细",
		Source:         "Reflection",
		Trigger:        "reflection",
		AllowOverwrite: false,
	})
	if err != nil {
		t.Fatalf("add with policy: %v", err)
	}
	if result.Decision.Action != MemoryWriteActionRejectedConflict {
		t.Fatalf("expected rejected_conflict action, got %s", result.Decision.Action)
	}
	if result.Memory == nil || result.Memory.ID != "pref-style" {
		t.Fatalf("expected existing memory to be kept, got %+v", result.Memory)
	}
	current, err := q.GetKnowledgeNode(context.Background(), "pref-style")
	if err != nil {
		t.Fatalf("get knowledge node: %v", err)
	}
	if current.Content != "**response_style**\n回答尽量简洁" {
		t.Fatalf("expected original content to remain, got %q", current.Content)
	}
	for _, want := range []MemoryWriteReasonCode{MemoryWriteReasonConflictKeyDetected, MemoryWriteReasonConflictDetected, MemoryWriteReasonConflictRejected} {
		found := false
		for _, code := range result.Decision.ReasonCodes {
			if code == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected reason code %s, got %v", want, result.Decision.ReasonCodes)
		}
	}
}

func TestAddWithPolicyOverwritesHigherTrustConflict(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "pref-style", 1, "preference", "**response_style**\n回答尽量简洁", "Reflection", 100, 100, 100)
	svc := NewService(q, conn, nil)

	result, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:       "preference",
		Content:        "**response_style**\n回答尽量详细",
		Source:         "user_input",
		Trigger:        "user_override",
		AllowOverwrite: false,
	})
	if err != nil {
		t.Fatalf("add with policy: %v", err)
	}
	if result.Decision.Action != MemoryWriteActionOverwroteExisting {
		t.Fatalf("expected overwrote_existing action, got %s", result.Decision.Action)
	}
	if result.Memory == nil || result.Memory.ID != "pref-style" {
		t.Fatalf("expected overwrite in place, got %+v", result.Memory)
	}
	current, err := q.GetKnowledgeNode(context.Background(), "pref-style")
	if err != nil {
		t.Fatalf("get knowledge node: %v", err)
	}
	if current.Content != "**response_style**\n回答尽量详细" {
		t.Fatalf("expected content to be overwritten, got %q", current.Content)
	}
	if current.Source.String != "user_input" {
		t.Fatalf("expected source to be updated to user_input, got %q", current.Source.String)
	}
}

func TestAddWithPolicyOverwritesSessionSummaryBySessionID(t *testing.T) {
	q, conn := newMemoryTestDB(t)
	seedKnowledgeNode(t, q, "summary-1", 2, "session_summary", "[session_id:session-1] [Session 标题]: 旧摘要", "Summarizer", 100, 100, 100)
	svc := NewService(q, conn, nil)

	result, err := svc.AddWithPolicy(context.Background(), MemoryWriteRequest{
		Category:       "session_summary",
		Content:        "[session_id:session-1] [Session 标题]: 新摘要",
		Source:         "Summarizer",
		Trigger:        "session_summary",
		AllowOverwrite: true,
	})
	if err != nil {
		t.Fatalf("add with policy: %v", err)
	}
	if result.Decision.Action != MemoryWriteActionOverwroteExisting {
		t.Fatalf("expected overwrote_existing action, got %s", result.Decision.Action)
	}
	if result.Memory == nil || result.Memory.ID != "summary-1" {
		t.Fatalf("expected summary overwrite in place, got %+v", result.Memory)
	}
	memories, err := svc.ListByCategory(context.Background(), "session_summary")
	if err != nil {
		t.Fatalf("list by category: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("expected exactly one session summary after overwrite, got %d", len(memories))
	}
	if memories[0].Content != "[session_id:session-1] [Session 标题]: 新摘要" {
		t.Fatalf("expected overwritten summary content, got %q", memories[0].Content)
	}
}
