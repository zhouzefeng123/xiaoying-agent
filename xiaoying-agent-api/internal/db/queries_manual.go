package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Hand written replacements for the queries that relied on SQLite's
// `RETURNING *`. MySQL has no RETURNING clause, so the statement is executed
// and the affected row is then read back with the matching Get* query. The Go
// signatures are kept identical to the previous sqlc output so callers are
// unaffected.
//
// Timestamps are supplied from Go rather than by a SQL function so the same
// statements run on both MySQL (production) and the in-memory SQLite fixtures
// the unit tests still use:
//   - sessions/files use seconds
//   - messages use milliseconds
//
// `sessions.updated_at` used to be refreshed by a SQLite trigger; MySQL cannot
// update the subject table from its own trigger, so UpdateSession sets it here.

const (
	updateCognitiveStateSQL = `UPDATE cognitive_state SET value = ?, updated_at = ? WHERE ` + "`key`" + ` = ?`

	createSessionSQL = `INSERT INTO sessions (
    id, parent_session_id, title, message_count, prompt_tokens, completion_tokens, cost, summary_message_id, updated_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`

	updateSessionSQL = `UPDATE sessions
SET title = ?, prompt_tokens = ?, completion_tokens = ?, summary_message_id = ?, cost = ?, updated_at = ?
WHERE id = ?`

	createFileSQL = `INSERT INTO files (
    id, session_id, path, content, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`

	updateFileSQL = `UPDATE files
SET content = ?, version = ?, updated_at = ?
WHERE id = ?`

	createMessageSQL = `INSERT INTO messages (
    id, session_id, role, parts, model, embedding, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	updateMessageSQL = `UPDATE messages
SET parts = ?, finished_at = ?, embedding = ?, updated_at = ?
WHERE id = ?`

	updateKnowledgeEdgeSQL = `UPDATE knowledge_edges
SET weight = ?, created_at = ?
WHERE source_id = ? AND target_id = ? AND relation = ?`

	insertKnowledgeEdgeSQL = `INSERT INTO knowledge_edges (
    source_id, target_id, relation, weight, created_at
) VALUES (?, ?, ?, ?, ?)`

	createMemorySQL = `INSERT INTO memories (
    id, category, content, source, embedding, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`

	updateMemorySQL = `UPDATE memories
SET category = ?, content = ?, source = ?, embedding = ?, updated_at = ?
WHERE id = ?`

	createScheduledTaskSQL = `INSERT INTO scheduled_tasks (
    id, task_type, parameters, schedule_expression, last_run_at, next_run_at, is_active, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	updateScheduledTaskSQL = `UPDATE scheduled_tasks
SET task_type = ?, parameters = ?, schedule_expression = ?, last_run_at = ?, next_run_at = ?, is_active = ?
WHERE id = ?`

	createKnowledgeNodeSQL = `INSERT INTO knowledge_tree (
    id, parent_id, layer, category, content, summary, keywords, confidence, source, embedding, created_at, updated_at, last_accessed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	updateKnowledgeNodeSQL = `UPDATE knowledge_tree
SET
    content = COALESCE(?, content),
    summary = COALESCE(?, summary),
    confidence = COALESCE(?, confidence),
    access_count = COALESCE(?, access_count),
    last_accessed_at = COALESCE(?, last_accessed_at),
    updated_at = ?
WHERE id = ?`
)

// SetCognitiveStateParams mirrors the previous sqlc generated struct.
type SetCognitiveStateParams struct {
	Key       string         `json:"key"`
	Value     sql.NullString `json:"value"`
	UpdatedAt int64          `json:"updated_at"`
}

// CreateSessionParams mirrors the previous sqlc generated struct.
type CreateSessionParams struct {
	ID               string         `json:"id"`
	ParentSessionID  sql.NullString `json:"parent_session_id"`
	Title            string         `json:"title"`
	MessageCount     int64          `json:"message_count"`
	PromptTokens     int64          `json:"prompt_tokens"`
	CompletionTokens int64          `json:"completion_tokens"`
	Cost             float64        `json:"cost"`
}

// UpdateSessionParams mirrors the previous sqlc generated struct.
type UpdateSessionParams struct {
	Title            string         `json:"title"`
	PromptTokens     int64          `json:"prompt_tokens"`
	CompletionTokens int64          `json:"completion_tokens"`
	SummaryMessageID sql.NullString `json:"summary_message_id"`
	Cost             float64        `json:"cost"`
	ID               string         `json:"id"`
}

// CreateFileParams mirrors the previous sqlc generated struct.
type CreateFileParams struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Version   string `json:"version"`
}

// UpdateFileParams mirrors the previous sqlc generated struct.
type UpdateFileParams struct {
	Content string `json:"content"`
	Version string `json:"version"`
	ID      string `json:"id"`
}

// CreateMessageParams mirrors the previous sqlc generated struct.
type CreateMessageParams struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Role      string         `json:"role"`
	Parts     string         `json:"parts"`
	Model     sql.NullString `json:"model"`
	Embedding sql.NullString `json:"embedding"`
}

// CreateMemoryParams mirrors the previous sqlc generated struct.
type CreateMemoryParams struct {
	ID        string         `json:"id"`
	Category  string         `json:"category"`
	Content   string         `json:"content"`
	Source    sql.NullString `json:"source"`
	Embedding sql.NullString `json:"embedding"`
	CreatedAt int64          `json:"created_at"`
	UpdatedAt int64          `json:"updated_at"`
}

// UpdateMemoryParams mirrors the previous sqlc generated struct.
type UpdateMemoryParams struct {
	Category  string         `json:"category"`
	Content   string         `json:"content"`
	Source    sql.NullString `json:"source"`
	Embedding sql.NullString `json:"embedding"`
	UpdatedAt int64          `json:"updated_at"`
	ID        string         `json:"id"`
}

// CreateScheduledTaskParams mirrors the previous sqlc generated struct.
type CreateScheduledTaskParams struct {
	ID                 string         `json:"id"`
	TaskType           string         `json:"task_type"`
	Parameters         string         `json:"parameters"`
	ScheduleExpression sql.NullString `json:"schedule_expression"`
	LastRunAt          sql.NullInt64  `json:"last_run_at"`
	NextRunAt          sql.NullInt64  `json:"next_run_at"`
	IsActive           sql.NullBool   `json:"is_active"`
	CreatedAt          int64          `json:"created_at"`
}

// UpdateScheduledTaskParams mirrors the previous sqlc generated struct.
type UpdateScheduledTaskParams struct {
	TaskType           string         `json:"task_type"`
	Parameters         string         `json:"parameters"`
	ScheduleExpression sql.NullString `json:"schedule_expression"`
	LastRunAt          sql.NullInt64  `json:"last_run_at"`
	NextRunAt          sql.NullInt64  `json:"next_run_at"`
	IsActive           sql.NullBool   `json:"is_active"`
	ID                 string         `json:"id"`
}

// CreateKnowledgeNodeParams mirrors the previous sqlc generated struct.
type CreateKnowledgeNodeParams struct {
	ID             string          `json:"id"`
	ParentID       sql.NullString  `json:"parent_id"`
	Layer          int64           `json:"layer"`
	Category       string          `json:"category"`
	Content        string          `json:"content"`
	Summary        sql.NullString  `json:"summary"`
	Keywords       sql.NullString  `json:"keywords"`
	Confidence     sql.NullFloat64 `json:"confidence"`
	Source         sql.NullString  `json:"source"`
	Embedding      sql.NullString  `json:"embedding"`
	CreatedAt      int64           `json:"created_at"`
	UpdatedAt      int64           `json:"updated_at"`
	LastAccessedAt sql.NullInt64   `json:"last_accessed_at"`
}

// UpdateKnowledgeNodeParams mirrors the previous sqlc generated struct.
type UpdateKnowledgeNodeParams struct {
	Content        sql.NullString  `json:"content"`
	Summary        sql.NullString  `json:"summary"`
	Confidence     sql.NullFloat64 `json:"confidence"`
	AccessCount    sql.NullInt64   `json:"access_count"`
	LastAccessedAt sql.NullInt64   `json:"last_accessed_at"`
	UpdatedAt      int64           `json:"updated_at"`
	ID             string          `json:"id"`
}

// UpdateMessageParams mirrors the previous sqlc generated struct.
type UpdateMessageParams struct {
	Parts      string         `json:"parts"`
	FinishedAt sql.NullInt64  `json:"finished_at"`
	Embedding  sql.NullString `json:"embedding"`
	ID         string         `json:"id"`
}

// CreateKnowledgeEdgeParams mirrors the previous sqlc generated struct.
type CreateKnowledgeEdgeParams struct {
	SourceID  string          `json:"source_id"`
	TargetID  string          `json:"target_id"`
	Relation  string          `json:"relation"`
	Weight    sql.NullFloat64 `json:"weight"`
	CreatedAt int64           `json:"created_at"`
}

// SetCognitiveState upserts one cognitive state entry and returns the stored row.
func (q *Queries) SetCognitiveState(ctx context.Context, arg SetCognitiveStateParams) (CognitiveState, error) {
	// Upsert without dialect specific syntax: UPDATE first, INSERT when the key
	// is new (MySQL would spell this ON DUPLICATE KEY UPDATE, SQLite ON CONFLICT).
	result, err := q.db.ExecContext(ctx, updateCognitiveStateSQL, arg.Value, arg.UpdatedAt, arg.Key)
	if err != nil {
		return CognitiveState{}, fmt.Errorf("SetCognitiveState: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CognitiveState{}, fmt.Errorf("SetCognitiveState: %w", err)
	}
	if affected == 0 {
		if _, err := q.db.ExecContext(ctx,
			"INSERT INTO cognitive_state (`key`, value, updated_at) VALUES (?, ?, ?)",
			arg.Key, arg.Value, arg.UpdatedAt,
		); err != nil {
			return CognitiveState{}, fmt.Errorf("SetCognitiveState: %w", err)
		}
	}
	return q.GetCognitiveState(ctx, arg.Key)
}

// CreateSession inserts a session and returns the stored row.
func (q *Queries) CreateSession(ctx context.Context, arg CreateSessionParams) (Session, error) {
	now := time.Now().Unix()
	if _, err := q.db.ExecContext(ctx, createSessionSQL,
		arg.ID,
		arg.ParentSessionID,
		arg.Title,
		arg.MessageCount,
		arg.PromptTokens,
		arg.CompletionTokens,
		arg.Cost,
		now,
		now,
	); err != nil {
		return Session{}, fmt.Errorf("CreateSession: %w", err)
	}
	return q.GetSessionByID(ctx, arg.ID)
}

// UpdateSession updates session metrics and returns the stored row.
func (q *Queries) UpdateSession(ctx context.Context, arg UpdateSessionParams) (Session, error) {
	if _, err := q.db.ExecContext(ctx, updateSessionSQL,
		arg.Title,
		arg.PromptTokens,
		arg.CompletionTokens,
		arg.SummaryMessageID,
		arg.Cost,
		time.Now().Unix(),
		arg.ID,
	); err != nil {
		return Session{}, fmt.Errorf("UpdateSession: %w", err)
	}
	return q.GetSessionByID(ctx, arg.ID)
}

// CreateFile inserts a file snapshot and returns the stored row.
func (q *Queries) CreateFile(ctx context.Context, arg CreateFileParams) (File, error) {
	now := time.Now().Unix()
	if _, err := q.db.ExecContext(ctx, createFileSQL,
		arg.ID,
		arg.SessionID,
		arg.Path,
		arg.Content,
		arg.Version,
		now,
		now,
	); err != nil {
		return File{}, fmt.Errorf("CreateFile: %w", err)
	}
	return q.GetFile(ctx, arg.ID)
}

// UpdateFile updates a file snapshot and returns the stored row.
func (q *Queries) UpdateFile(ctx context.Context, arg UpdateFileParams) (File, error) {
	if _, err := q.db.ExecContext(ctx, updateFileSQL, arg.Content, arg.Version, time.Now().Unix(), arg.ID); err != nil {
		return File{}, fmt.Errorf("UpdateFile: %w", err)
	}
	return q.GetFile(ctx, arg.ID)
}

// CreateMessage inserts a message and returns the stored row.
func (q *Queries) CreateMessage(ctx context.Context, arg CreateMessageParams) (Message, error) {
	nowMillis := time.Now().UnixMilli()
	if _, err := q.db.ExecContext(ctx, createMessageSQL,
		arg.ID,
		arg.SessionID,
		arg.Role,
		arg.Parts,
		arg.Model,
		arg.Embedding,
		nowMillis,
		nowMillis,
	); err != nil {
		return Message{}, fmt.Errorf("CreateMessage: %w", err)
	}
	return q.GetMessage(ctx, arg.ID)
}

// UpdateMessage stores the latest message payload and refreshes updated_at.
func (q *Queries) UpdateMessage(ctx context.Context, arg UpdateMessageParams) error {
	if _, err := q.db.ExecContext(ctx, updateMessageSQL,
		arg.Parts,
		arg.FinishedAt,
		arg.Embedding,
		time.Now().UnixMilli(),
		arg.ID,
	); err != nil {
		return fmt.Errorf("UpdateMessage: %w", err)
	}
	return nil
}

// CreateKnowledgeEdge inserts or refreshes a knowledge graph edge.
func (q *Queries) CreateKnowledgeEdge(ctx context.Context, arg CreateKnowledgeEdgeParams) error {
	// Same dialect neutral upsert trick as SetCognitiveState.
	result, err := q.db.ExecContext(ctx, updateKnowledgeEdgeSQL,
		arg.Weight, arg.CreatedAt, arg.SourceID, arg.TargetID, arg.Relation)
	if err != nil {
		return fmt.Errorf("CreateKnowledgeEdge: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("CreateKnowledgeEdge: %w", err)
	}
	if affected == 0 {
		if _, err := q.db.ExecContext(ctx, insertKnowledgeEdgeSQL,
			arg.SourceID, arg.TargetID, arg.Relation, arg.Weight, arg.CreatedAt,
		); err != nil {
			return fmt.Errorf("CreateKnowledgeEdge: %w", err)
		}
	}
	return nil
}

// CreateMemory inserts a memory and returns the stored row.
func (q *Queries) CreateMemory(ctx context.Context, arg CreateMemoryParams) (Memory, error) {
	if _, err := q.db.ExecContext(ctx, createMemorySQL,
		arg.ID,
		arg.Category,
		arg.Content,
		arg.Source,
		arg.Embedding,
		arg.CreatedAt,
		arg.UpdatedAt,
	); err != nil {
		return Memory{}, fmt.Errorf("CreateMemory: %w", err)
	}
	return q.GetMemory(ctx, arg.ID)
}

// UpdateMemory updates a memory and returns the stored row.
func (q *Queries) UpdateMemory(ctx context.Context, arg UpdateMemoryParams) (Memory, error) {
	if _, err := q.db.ExecContext(ctx, updateMemorySQL,
		arg.Category,
		arg.Content,
		arg.Source,
		arg.Embedding,
		arg.UpdatedAt,
		arg.ID,
	); err != nil {
		return Memory{}, fmt.Errorf("UpdateMemory: %w", err)
	}
	return q.GetMemory(ctx, arg.ID)
}

// CreateScheduledTask inserts a scheduled task and returns the stored row.
func (q *Queries) CreateScheduledTask(ctx context.Context, arg CreateScheduledTaskParams) (ScheduledTask, error) {
	if _, err := q.db.ExecContext(ctx, createScheduledTaskSQL,
		arg.ID,
		arg.TaskType,
		arg.Parameters,
		arg.ScheduleExpression,
		arg.LastRunAt,
		arg.NextRunAt,
		arg.IsActive,
		arg.CreatedAt,
	); err != nil {
		return ScheduledTask{}, fmt.Errorf("CreateScheduledTask: %w", err)
	}
	return q.GetScheduledTask(ctx, arg.ID)
}

// UpdateScheduledTask updates a scheduled task and returns the stored row.
func (q *Queries) UpdateScheduledTask(ctx context.Context, arg UpdateScheduledTaskParams) (ScheduledTask, error) {
	if _, err := q.db.ExecContext(ctx, updateScheduledTaskSQL,
		arg.TaskType,
		arg.Parameters,
		arg.ScheduleExpression,
		arg.LastRunAt,
		arg.NextRunAt,
		arg.IsActive,
		arg.ID,
	); err != nil {
		return ScheduledTask{}, fmt.Errorf("UpdateScheduledTask: %w", err)
	}
	return q.GetScheduledTask(ctx, arg.ID)
}

// CreateKnowledgeNode inserts a knowledge node and returns the stored row.
func (q *Queries) CreateKnowledgeNode(ctx context.Context, arg CreateKnowledgeNodeParams) (KnowledgeTree, error) {
	if _, err := q.db.ExecContext(ctx, createKnowledgeNodeSQL,
		arg.ID,
		arg.ParentID,
		arg.Layer,
		arg.Category,
		arg.Content,
		arg.Summary,
		arg.Keywords,
		arg.Confidence,
		arg.Source,
		arg.Embedding,
		arg.CreatedAt,
		arg.UpdatedAt,
		arg.LastAccessedAt,
	); err != nil {
		return KnowledgeTree{}, fmt.Errorf("CreateKnowledgeNode: %w", err)
	}
	return q.GetKnowledgeNode(ctx, arg.ID)
}

// UpdateKnowledgeNode applies a partial update and returns the stored row.
func (q *Queries) UpdateKnowledgeNode(ctx context.Context, arg UpdateKnowledgeNodeParams) (KnowledgeTree, error) {
	if _, err := q.db.ExecContext(ctx, updateKnowledgeNodeSQL,
		arg.Content,
		arg.Summary,
		arg.Confidence,
		arg.AccessCount,
		arg.LastAccessedAt,
		arg.UpdatedAt,
		arg.ID,
	); err != nil {
		return KnowledgeTree{}, fmt.Errorf("UpdateKnowledgeNode: %w", err)
	}
	return q.GetKnowledgeNode(ctx, arg.ID)
}
