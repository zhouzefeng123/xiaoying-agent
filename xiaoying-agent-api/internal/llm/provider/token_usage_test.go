package provider

import "testing"

func TestTokenUsageTotalsIncludeCacheTokens(t *testing.T) {
	usage := TokenUsage{
		InputTokens:         10,
		OutputTokens:        4,
		CacheReadTokens:     6,
		CacheCreationTokens: 2,
	}

	if got := usage.PromptTokens(); got != 18 {
		t.Fatalf("expected prompt tokens 18, got %d", got)
	}
	if got := usage.TotalTokens(); got != 22 {
		t.Fatalf("expected total tokens 22, got %d", got)
	}
}

func TestUsageDeltaTreatsUsageEventsAsSnapshots(t *testing.T) {
	previous := TokenUsage{
		InputTokens:         10,
		OutputTokens:        4,
		CacheReadTokens:     6,
		CacheCreationTokens: 2,
	}
	current := TokenUsage{
		InputTokens:         13,
		OutputTokens:        9,
		CacheReadTokens:     8,
		CacheCreationTokens: 2,
	}

	delta := UsageDelta(previous, current)
	if delta.InputTokens != 3 || delta.OutputTokens != 5 || delta.CacheReadTokens != 2 || delta.CacheCreationTokens != 0 {
		t.Fatalf("unexpected delta: %+v", delta)
	}
}

func TestUsageDeltaClampsNegativeFields(t *testing.T) {
	previous := TokenUsage{
		InputTokens:         10,
		OutputTokens:        4,
		CacheReadTokens:     6,
		CacheCreationTokens: 2,
	}
	current := TokenUsage{
		InputTokens:         9,
		OutputTokens:        3,
		CacheReadTokens:     5,
		CacheCreationTokens: 1,
	}

	delta := UsageDelta(previous, current)
	if delta != (TokenUsage{}) {
		t.Fatalf("expected negative deltas to clamp to zero, got %+v", delta)
	}
}
