package scheduler

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/auramate/auramate/internal/db"
)

func TestEnsureDefaultTasksRemovesDeprecatedTaskTypes(t *testing.T) {
	conn, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer conn.Close()

	_, err = conn.Exec(`
		CREATE TABLE scheduled_tasks (
			id TEXT PRIMARY KEY,
			task_type TEXT NOT NULL,
			parameters TEXT NOT NULL,
			schedule_expression TEXT,
			last_run_at INTEGER,
			next_run_at INTEGER,
			is_active INTEGER,
			created_at INTEGER NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("create scheduled_tasks table: %v", err)
	}

	queries := db.New(conn)
	seedTask := func(id string, taskType string) {
		t.Helper()
		_, seedErr := queries.CreateScheduledTask(context.Background(), db.CreateScheduledTaskParams{
			ID:         id,
			TaskType:   taskType,
			Parameters: "{}",
			CreatedAt:  1,
		})
		if seedErr != nil {
			t.Fatalf("seed task %s: %v", id, seedErr)
		}
	}

	seedTask("deprecated-greeting", "daily_greeting")
	seedTask("deprecated-news", "news_briefing")
	seedTask("deprecated-profile", "user_profiling")
	seedTask("active-reminder", TaskTypeReminder)

	s := New(conn, nil, nil, nil, nil, nil, nil, nil, nil)
	s.EnsureDefaultTasks(context.Background())

	tasks, err := queries.ListScheduledTasks(context.Background())
	if err != nil {
		t.Fatalf("list tasks after cleanup: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task after cleanup, got %d", len(tasks))
	}
	if tasks[0].TaskType != TaskTypeReminder {
		t.Fatalf("expected remaining task type %q, got %q", TaskTypeReminder, tasks[0].TaskType)
	}
}
