package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/dbtest"
)

// TestManualQueriesAgainstMySQL covers the statements that live in
// queries_manual.go - the ones MySQL could not express with RETURNING. They are
// read-back implementations, so they need to be exercised against a real server
// rather than the in-memory SQLite fixtures.
func TestManualQueriesAgainstMySQL(t *testing.T) {
	conn := dbtest.OpenMySQL(t)
	q := db.New(conn)
	ctx := context.Background()

	// Sessions: insert, update and the token aggregation.
	session, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:               "sess-1",
		ParentSessionID:  sql.NullString{},
		Title:            "集成测试",
		MessageCount:     0,
		PromptTokens:     11,
		CompletionTokens: 7,
		Cost:             0.5,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.ID != "sess-1" || session.Title != "集成测试" {
		t.Fatalf("unexpected session after insert: %+v", session)
	}
	if session.CreatedAt <= 0 || session.UpdatedAt <= 0 {
		t.Fatalf("expected created_at/updated_at to be populated, got %+v", session)
	}

	updated, err := q.UpdateSession(ctx, db.UpdateSessionParams{
		Title:            "集成测试 2",
		PromptTokens:     21,
		CompletionTokens: 13,
		SummaryMessageID: sql.NullString{String: "", Valid: false},
		Cost:             1.25,
		ID:               "sess-1",
	})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if updated.Title != "集成测试 2" || updated.PromptTokens != 21 {
		t.Fatalf("unexpected session after update: %+v", updated)
	}

	// Messages: the MySQL trigger must bump sessions.message_count.
	message, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID:        "msg-1",
		SessionID: "sess-1",
		Role:      "user",
		Parts:     "[{\"text\":\"hi\"}]",
		Model:     sql.NullString{String: "test-model", Valid: true},
		Embedding: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if message.ID != "msg-1" || message.CreatedAt <= 0 {
		t.Fatalf("unexpected message after insert: %+v", message)
	}

	afterInsert, err := q.GetSessionByID(ctx, "sess-1")
	if err != nil {
		t.Fatalf("GetSessionByID: %v", err)
	}
	if afterInsert.MessageCount != 1 {
		t.Fatalf("expected trigger to raise message_count to 1, got %d", afterInsert.MessageCount)
	}

	if err := q.UpdateMessage(ctx, db.UpdateMessageParams{
		Parts:      "[{\"text\":\"hi again\"}]",
		FinishedAt: sql.NullInt64{Int64: 12345, Valid: true},
		Embedding:  sql.NullString{},
		ID:         "msg-1",
	}); err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	refetched, err := q.GetMessage(ctx, "msg-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if refetched.Parts != "[{\"text\":\"hi again\"}]" || !refetched.FinishedAt.Valid {
		t.Fatalf("unexpected message after update: %+v", refetched)
	}

	if err := q.DeleteMessage(ctx, "msg-1"); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	afterDelete, err := q.GetSessionByID(ctx, "sess-1")
	if err != nil {
		t.Fatalf("GetSessionByID after delete: %v", err)
	}
	if afterDelete.MessageCount != 0 {
		t.Fatalf("expected trigger to lower message_count to 0, got %d", afterDelete.MessageCount)
	}

	stats, err := q.GetTokenStats(ctx)
	if err != nil {
		t.Fatalf("GetTokenStats: %v", err)
	}
	if stats.InputTokens != 21 || stats.OutputTokens != 13 {
		t.Fatalf("unexpected token stats: %+v", stats)
	}

	// Files: insert, update, list.
	file, err := q.CreateFile(ctx, db.CreateFileParams{
		ID:        "file-1",
		SessionID: "sess-1",
		Path:      `internal\db\connect.go`,
		Content:   "package db",
		Version:   "v1",
	})
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if file.Path != `internal\db\connect.go` {
		t.Fatalf("unexpected file after insert: %+v", file)
	}
	file, err = q.UpdateFile(ctx, db.UpdateFileParams{
		Content: "package db // updated",
		Version: "v2",
		ID:      "file-1",
	})
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if file.Version != "v2" || file.Content != "package db // updated" {
		t.Fatalf("unexpected file after update: %+v", file)
	}
	files, err := q.ListFilesBySession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("ListFilesBySession: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	// Memories: create, update, category filter.
	memory, err := q.CreateMemory(ctx, db.CreateMemoryParams{
		ID:        "mem-1",
		Category:  "preference",
		Content:   "回答尽量简洁",
		Source:    sql.NullString{String: "user_input", Valid: true},
		Embedding: sql.NullString{},
		CreatedAt: 100,
		UpdatedAt: 100,
	})
	if err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	if memory.ID != "mem-1" || memory.Content != "回答尽量简洁" {
		t.Fatalf("unexpected memory after insert: %+v", memory)
	}
	memory, err = q.UpdateMemory(ctx, db.UpdateMemoryParams{
		Category:  "fact",
		Content:   "Redis endpoint 是 redis://localhost:6379",
		Source:    sql.NullString{String: "user_input", Valid: true},
		Embedding: sql.NullString{},
		UpdatedAt: 200,
		ID:        "mem-1",
	})
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	if memory.Category != "fact" || memory.UpdatedAt != 200 {
		t.Fatalf("unexpected memory after update: %+v", memory)
	}
	facts, err := q.ListMemoriesByCategory(ctx, "fact")
	if err != nil {
		t.Fatalf("ListMemoriesByCategory: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact memory, got %d", len(facts))
	}

	// Scheduled tasks.
	task, err := q.CreateScheduledTask(ctx, db.CreateScheduledTaskParams{
		ID:                 "task-1",
		TaskType:           "daily_info",
		Parameters:         "{}",
		ScheduleExpression: sql.NullString{String: "0 9 * * *", Valid: true},
		LastRunAt:          sql.NullInt64{},
		NextRunAt:          sql.NullInt64{Int64: 500, Valid: true},
		IsActive:           sql.NullBool{Bool: true, Valid: true},
		CreatedAt:          100,
	})
	if err != nil {
		t.Fatalf("CreateScheduledTask: %v", err)
	}
	if task.TaskType != "daily_info" || !task.IsActive.Valid || !task.IsActive.Bool {
		t.Fatalf("unexpected scheduled task after insert: %+v", task)
	}
	task, err = q.UpdateScheduledTask(ctx, db.UpdateScheduledTaskParams{
		TaskType:           "daily_info",
		Parameters:         "{\"limit\":10}",
		ScheduleExpression: sql.NullString{String: "0 10 * * *", Valid: true},
		LastRunAt:          sql.NullInt64{Int64: 400, Valid: true},
		NextRunAt:          sql.NullInt64{Int64: 600, Valid: true},
		IsActive:           sql.NullBool{Bool: false, Valid: true},
		ID:                 "task-1",
	})
	if err != nil {
		t.Fatalf("UpdateScheduledTask: %v", err)
	}
	if task.Parameters != "{\"limit\":10}" || task.IsActive.Bool {
		t.Fatalf("unexpected scheduled task after update: %+v", task)
	}
	active, err := q.ListActiveScheduledTasks(ctx)
	if err != nil {
		t.Fatalf("ListActiveScheduledTasks: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected no active tasks, got %d", len(active))
	}

	// Knowledge nodes plus the dialect neutral upsert for graph edges.
	node, err := q.CreateKnowledgeNode(ctx, db.CreateKnowledgeNodeParams{
		ID:             "know-1",
		ParentID:       sql.NullString{},
		Layer:          3,
		Category:       "fact",
		Content:        "Redis endpoint 是 redis://localhost:6379",
		Summary:        sql.NullString{},
		Keywords:       sql.NullString{String: "redis endpoint", Valid: true},
		Confidence:     sql.NullFloat64{Float64: 0.9, Valid: true},
		Source:         sql.NullString{String: "user_input", Valid: true},
		Embedding:      sql.NullString{String: "[1,0,0]", Valid: true},
		CreatedAt:      100,
		UpdatedAt:      100,
		LastAccessedAt: sql.NullInt64{Int64: 100, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeNode: %v", err)
	}
	if node.ID != "know-1" || node.Keywords.String != "redis endpoint" {
		t.Fatalf("unexpected knowledge node after insert: %+v", node)
	}

	node, err = q.UpdateKnowledgeNode(ctx, db.UpdateKnowledgeNodeParams{
		Content:        sql.NullString{String: "Redis endpoint 是 redis://cache:6379", Valid: true},
		Summary:        sql.NullString{},
		Confidence:     sql.NullFloat64{Float64: 0.95, Valid: true},
		AccessCount:    sql.NullInt64{Int64: 3, Valid: true},
		LastAccessedAt: sql.NullInt64{Int64: 300, Valid: true},
		UpdatedAt:      300,
		ID:             "know-1",
	})
	if err != nil {
		t.Fatalf("UpdateKnowledgeNode: %v", err)
	}
	if node.Content != "Redis endpoint 是 redis://cache:6379" || node.AccessCount.Int64 != 3 {
		t.Fatalf("unexpected knowledge node after update: %+v", node)
	}

	second, err := q.CreateKnowledgeNode(ctx, db.CreateKnowledgeNodeParams{
		ID:             "know-2",
		Layer:          3,
		Category:       "fact",
		Content:        "Postgres DSN 是 postgres://localhost:5432/app",
		Keywords:       sql.NullString{String: "postgres dsn", Valid: true},
		Source:         sql.NullString{String: "user_input", Valid: true},
		Confidence:     sql.NullFloat64{Float64: 0.8, Valid: true},
		Embedding:      sql.NullString{String: "[0,1,0]", Valid: true},
		CreatedAt:      200,
		UpdatedAt:      200,
		LastAccessedAt: sql.NullInt64{Int64: 200, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeNode (second): %v", err)
	}
	if second.ID != "know-2" {
		t.Fatalf("unexpected second knowledge node: %+v", second)
	}

	edge := db.CreateKnowledgeEdgeParams{
		SourceID:  "know-1",
		TargetID:  "know-2",
		Relation:  "related_to",
		Weight:    sql.NullFloat64{Float64: 0.4, Valid: true},
		CreatedAt: 100,
	}
	if err := q.CreateKnowledgeEdge(ctx, edge); err != nil {
		t.Fatalf("CreateKnowledgeEdge (insert): %v", err)
	}
	edge.Weight = sql.NullFloat64{Float64: 0.9, Valid: true}
	if err := q.CreateKnowledgeEdge(ctx, edge); err != nil {
		t.Fatalf("CreateKnowledgeEdge (update path): %v", err)
	}
	edges, err := q.GetKnowledgeEdges(ctx, "know-1")
	if err != nil {
		t.Fatalf("GetKnowledgeEdges: %v", err)
	}
	if len(edges) != 1 || edges[0].Weight.Float64 != 0.9 {
		t.Fatalf("expected the upsert to refresh the edge weight, got %+v", edges)
	}

	// Cognitive state: the upsert has to insert once and update after that.
	state, err := q.SetCognitiveState(ctx, db.SetCognitiveStateParams{
		Key:       "emotion",
		Value:     sql.NullString{String: `{"happiness":0.5}`, Valid: true},
		UpdatedAt: 100,
	})
	if err != nil {
		t.Fatalf("SetCognitiveState (insert): %v", err)
	}
	if !state.Value.Valid || state.UpdatedAt != 100 {
		t.Fatalf("unexpected state after insert: %+v", state)
	}
	state, err = q.SetCognitiveState(ctx, db.SetCognitiveStateParams{
		Key:       "emotion",
		Value:     sql.NullString{String: `{"happiness":0.9}`, Valid: true},
		UpdatedAt: 200,
	})
	if err != nil {
		t.Fatalf("SetCognitiveState (update): %v", err)
	}
	if state.Value.String != `{"happiness":0.9}` || state.UpdatedAt != 200 {
		t.Fatalf("expected the upsert to overwrite the value, got %+v", state)
	}
	states, err := q.ListAllCognitiveStates(ctx)
	if err != nil {
		t.Fatalf("ListAllCognitiveStates: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected a single cognitive state row, got %d", len(states))
	}
}
