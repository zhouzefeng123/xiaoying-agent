package agent

import "github.com/auramate/auramate/internal/llm/provider"

// SetProvider is a backdoor for testing to inject a mock provider.
// This allows tests to simulate provider errors (like context limits) and verify recovery logic.
func (a *agent) SetProvider(p provider.Provider) {
	a.provider = p
	// Also set summarize provider to the same mock to ensure summaries don't fail due to missing provider
	// or use a real provider if needed. For this test, the mock should handle summary too.
	a.summarizeProvider = p
}
