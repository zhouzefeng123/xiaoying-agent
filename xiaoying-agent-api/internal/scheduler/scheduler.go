package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/embedding"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/lsp"
	"github.com/auramate/auramate/internal/mcp"

	"github.com/auramate/auramate/internal/memory"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/permission"
	"github.com/auramate/auramate/internal/session"
	"github.com/auramate/auramate/internal/skills"
)

// Notifier defines the interface for sending notifications to the user
type Notifier interface {
	Notify(title, content string) error
}

const (
	TaskTypeTestNotification = "test_notification"
	TaskTypeReminder         = "general_reminder"
	TaskTypeGeneralAgent     = "general_agent"
)

var deprecatedTaskTypes = map[string]struct{}{
	"daily_greeting": {},
	"news_briefing":  {},
	"user_profiling": {},
}

type Scheduler struct {
	q             *db.Queries
	db            *sql.DB
	sessions      session.Service
	messages      message.Service
	memories      memory.Service
	permissions   permission.Service
	mcpService    mcp.Service
	lspRegistry   *lsp.Registry
	skillsManager *skills.Manager
	embedder      embedding.Embedder
	quit          chan struct{}

	runningTasks sync.Map // Track running tasks to prevent duplicate execution
	taskQueue    chan db.ScheduledTask
	workerWg     sync.WaitGroup

	notifier          Notifier
	reflectionHandler func(ctx context.Context, sessionID string) error
}

func New(
	conn *sql.DB,
	sessions session.Service,
	messages message.Service,
	memories memory.Service,
	permissions permission.Service,
	mcpService mcp.Service,
	lspRegistry *lsp.Registry,
	skillsManager *skills.Manager,
	embedder embedding.Embedder,
) *Scheduler {
	return &Scheduler{
		q:             db.New(conn),
		db:            conn,
		sessions:      sessions,
		messages:      messages,
		memories:      memories,
		permissions:   permissions,
		mcpService:    mcpService,
		lspRegistry:   lspRegistry,
		skillsManager: skillsManager,
		embedder:      embedder,
		quit:          make(chan struct{}),
		taskQueue:     make(chan db.ScheduledTask, 100), // Buffer size 100
	}
}

func (s *Scheduler) SetNotifier(n Notifier) {
	s.notifier = n
}

func (s *Scheduler) SetReflectionHandler(handler func(ctx context.Context, sessionID string) error) {
	s.reflectionHandler = handler
}

func (s *Scheduler) EnsureDefaultTasks(ctx context.Context) {
	tasks, err := s.q.ListScheduledTasks(ctx)
	if err != nil {
		logging.Error("Failed to list scheduled tasks during deprecated task cleanup", "error", err)
		return
	}

	removed := 0
	for _, task := range tasks {
		if _, deprecated := deprecatedTaskTypes[task.TaskType]; !deprecated {
			continue
		}
		if err := s.q.DeleteScheduledTask(ctx, task.ID); err != nil {
			logging.Warn("Failed to delete deprecated scheduled task", "id", task.ID, "type", task.TaskType, "error", err)
			continue
		}
		removed++
	}

	if removed > 0 {
		logging.Info("Removed deprecated scheduled tasks on startup", "count", removed)
		return
	}

	logging.Info("Default scheduled task bootstrap disabled; all scheduled tasks are user-managed")
}

// calculateNextRun calculates the next execution time based on the schedule expression
func (s *Scheduler) calculateNextRun(expression string, fromTime time.Time) time.Time {
	// 1. Handle "daily:HH:MM" format
	var hour, minute int
	if n, err := fmt.Sscanf(expression, "daily:%d:%d", &hour, &minute); err == nil && n == 2 {
		// Create a time for today at HH:MM
		next := time.Date(fromTime.Year(), fromTime.Month(), fromTime.Day(), hour, minute, 0, 0, fromTime.Location())

		// If this time is in the past (or now), move to tomorrow
		if !next.After(fromTime) {
			next = next.Add(24 * time.Hour)
		}
		return next
	}

	// 2. Handle "every X" or standard duration format (fallback)
	// Try parsing as duration
	duration, err := time.ParseDuration(expression)
	if err == nil {
		return fromTime.Add(duration)
	}

	// Default fallback if parsing fails: 1 hour later
	logging.Warn("Invalid schedule expression, defaulting to 1h", "expr", expression)
	return fromTime.Add(time.Hour)
}

func (s *Scheduler) Start() {
	logging.Info("Starting Scheduler...")

	// Scheduled tasks are fully user-managed; startup no longer seeds built-in tasks.
	s.EnsureDefaultTasks(context.Background())

	// Start Workers
	workerCount := 5
	for i := 0; i < workerCount; i++ {
		s.workerWg.Add(1)
		go s.worker(i)
	}

	// Check every 1 second for extreme speed
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		// Recover from panic to keep scheduler running
		defer func() {
			if r := recover(); r != nil {
				logging.Error("Scheduler panic recovered", "error", r)
				// Restart the loop if it crashes
				s.Start()
			}
		}()

		for {
			select {
			case <-ticker.C:
				s.checkTasks()
			case <-s.quit:
				ticker.Stop()
				return
			}
		}
	}()
}

func (s *Scheduler) Stop() {
	logging.Info("Scheduler stopping... performing final reflection")

	// Force a final reflection before shutdown to ensure memory safety
	if s.reflectionHandler != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.reflectionHandler(ctx, "shutdown-reflection"); err != nil {
			logging.Error("Final reflection failed", "error", err)
		} else {
			logging.Info("Final reflection completed successfully")
		}
	}

	close(s.quit)
	close(s.taskQueue)
	s.workerWg.Wait()
}

func (s *Scheduler) worker(id int) {
	defer s.workerWg.Done()
	logging.Info("Scheduler worker started", "worker_id", id)

	for task := range s.taskQueue {
		// Execute task safely
		func() {
			defer func() {
				if r := recover(); r != nil {
					logging.Error("Worker panic recovered", "worker_id", id, "task_id", task.ID, "error", r)
				}
			}()
			s.executeTask(context.Background(), task)
		}()
	}
}

func (s *Scheduler) checkTasks() {
	// Recover from panic within a single check to avoid crashing the ticker loop
	defer func() {
		if r := recover(); r != nil {
			logging.Error("checkTasks panic recovered", "error", r)
		}
	}()

	ctx := context.Background()
	tasks, err := s.q.ListActiveScheduledTasks(ctx)
	if err != nil {
		logging.Error("Scheduler failed to list active tasks", "error", err)
		return
	}

	now := time.Now().Unix()
	for _, task := range tasks {
		// Check if task is due
		if task.NextRunAt.Valid && task.NextRunAt.Int64 <= now {
			// Check if already running
			if _, loaded := s.runningTasks.LoadOrStore(task.ID, true); loaded {
				logging.Debug("Task already running, skipping", "id", task.ID)
				continue
			}

			// Push to Queue (Non-blocking or Log if full)
			select {
			case s.taskQueue <- task:
				logging.Info("Queued scheduled task", "id", task.ID, "type", task.TaskType)
			default:
				logging.Warn("Task queue full, skipping execution for this tick", "id", task.ID)
				s.runningTasks.Delete(task.ID) // Cleanup since we didn't run it
			}
		}
	}
}

func (s *Scheduler) executeTask(ctx context.Context, task db.ScheduledTask) {
	defer func() {
		s.runningTasks.Delete(task.ID)
		if r := recover(); r != nil {
			logging.Error("executeTask panic recovered", "id", task.ID, "error", r)
		}
	}()

	logging.Info("Executing scheduled task", "id", task.ID, "type", task.TaskType)

	var err error
	switch task.TaskType {
	case TaskTypeTestNotification:
		err = s.runTestNotificationTask(ctx, task)
	case TaskTypeReminder:
		err = s.runReminderTask(ctx, task)
	case TaskTypeGeneralAgent:
		err = s.runGeneralAgentTask(ctx, task)
	default:
		logging.Warn("Unknown task type", "type", task.TaskType)
	}

	if err != nil {
		logging.Error("Failed to execute task", "id", task.ID, "error", err)
	}

	// Update LastRunAt and NextRunAt
	now := time.Now().Unix()

	// Determine if it's a recurring task
	var nextRun sql.NullInt64
	isActive := task.IsActive

	if task.ScheduleExpression.Valid && task.ScheduleExpression.String != "" {
		// Recurring task
		nextRunTime := s.calculateNextRun(task.ScheduleExpression.String, time.Unix(now, 0))
		nextRun = sql.NullInt64{Int64: nextRunTime.Unix(), Valid: true}

		params := db.UpdateScheduledTaskParams{
			ID:                 task.ID,
			TaskType:           task.TaskType,
			Parameters:         task.Parameters,
			ScheduleExpression: task.ScheduleExpression,
			IsActive:           isActive,
			LastRunAt:          sql.NullInt64{Int64: now, Valid: true},
			NextRunAt:          nextRun,
		}

		_, updateErr := s.q.UpdateScheduledTask(ctx, params)
		if updateErr != nil {
			logging.Error("Failed to update recurring task after execution", "id", task.ID, "error", updateErr)
		}
	} else {
		// One-time task: Delete it
		logging.Info("Deleting completed one-time task", "id", task.ID)
		if err := s.q.DeleteScheduledTask(ctx, task.ID); err != nil {
			logging.Error("Failed to delete completed one-time task", "id", task.ID, "error", err)
		}
	}
}

func (s *Scheduler) runTestNotificationTask(ctx context.Context, task db.ScheduledTask) error {
	logging.Info("Running Test Notification Task...")
	if s.notifier != nil {
		s.notifier.Notify("测试通知", "这是一个来自后端的测试消息，用于验证消息中心功能")
	}
	return nil
}

func (s *Scheduler) runReminderTask(ctx context.Context, task db.ScheduledTask) error {
	logging.Info("Running Reminder Task...")
	// Parse parameters JSON (simple string check for now)
	content := task.Parameters
	if content == "" {
		content = "您有一个新的提醒！"
	}

	if s.notifier != nil {
		// Use server notifier
		s.notifier.Notify("提醒", content)
	}

	// CRITICAL FIX: Also emit an event to the main chat if possible
	// Since scheduler runs in background, it might not have an active chat session.
	// We can create a ephemeral session or broadcast a system event.
	// For now, logging and Notify (which sends to frontend toast/notification) is standard.
	// But let's verify if 'notifier' is actually hooked up to WebSocket.
	return nil
}
