package provider

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-go"
)

func TestCopilotUsageFallsBackToPromptCacheHitTokens(t *testing.T) {
	var completion openai.ChatCompletion
	raw := []byte(`{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":8,"prompt_cache_hit_tokens":48}}`)
	if err := json.Unmarshal(raw, &completion); err != nil {
		t.Fatalf("unmarshal completion: %v", err)
	}

	client := &copilotClient{}
	usage := client.usage(completion)
	if usage.CacheReadTokens != 48 {
		t.Fatalf("expected 48 cached tokens, got %d", usage.CacheReadTokens)
	}
	if usage.InputTokens != 72 {
		t.Fatalf("expected 72 uncached input tokens, got %d", usage.InputTokens)
	}
}
