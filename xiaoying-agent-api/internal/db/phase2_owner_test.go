package db_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/dbtest"
)

func TestPhase2OwnerScans(t *testing.T) {
	conn := dbtest.OpenMySQL(t)
	q := db.New(conn)
	ctx := context.Background()
	now := time.Now().Unix()

	taskA, err := q.CreateScheduledTask(ctx, db.CreateScheduledTaskParams{
		ID:         "task-a",
		TaskType:   "general_reminder",
		Parameters: "alice reminder",
		IsActive:   sql.NullBool{Bool: true, Valid: true},
		CreatedAt:  now,
		OwnerEmail: "alice@qq.com",
	})
	if err != nil {
		t.Fatalf("create Alice task: %v", err)
	}
	if taskA.OwnerEmail != "alice@qq.com" {
		t.Fatalf("task owner = %q", taskA.OwnerEmail)
	}

	if _, err := q.CreateScheduledTask(ctx, db.CreateScheduledTaskParams{
		ID:         "task-b",
		TaskType:   "general_reminder",
		Parameters: "bob reminder",
		IsActive:   sql.NullBool{Bool: true, Valid: true},
		CreatedAt:  now,
		OwnerEmail: "bob@qq.com",
	}); err != nil {
		t.Fatalf("create Bob task: %v", err)
	}

	tasks, err := q.ListScheduledTasks(ctx)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 2 || tasks[0].OwnerEmail == "" || tasks[1].OwnerEmail == "" {
		t.Fatalf("unexpected tasks/owners: %+v", tasks)
	}

	if _, err := conn.ExecContext(ctx, `INSERT INTO saved_news
		(id, title, summary, category, url, published_at, created_at, owner_email)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"news-a", "Alice news", "summary", "tech", "https://example.com", now, now, "alice@qq.com",
	); err != nil {
		t.Fatalf("insert saved news: %v", err)
	}
	news, err := q.ListSavedNews(ctx, 10)
	if err != nil {
		t.Fatalf("list saved news: %v", err)
	}
	if len(news) != 1 || news[0].OwnerEmail != "alice@qq.com" {
		t.Fatalf("unexpected saved news: %+v", news)
	}

	longKey := "1234567890abcdef:reflection_checkpoint:session-1234567890"
	if _, err := q.SetCognitiveState(ctx, db.SetCognitiveStateParams{
		Key:       longKey,
		Value:     sql.NullString{String: "checkpoint", Valid: true},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set long cognitive key: %v", err)
	}
	state, err := q.GetCognitiveState(ctx, longKey)
	if err != nil || !state.Value.Valid || state.Value.String != "checkpoint" {
		t.Fatalf("get long cognitive key: state=%+v err=%v", state, err)
	}
}
