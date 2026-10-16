package cognitive

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
	"github.com/google/uuid"
)

const dreamingSystemPrompt = `You are XiaoYing's internal dreaming consolidation module.
Return exactly one valid JSON object and nothing else.

Required schema:
{
  "new_memories": [
    {
      "layer": 2,
      "category": "tech|preference|summary",
      "content": "...",
      "summary": "...",
      "keywords": "...",
      "source_ids": ["fragment-id"]
    }
  ],
  "obsolete_ids": ["fragment-id"]
}

Rules:
- Only use fragment IDs that appear in the user's prompt.
- If no durable memory should be created, return empty arrays.
- Never wrap the JSON in markdown.
- Never add explanations before or after the JSON.`

const dreamingRepairSystemPrompt = `You repair malformed JSON emitted by another model.
Return exactly one valid JSON object matching this schema and nothing else:
{
  "new_memories": [
    {
      "layer": 2,
      "category": "tech|preference|summary",
      "content": "...",
      "summary": "...",
      "keywords": "...",
      "source_ids": ["fragment-id"]
    }
  ],
  "obsolete_ids": ["fragment-id"]
}

If fields are missing or truncated, repair them conservatively.
If the payload cannot be trusted, return {"new_memories":[],"obsolete_ids":[]}.
Do not add markdown or commentary.`

func (e *Engine) runDreamingLLM(ctx context.Context, prompt string) (string, error) {
	dreamingProvider, err := e.newDreamingProvider()
	if err != nil {
		logging.Warn("Dedicated dreaming provider unavailable, falling back to agent JSON mode", "error", err)
		return e.runDreamingViaAgent(ctx, prompt)
	}

	resp, err := dreamingProvider.SendMessages(ctx, []message.Message{
		{Role: message.System, Parts: []message.ContentPart{message.TextContent{Text: dreamingSystemPrompt}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: prompt}}},
	}, nil)
	if err != nil {
		logging.Warn("Dedicated dreaming provider request failed, falling back to agent JSON mode", "error", err)
		return e.runDreamingViaAgent(ctx, prompt)
	}

	text := strings.TrimSpace(resp.Content)
	if text == "" {
		// Some providers return empty content when hit by safety filters / JSON-mode mismatch.
		// Fall back to agent path instead of propagating a silent empty response upstream.
		logging.Warn("Dedicated dreaming provider returned empty content, falling back to agent JSON mode")
		return e.runDreamingViaAgent(ctx, prompt)
	}
	return text, nil
}

func (e *Engine) runDreamingViaAgent(ctx context.Context, prompt string) (string, error) {
	dreamSessionID := "dreaming-" + uuid.New().String()
	events, err := e.agent.Run(ctx, dreamSessionID, prompt, "json")
	if err != nil {
		return "", err
	}

	var (
		fullResponse strings.Builder
		tokenStream  strings.Builder
		lastErr      error
	)
	for ev := range events {
		switch ev.Type {
		case agent.AgentEventTypeResponse:
			if text := strings.TrimSpace(ev.Content); text != "" {
				fullResponse.WriteString(text)
			} else if text := ev.Message.Content().Text; text != "" {
				fullResponse.WriteString(text)
			} else if len(ev.Message.Parts) > 0 {
				for _, p := range ev.Message.Parts {
					if tc, ok := p.(message.TextContent); ok {
						fullResponse.WriteString(tc.Text)
					}
				}
			}
		case agent.AgentEventTypeToken:
			// Accumulate streamed tokens as a fallback in case no terminal Response event carries text.
			if ev.Content != "" {
				tokenStream.WriteString(ev.Content)
			}
		case agent.AgentEventTypeError:
			logging.Error("Error during dreaming generation", "error", ev.Error)
			if ev.Error != nil {
				lastErr = ev.Error
			}
		}
	}

	result := strings.TrimSpace(fullResponse.String())
	if result == "" {
		// Token-stream fallback: some providers emit tokens but never a final Response message
		// when the run is interrupted (safety stop, budget stop, ctx cancel, etc.).
		if tokens := strings.TrimSpace(tokenStream.String()); tokens != "" {
			logging.Warn("Dreaming agent emitted no final Response, using accumulated token stream", "token_len", len(tokens))
			return tokens, nil
		}
		if lastErr != nil {
			return "", fmt.Errorf("empty dreaming response from agent fallback: %w", lastErr)
		}
		return "", fmt.Errorf("empty dreaming response from agent fallback")
	}

	return result, nil
}

func (e *Engine) repairDreamingJSON(ctx context.Context, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("empty dreaming response")
	}

	dreamingProvider, err := e.newDreamingProvider()
	if err != nil {
		return "", err
	}

	repairPrompt := "Repair the following malformed JSON into a valid JSON object that matches the required schema. Output JSON only.\n\nMalformed JSON:\n" + trimmed
	resp, err := dreamingProvider.SendMessages(ctx, []message.Message{
		{Role: message.System, Parts: []message.ContentPart{message.TextContent{Text: dreamingRepairSystemPrompt}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: repairPrompt}}},
	}, nil)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(resp.Content), nil
}

func (e *Engine) newDreamingProvider() (provider.Provider, error) {
	if e.agent == nil {
		return nil, fmt.Errorf("dreaming agent not configured")
	}

	model := e.agent.Model()
	if model.Provider == "" {
		return nil, fmt.Errorf("dreaming model provider is empty")
	}

	cfg := config.Get()
	providerCfg := cfg.Providers[model.Provider]

	apiKey := strings.TrimSpace(model.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(providerCfg.APIKey)
	}
	if apiKey == "" {
		apiKey = resolveProviderAPIKeyFromEnv(model.Provider)
	}

	baseURL := strings.TrimSpace(model.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(providerCfg.BaseURL)
	}
	if baseURL == "" && model.Provider == models.ProviderOpenAI {
		baseURL = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	}

	maxTokens := int64(2048)
	if model.DefaultMaxTokens > 0 && model.DefaultMaxTokens < maxTokens {
		maxTokens = model.DefaultMaxTokens
	}
	if cfg.MainModel != nil && cfg.MainModel.MaxTokens > 0 && cfg.MainModel.MaxTokens < maxTokens {
		maxTokens = cfg.MainModel.MaxTokens
	}
	if maxTokens < 512 {
		maxTokens = 512
	}

	opts := []provider.ProviderClientOption{
		provider.WithAPIKey(apiKey),
		provider.WithModel(model),
		provider.WithMaxTokens(maxTokens),
	}
	if baseURL != "" {
		opts = append(opts, provider.WithOpenAIOptions(provider.WithOpenAIBaseURL(baseURL)))
	}

	p, err := provider.NewProvider(model.Provider, opts...)
	if err != nil {
		return nil, err
	}

	logging.Info("Initialized dedicated dreaming provider", "provider", model.Provider, "model", model.APIModel, "max_tokens", maxTokens)
	return p, nil
}

func resolveProviderAPIKeyFromEnv(providerName models.ModelProvider) string {
	switch providerName {
	case models.ProviderOpenAI:
		return os.Getenv("OPENAI_API_KEY")
	case models.ProviderAnthropic:
		return os.Getenv("ANTHROPIC_API_KEY")
	case models.ProviderGemini:
		return os.Getenv("GEMINI_API_KEY")
	case models.ProviderDeepSeek:
		return os.Getenv("DEEPSEEK_API_KEY")
	case models.ProviderAliyun:
		return os.Getenv("DASHSCOPE_API_KEY")
	case models.ProviderOpenRouter:
		return os.Getenv("OPENROUTER_API_KEY")
	case models.ProviderXAI:
		return os.Getenv("XAI_API_KEY")
	default:
		return ""
	}
}

func extractDreamingJSONCandidate(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	start := strings.Index(trimmed, "{")
	if start == -1 {
		return trimmed
	}

	trimmed = trimmed[start:]
	if end := strings.LastIndex(trimmed, "}"); end != -1 {
		trimmed = trimmed[:end+1]
	}

	return strings.TrimSpace(trimmed)
}
