package db

import (
	"context"
	"database/sql"
)

// Querier is the database contract the services depend on.
//
// It is maintained by hand (sqlc runs with emit_interface: false) because a
// large part of the surface lives in queries_manual.go: MySQL has no RETURNING
// clause, so every "insert/update and give me the row back" statement is
// executed and then read back by hand. When you add a query, add it here too –
// `var _ Querier = (*Queries)(nil)` below keeps the two in sync.
type Querier interface {
	// Sessions
	CreateSession(ctx context.Context, arg CreateSessionParams) (Session, error)
	UpdateSession(ctx context.Context, arg UpdateSessionParams) (Session, error)
	GetSessionByID(ctx context.Context, id string) (Session, error)
	ListSessions(ctx context.Context) ([]Session, error)
	DeleteSession(ctx context.Context, id string) error
	GetTokenStats(ctx context.Context) (GetTokenStatsRow, error)

	// Files
	CreateFile(ctx context.Context, arg CreateFileParams) (File, error)
	UpdateFile(ctx context.Context, arg UpdateFileParams) (File, error)
	GetFile(ctx context.Context, id string) (File, error)
	GetFileByPathAndSession(ctx context.Context, arg GetFileByPathAndSessionParams) (File, error)
	ListFilesBySession(ctx context.Context, sessionID string) ([]File, error)
	ListFilesByPath(ctx context.Context, path string) ([]File, error)
	ListLatestSessionFiles(ctx context.Context, sessionID string) ([]File, error)
	DeleteFile(ctx context.Context, id string) error
	DeleteSessionFiles(ctx context.Context, sessionID string) error

	// Messages
	CreateMessage(ctx context.Context, arg CreateMessageParams) (Message, error)
	UpdateMessage(ctx context.Context, arg UpdateMessageParams) error
	GetMessage(ctx context.Context, id string) (Message, error)
	GetMessageCreatedAt(ctx context.Context, id string) (int64, error)
	ListMessagesBySession(ctx context.Context, sessionID string) ([]Message, error)
	ListRecentMessagesBySession(ctx context.Context, arg ListRecentMessagesBySessionParams) ([]Message, error)
	ListUserMessagesWithEmbedding(ctx context.Context) ([]Message, error)
	ListSessionMessagesWithEmbedding(ctx context.Context, sessionID string) ([]Message, error)
	DeleteMessage(ctx context.Context, id string) error
	DeleteSessionMessages(ctx context.Context, sessionID string) error

	// Memories
	CreateMemory(ctx context.Context, arg CreateMemoryParams) (Memory, error)
	UpdateMemory(ctx context.Context, arg UpdateMemoryParams) (Memory, error)
	UpdateMemoryEmbedding(ctx context.Context, arg UpdateMemoryEmbeddingParams) error
	UpdateMemoryConfidence(ctx context.Context, arg UpdateMemoryConfidenceParams) error
	UpdateMemoryCategory(ctx context.Context, arg UpdateMemoryCategoryParams) error
	GetMemory(ctx context.Context, id string) (Memory, error)
	ListMemories(ctx context.Context) ([]Memory, error)
	ListMemoriesByCategory(ctx context.Context, category string) ([]Memory, error)
	DeleteMemory(ctx context.Context, id string) error

	// Knowledge tree (memories move here over time)
	CreateKnowledgeNode(ctx context.Context, arg CreateKnowledgeNodeParams) (KnowledgeTree, error)
	UpdateKnowledgeNode(ctx context.Context, arg UpdateKnowledgeNodeParams) (KnowledgeTree, error)
	UpdateKnowledgeNodeConfidence(ctx context.Context, arg UpdateKnowledgeNodeConfidenceParams) error
	IncrementAccessCount(ctx context.Context, id string) error
	GetKnowledgeNode(ctx context.Context, id string) (KnowledgeTree, error)
	ListKnowledgeByLayer(ctx context.Context, layer int64) ([]KnowledgeTree, error)
	ListKnowledgeByParent(ctx context.Context, parentID sql.NullString) ([]KnowledgeTree, error)
	SearchKnowledge(ctx context.Context, arg SearchKnowledgeParams) ([]KnowledgeTree, error)
	DeleteKnowledgeNode(ctx context.Context, id string) error

	// Knowledge graph edges (GraphRAG)
	CreateKnowledgeEdge(ctx context.Context, arg CreateKnowledgeEdgeParams) error
	GetKnowledgeEdges(ctx context.Context, sourceID string) ([]GetKnowledgeEdgesRow, error)
	GetIncomingEdges(ctx context.Context, targetID string) ([]GetIncomingEdgesRow, error)
	DeleteKnowledgeEdge(ctx context.Context, arg DeleteKnowledgeEdgeParams) error

	// Cognitive state
	SetCognitiveState(ctx context.Context, arg SetCognitiveStateParams) (CognitiveState, error)
	GetCognitiveState(ctx context.Context, key string) (CognitiveState, error)
	ListAllCognitiveStates(ctx context.Context) ([]CognitiveState, error)

	// Scheduled tasks
	CreateScheduledTask(ctx context.Context, arg CreateScheduledTaskParams) (ScheduledTask, error)
	UpdateScheduledTask(ctx context.Context, arg UpdateScheduledTaskParams) (ScheduledTask, error)
	GetScheduledTask(ctx context.Context, id string) (ScheduledTask, error)
	ListScheduledTasks(ctx context.Context) ([]ScheduledTask, error)
	ListActiveScheduledTasks(ctx context.Context) ([]ScheduledTask, error)
	DeleteScheduledTask(ctx context.Context, id string) error

	// Saved news
	ListSavedNews(ctx context.Context, limit int32) ([]SavedNews, error)
}

var _ Querier = (*Queries)(nil)
