package cognitive

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/db"
)

type TokenBucket struct {
	q  *db.Queries
	mu sync.Mutex
}

func NewTokenBucket(q *db.Queries) *TokenBucket {
	return &TokenBucket{q: q}
}

// CheckBudget returns true if current usage < limit
func (tb *TokenBucket) CheckBudget(ctx context.Context) (bool, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Get Usage
	usageStr, err := tb.getState(ctx, "daily_token_usage")
	if err != nil {
		return false, err
	}
	usage, _ := strconv.ParseInt(usageStr, 10, 64)

	// Get Limit
	limitStr, err := tb.getState(ctx, "daily_token_limit")
	if err != nil {
		return false, err
	}
	limit, _ := strconv.ParseInt(limitStr, 10, 64)

	return usage < limit, nil
}

// Consume adds tokens to usage
func (tb *TokenBucket) Consume(ctx context.Context, amount int64) error {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Get current
	usageStr, err := tb.getState(ctx, "daily_token_usage")
	if err != nil {
		return err
	}
	usage, _ := strconv.ParseInt(usageStr, 10, 64)

	// Update
	newUsage := usage + amount
	return tb.setState(ctx, "daily_token_usage", fmt.Sprintf("%d", newUsage))
}

// Reset resets usage to 0 (called daily)
func (tb *TokenBucket) Reset(ctx context.Context) error {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.setState(ctx, "daily_token_usage", "0")
}

// AddLimit adds tokens to the daily limit (Feeding)
func (tb *TokenBucket) AddLimit(ctx context.Context, amount int64) error {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	limitStr, err := tb.getState(ctx, "daily_token_limit")
	if err != nil {
		return err
	}
	limit, _ := strconv.ParseInt(limitStr, 10, 64)

	newLimit := limit + amount
	return tb.setState(ctx, "daily_token_limit", fmt.Sprintf("%d", newLimit))
}

// GetStatus returns current usage and limit
func (tb *TokenBucket) GetStatus(ctx context.Context) (int64, int64, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	usageStr, err := tb.getState(ctx, "daily_token_usage")
	if err != nil {
		return 0, 0, err
	}
	usage, _ := strconv.ParseInt(usageStr, 10, 64)

	limitStr, err := tb.getState(ctx, "daily_token_limit")
	if err != nil {
		return 0, 0, err
	}
	limit, _ := strconv.ParseInt(limitStr, 10, 64)

	return usage, limit, nil
}

// Helper methods for cognitive_state table
func (tb *TokenBucket) getState(ctx context.Context, key string) (string, error) {
	state, err := tb.q.GetCognitiveState(ctx, key)
	if err != nil {
		if err == sql.ErrNoRows {
			// Return default or empty?
			// For usage, 0 is safe default
			if key == "daily_token_usage" {
				return "0", nil
			}
			if key == "daily_token_limit" {
				return "50000", nil
			}
			return "", nil
		}
		return "", err
	}
	if state.Value.Valid {
		return state.Value.String, nil
	}
	return "", nil
}

func (tb *TokenBucket) setState(ctx context.Context, key, value string) error {
	params := db.SetCognitiveStateParams{
		Key:       key,
		Value:     sql.NullString{String: value, Valid: true},
		UpdatedAt: time.Now().Unix(),
	}
	_, err := tb.q.SetCognitiveState(ctx, params)
	return err
}
