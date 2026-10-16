package message

import (
	"context"
	"database/sql"
	"testing"

	"github.com/auramate/auramate/internal/db"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func newMessageWindowTestService(t *testing.T) Service {
	t.Helper()

	conn, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })

	schema := `
CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    parts TEXT NOT NULL,
    model TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    finished_at INTEGER,
    embedding TEXT
);`

	if _, err := conn.Exec(schema); err != nil {
		t.Fatalf("init messages schema: %v", err)
	}

	return NewService(db.New(conn), nil)
}

func seedWindowMessage(t *testing.T, svc Service, sessionID, messageID, text string, role MessageRole) {
	t.Helper()

	impl, ok := svc.(*service)
	if !ok {
		t.Fatalf("expected concrete message service")
	}

	parts, err := marshallParts([]ContentPart{TextContent{Text: text}})
	if err != nil {
		t.Fatalf("marshal seeded message parts: %v", err)
	}

	if _, err := impl.q.CreateMessage(context.Background(), db.CreateMessageParams{
		ID:        messageID,
		SessionID: sessionID,
		Role:      string(role),
		Parts:     string(parts),
	}); err != nil {
		t.Fatalf("insert seeded message: %v", err)
	}
}

func TestListWindowStartsAtSummaryAnchorWhenAnchorInRecentHistory(t *testing.T) {
	svc := newMessageWindowTestService(t)
	sessionID := "session-window-anchor"

	seedWindowMessage(t, svc, sessionID, "msg-1", "old-user-1", User)
	seedWindowMessage(t, svc, sessionID, "msg-2", "old-assistant-2", Assistant)
	seedWindowMessage(t, svc, sessionID, "msg-3", "[System Summary]: summarized", User)
	seedWindowMessage(t, svc, sessionID, "msg-4", "new-user-4", User)
	seedWindowMessage(t, svc, sessionID, "msg-5", "new-assistant-5", Assistant)

	window, err := svc.ListWindow(context.Background(), sessionID, "msg-3", 10)
	if err != nil {
		t.Fatalf("list window: %v", err)
	}

	if len(window) != 3 {
		t.Fatalf("expected 3 messages from summary anchor onward, got %d", len(window))
	}

	gotIDs := []string{window[0].ID, window[1].ID, window[2].ID}
	wantIDs := []string{"msg-3", "msg-4", "msg-5"}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("unexpected window ids: got %v want %v", gotIDs, wantIDs)
		}
	}

	for _, msg := range window {
		if msg.ID == "msg-1" || msg.ID == "msg-2" {
			t.Fatalf("expected pre-summary messages to be excluded from window, got %s", msg.ID)
		}
	}
}
