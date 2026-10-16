package cognitive

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/auramate/auramate/internal/db"
)

func setBudgetState(t *testing.T, q *db.Queries, usage, limit int64) {
	t.Helper()
	now := time.Now().Unix()
	if _, err := q.SetCognitiveState(context.Background(), db.SetCognitiveStateParams{
		Key:       "daily_token_usage",
		Value:     sql.NullString{String: stringValue(usage), Valid: true},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set usage: %v", err)
	}
	if _, err := q.SetCognitiveState(context.Background(), db.SetCognitiveStateParams{
		Key:       "daily_token_limit",
		Value:     sql.NullString{String: stringValue(limit), Valid: true},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("set limit: %v", err)
	}
}

func stringValue(value int64) string {
	return fmt.Sprintf("%d", value)
}

func TestTransitionToInteractiveWithoutRemainingTokensStaysDormant(t *testing.T) {
	engine, q, conn := newReflectionTestEngine(t)
	defer conn.Close()

	setBudgetState(t, q, 100, 100)
	engine.TransitionTo(context.Background(), string(StateInteractive))

	state, _, _, err := engine.GetState(context.Background())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != string(StateDormant) {
		t.Fatalf("expected dormant when budget exhausted, got %s", state)
	}
}

func TestConsumeLifeTokensDepletesBudgetAndForcesDormant(t *testing.T) {
	engine, q, conn := newReflectionTestEngine(t)
	defer conn.Close()

	setBudgetState(t, q, 90, 100)
	engine.TransitionTo(context.Background(), string(StateInteractive))

	depleted, err := engine.ConsumeLifeTokens(context.Background(), 10)
	if err != nil {
		t.Fatalf("consume life tokens: %v", err)
	}
	if !depleted {
		t.Fatalf("expected depletion when usage reaches limit")
	}

	state, usage, limit, err := engine.GetState(context.Background())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != string(StateDormant) {
		t.Fatalf("expected dormant after depletion, got %s", state)
	}
	if usage != limit {
		t.Fatalf("expected usage to equal limit after depletion, got usage=%d limit=%d", usage, limit)
	}
}
