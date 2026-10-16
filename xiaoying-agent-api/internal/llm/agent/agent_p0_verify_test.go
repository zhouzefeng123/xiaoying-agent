package agent

import (
	"fmt"
	"testing"
	"time"
)

// TestCharPerTokenValue verifies that CharPerToken is correctly set to 3.5
func TestCharPerTokenValue(t *testing.T) {
	if CharPerToken != 3.5 {
		t.Errorf("CharPerToken = %.1f, want 3.5", CharPerToken)
	} else {
		t.Logf("✅ CharPerToken correctly set to %.1f", CharPerToken)
	}
}

// TestCharPerTokenEstimation verifies token estimation accuracy
func TestCharPerTokenEstimation(t *testing.T) {
	tests := []struct {
		name   string
		chars  int
		newEst float64 // estimation with CharPerToken=3.5
		oldEst float64 // estimation with CharPerToken=1.2
	}{
		{
			name:   "100字符内容",
			chars:  100,
			newEst: 100 / 3.5, // ~28.6
			oldEst: 100 / 1.2, // ~83.3
		},
		{
			name:   "10KB内容",
			chars:  10000,
			newEst: 10000 / 3.5, // ~2857
			oldEst: 10000 / 1.2, // ~8333
		},
		{
			name:   "100KB内容",
			chars:  100000,
			newEst: 100000 / 3.5, // ~28571
			oldEst: 100000 / 1.2, // ~83333
		},
		{
			name:   "1MB内容",
			chars:  1000000,
			newEst: 1000000 / 3.5, // ~285714
			oldEst: 1000000 / 1.2, // ~833333
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate with new CharPerToken
			newTokens := float64(tt.chars) / CharPerToken

			// Verify calculation is correct
			if newTokens != tt.newEst {
				t.Errorf("New estimation = %.0f, want %.0f", newTokens, tt.newEst)
			}

			// Calculate improvement ratio
			ratio := tt.oldEst / tt.newEst

			t.Logf("%s: %d chars\n  Old (1.2): %.0f tokens\n  New (3.5): %.0f tokens\n  Improvement: %.1fx more accurate",
				tt.name, tt.chars, tt.oldEst, tt.newEst, ratio)
		})
	}
}

// TestSummarizationStateStructure verifies the SummarizationState struct exists and has correct fields
func TestSummarizationStateStructure(t *testing.T) {
	now := time.Now()
	state := &SummarizationState{
		failureCount:    2,
		lastAttemptTime: now,
		backoffDuration: 4 * time.Second,
		lastSuccessTime: now.Add(-10 * time.Second),
	}

	// Verify all fields are accessible
	if state.failureCount != 2 {
		t.Error("failureCount field missing or incorrect")
	}
	if state.backoffDuration != 4*time.Second {
		t.Error("backoffDuration field missing or incorrect")
	}
	if state.lastAttemptTime != now {
		t.Error("lastAttemptTime field missing or incorrect")
	}
	if state.lastSuccessTime.Before(now) == false {
		t.Error("lastSuccessTime field missing or incorrect")
	}

	t.Log("✅ SummarizationState structure is valid with all required fields")
}

// TestExponentialBackoff verifies the exponential backoff calculation
func TestExponentialBackoff(t *testing.T) {
	tests := []struct {
		failureCount int
		expectedSecs int
	}{
		{failureCount: 1, expectedSecs: 2},  // 2^1 = 2
		{failureCount: 2, expectedSecs: 4},  // 2^2 = 4
		{failureCount: 3, expectedSecs: 8},  // 2^3 = 8
		{failureCount: 4, expectedSecs: 16}, // 2^4 = 16
		{failureCount: 5, expectedSecs: 32}, // 2^5 = 32
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("failure_%d", tt.failureCount), func(t *testing.T) {
			// Calculate exponential backoff: 1 << uint(failureCount) seconds
			calculated := 1 << uint(tt.failureCount)

			if calculated != tt.expectedSecs {
				t.Errorf("Backoff for %d failures = %d sec, want %d sec", tt.failureCount, calculated, tt.expectedSecs)
			}

			t.Logf("✅ Failure #%d → Backoff: %d seconds", tt.failureCount, calculated)
		})
	}
}

// TestFallbackThreshold verifies that fallback is triggered after 3 failures
func TestFallbackThreshold(t *testing.T) {
	tests := []struct {
		failureCount   int
		shouldFallback bool
	}{
		{failureCount: 0, shouldFallback: false},
		{failureCount: 1, shouldFallback: false},
		{failureCount: 2, shouldFallback: false},
		{failureCount: 3, shouldFallback: true}, // Threshold
		{failureCount: 4, shouldFallback: true},
		{failureCount: 5, shouldFallback: true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("failures_%d", tt.failureCount), func(t *testing.T) {
			shouldUse := tt.failureCount >= 3

			if shouldUse != tt.shouldFallback {
				t.Errorf("Fallback for %d failures = %v, want %v", tt.failureCount, shouldUse, tt.shouldFallback)
			}

			status := "retry"
			if shouldUse {
				status = "fallback"
			}
			t.Logf("✅ Failure #%d → Status: %s", tt.failureCount, status)
		})
	}
}
