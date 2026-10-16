package cognitive

import (
	"context"
	"database/sql"
	"testing"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/memory"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

type reflectionTestAgent struct {
	summarize func(ctx context.Context, sessionID string) error
}

func (a *reflectionTestAgent) Model() models.Model { return models.Model{} }

func (a *reflectionTestAgent) Run(ctx context.Context, sessionID string, content string, mode string, attachments ...message.Attachment) (<-chan agent.AgentEvent, error) {
	ch := make(chan agent.AgentEvent, 1)
	ch <- agent.AgentEvent{
		Type:    agent.AgentEventTypeResponse,
		Content: `{"core_principles":[],"interests":[],"habits":[],"preferences":[],"new_facts":[],"highlights":[]}`,
	}
	close(ch)
	return ch, nil
}

func (a *reflectionTestAgent) Cancel(sessionID string) {}

func (a *reflectionTestAgent) CancelTool(sessionID string, executionID string) {}

func (a *reflectionTestAgent) IsSessionBusy(sessionID string) bool { return false }

func (a *reflectionTestAgent) IsBusy() bool { return false }

func (a *reflectionTestAgent) Update(agentName config.AgentName, modelID models.ModelID) (models.Model, error) {
	return models.Model{}, nil
}

func (a *reflectionTestAgent) Summarize(ctx context.Context, sessionID string) error {
	if a.summarize != nil {
		return a.summarize(ctx, sessionID)
	}
	return nil
}

func (a *reflectionTestAgent) SetReadOnlyMode(ctx context.Context, sessionID string, enabled bool) error {
	return nil
}

func (a *reflectionTestAgent) Subscribe(ctx context.Context) <-chan pubsub.Event[agent.AgentEvent] {
	return nil
}

func newReflectionTestEngine(t *testing.T) (*Engine, *db.Queries, *sql.DB) {
	t.Helper()

	conn, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	conn.SetMaxOpenConns(1)

	schema := `
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    parent_session_id TEXT,
    title TEXT NOT NULL,
    message_count INTEGER NOT NULL DEFAULT 0,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cost REAL NOT NULL DEFAULT 0.0,
    updated_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    summary_message_id TEXT
);

CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    parts TEXT NOT NULL DEFAULT '[]',
    model TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    finished_at INTEGER,
    embedding TEXT
);

CREATE TABLE knowledge_tree (
    id TEXT PRIMARY KEY,
    parent_id TEXT,
    layer INTEGER NOT NULL,
    category TEXT NOT NULL,
    content TEXT NOT NULL,
    summary TEXT,
    keywords TEXT,
    confidence REAL DEFAULT 1.0,
    source TEXT,
    embedding TEXT,
    access_count INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_accessed_at INTEGER DEFAULT 0
);

CREATE TABLE cognitive_state (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at INTEGER NOT NULL
);
`

	if _, err := conn.Exec(schema); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	q := db.New(conn)
	mem := memory.NewService(q, conn, nil)
	engine := NewEngine(conn, mem)
	engine.SetAgent(&reflectionTestAgent{})

	return engine, q, conn
}

func createReflectionTestSession(t *testing.T, q *db.Queries, sessionID string) {
	t.Helper()

	ctx := context.Background()
	if _, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:               sessionID,
		Title:            "测试会话",
		MessageCount:     0,
		PromptTokens:     0,
		CompletionTokens: 0,
		Cost:             0,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID:        "msg-user-1",
		SessionID: sessionID,
		Role:      "user",
		Parts:     "请把当前生命周期的阶段切换问题彻底查清楚，并给我一个明确结论。",
	}); err != nil {
		t.Fatalf("create user message: %v", err)
	}

	if _, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID:        "msg-assistant-1",
		SessionID: sessionID,
		Role:      "assistant",
		Parts:     "我会直接检查代码和数据库，不靠猜测。",
	}); err != nil {
		t.Fatalf("create assistant message: %v", err)
	}
}

func countLayerNodes(t *testing.T, q *db.Queries, layer int64) int {
	t.Helper()

	nodes, err := q.ListKnowledgeByLayer(context.Background(), layer)
	if err != nil {
		t.Fatalf("list knowledge by layer: %v", err)
	}

	return len(nodes)
}

func TestReflectSkipsUnchangedSessionAfterCheckpoint(t *testing.T) {
	engine, q, conn := newReflectionTestEngine(t)
	defer conn.Close()

	createReflectionTestSession(t, q, "session-1")

	ctx := context.Background()
	if err := engine.Reflect(ctx, "session-auto"); err != nil {
		t.Fatalf("first reflect: %v", err)
	}

	firstCount := countLayerNodes(t, q, LayerEpisodic)
	if firstCount != 2 {
		t.Fatalf("unexpected episodic count after first reflection: want 2, got %d", firstCount)
	}

	if err := engine.Reflect(ctx, "session-auto"); err != nil {
		t.Fatalf("second reflect: %v", err)
	}

	secondCount := countLayerNodes(t, q, LayerEpisodic)
	if secondCount != firstCount {
		t.Fatalf("unchanged session should not create duplicate L4 fragments: want %d, got %d", firstCount, secondCount)
	}
}

func TestReflectProcessesSessionAgainAfterNewMessage(t *testing.T) {
	engine, q, conn := newReflectionTestEngine(t)
	defer conn.Close()

	createReflectionTestSession(t, q, "session-1")

	ctx := context.Background()
	if err := engine.Reflect(ctx, "session-auto"); err != nil {
		t.Fatalf("first reflect: %v", err)
	}

	firstCount := countLayerNodes(t, q, LayerEpisodic)

	if _, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID:        "msg-user-2",
		SessionID: "session-1",
		Role:      "user",
		Parts:     "再补充一点：你需要把数据库里的阶段流转也一起验证。",
	}); err != nil {
		t.Fatalf("create extra message: %v", err)
	}

	if err := engine.Reflect(ctx, "session-auto"); err != nil {
		t.Fatalf("second reflect after update: %v", err)
	}

	count := countLayerNodes(t, q, LayerEpisodic)
	if count != firstCount+3 {
		t.Fatalf("updated session should produce a new reflection batch: want %d, got %d", firstCount+3, count)
	}
}

func TestReflectSkipsSessionAfterSummarizeRewritesLastMessage(t *testing.T) {
	engine, q, conn := newReflectionTestEngine(t)
	defer conn.Close()

	createReflectionTestSession(t, q, "session-1")
	engine.SetAgent(&reflectionTestAgent{
		summarize: func(ctx context.Context, sessionID string) error {
			msg, err := q.GetMessage(ctx, "msg-assistant-1")
			if err != nil {
				return err
			}
			_, err = conn.ExecContext(ctx, "UPDATE messages SET role = ?, parts = ? WHERE id = ?", "user", "[System Summary]: 已完成阶段分析。", msg.ID)
			return err
		},
	})

	ctx := context.Background()
	if err := engine.Reflect(ctx, "session-auto"); err != nil {
		t.Fatalf("first reflect: %v", err)
	}

	firstCount := countLayerNodes(t, q, LayerEpisodic)
	if err := engine.Reflect(ctx, "session-auto"); err != nil {
		t.Fatalf("second reflect: %v", err)
	}

	secondCount := countLayerNodes(t, q, LayerEpisodic)
	if secondCount != firstCount {
		t.Fatalf("summary rewrite should not invalidate reflection checkpoint: want %d, got %d", firstCount, secondCount)
	}
}

func TestReflectIgnoresGreetingOnlyJSONSession(t *testing.T) {
	engine, q, conn := newReflectionTestEngine(t)
	defer conn.Close()

	ctx := context.Background()
	if _, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:               "session-greeting",
		Title:            "问候会话",
		MessageCount:     0,
		PromptTokens:     0,
		CompletionTokens: 0,
		Cost:             0,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	userParts := `[{"type":"text","data":{"text":"你好"}},{"type":"finish","data":{"reason":"stop","time":0}}]`
	assistantParts := `[{"type":"text","data":{"text":"你好，有什么我可以帮你的？"}}]`
	if _, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID:        "msg-user-greeting",
		SessionID: "session-greeting",
		Role:      "user",
		Parts:     userParts,
	}); err != nil {
		t.Fatalf("create user greeting: %v", err)
	}
	if _, err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID:        "msg-assistant-greeting",
		SessionID: "session-greeting",
		Role:      "assistant",
		Parts:     assistantParts,
	}); err != nil {
		t.Fatalf("create assistant greeting: %v", err)
	}

	if err := engine.Reflect(ctx, "session-greeting"); err != nil {
		t.Fatalf("reflect greeting session: %v", err)
	}

	count := countLayerNodes(t, q, LayerEpisodic)
	if count != 0 {
		t.Fatalf("greeting-only session should not create episodic fragments, got %d", count)
	}
}
