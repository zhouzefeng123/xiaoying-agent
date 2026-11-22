package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	llmtools "github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
)

func BuildProviderForAgent(agentName config.AgentName, explicitModel string, systemPrompt string, maxTokensOverride int64) (provider.Provider, []string, error) {
	cfg := config.Get()
	agentCfg := cfg.Agents[agentName]
	maxTokens := agentCfg.MaxTokens
	if maxTokensOverride > 0 {
		maxTokens = maxTokensOverride
	}

	return BuildProviderChain(agentName, ResolveAgentModelCandidates(agentName, explicitModel, cfg.Agents), systemPrompt, maxTokens, agentCfg.ReasoningEffort)
}

// BuildProviderForContext uses a request snapshot so concurrent accounts and
// in-flight generations never exchange credentials when a selection changes.
func BuildProviderForContext(ctx context.Context, agentName config.AgentName, systemPrompt string, maxTokensOverride int64) (provider.Provider, error) {
	if config.UserEmailFromContext(ctx) == "" {
		p, _, err := BuildProviderForAgent(agentName, "", systemPrompt, maxTokensOverride)
		return p, err
	}
	model, ok := config.UserRequestModel(ctx)
	if !ok {
		resolved, err := config.ResolveUserRequestModel(ctx, "")
		if err != nil {
			return nil, err
		}
		model, _ = config.UserRequestModel(resolved)
	}
	agentCfg := config.GetAgent(agentName)
	maxTokens := agentCfg.MaxTokens
	if maxTokensOverride > 0 {
		maxTokens = maxTokensOverride
	}
	if maxTokens <= 0 {
		maxTokens = model.DefaultMaxTokens
	}
	baseURL := model.BaseURL
	if baseURL == "" && model.Provider == models.ProviderOpenAI {
		baseURL = "https://api.openai.com/v1"
	}
	if baseURL == "" && model.Provider == models.ProviderOllama {
		baseURL = "http://localhost:11434/v1"
	}
	opts := []provider.ProviderClientOption{provider.WithAPIKey(model.APIKey), provider.WithModel(model), provider.WithMaxTokens(maxTokens)}
	if baseURL != "" {
		opts = append(opts, provider.WithOpenAIOptions(provider.WithOpenAIBaseURL(baseURL)))
	}
	if systemPrompt != "" {
		opts = append(opts, provider.WithSystemMessage(systemPrompt))
	}
	if model.CanReason && agentCfg.ReasoningEffort != "" {
		opts = append(opts, provider.WithOpenAIOptions(provider.WithReasoningEffort(agentCfg.ReasoningEffort)))
	}
	return provider.NewProvider(model.Provider, opts...)
}

func BuildProviderChain(agentName config.AgentName, modelCandidates []string, systemPrompt string, maxTokens int64, reasoningEffort string) (provider.Provider, []string, error) {
	providers := make([]provider.Provider, 0, len(modelCandidates))
	providerModels := make([]string, 0, len(modelCandidates))
	buildErrors := make([]string, 0)

	for _, modelID := range modelCandidates {
		targetProvider, err := buildProviderForModelID(agentName, modelID, systemPrompt, maxTokens, reasoningEffort)
		if err != nil {
			logging.Warn("Skipping candidate model", "agent", agentName, "model", modelID, "error", err)
			buildErrors = append(buildErrors, fmt.Sprintf("%s: %v", modelID, err))
			continue
		}
		providers = append(providers, targetProvider)
		providerModels = append(providerModels, modelID)
	}

	if len(providers) == 0 {
		if len(buildErrors) == 0 {
			return nil, nil, errors.New("no candidate models configured")
		}
		return nil, nil, fmt.Errorf("failed to build provider chain: %s", strings.Join(buildErrors, "; "))
	}

	if len(providers) == 1 {
		return providers[0], providerModels, nil
	}

	logging.Info("Built provider fallback chain", "agent", agentName, "models", strings.Join(providerModels, ","))
	return newFallbackProvider(providers, providerModels), providerModels, nil
}

func buildProviderForModelID(agentName config.AgentName, modelID string, systemPrompt string, maxTokens int64, reasoningEffort string) (provider.Provider, error) {
	targetModel, exists := models.SupportedModels[models.ModelID(modelID)]
	if models.ModelID(modelID) == CustomMainModelID {
		targetModel, exists = config.CurrentMainModel()
		if !exists {
			return nil, errors.New("请先在设置中配置自定义模型的名称、接口地址和 API 密钥")
		}
	}
	if !exists {
		return nil, fmt.Errorf("model %s not supported", modelID)
	}

	cfg := config.Get()
	providerCfg, hasProvider := cfg.Providers[targetModel.Provider]
	isCustomOrLocal := targetModel.Provider == models.ProviderOllama || targetModel.Provider == models.ProviderLocal || targetModel.Provider == models.ProviderCustom
	if !hasProvider && !isCustomOrLocal && targetModel.APIKey == "" && targetModel.BaseURL == "" {
		return nil, fmt.Errorf("provider %s not configured", targetModel.Provider)
	}
	if hasProvider && providerCfg.Disabled && targetModel.APIKey == "" && targetModel.BaseURL == "" {
		return nil, fmt.Errorf("provider %s is disabled", targetModel.Provider)
	}

	apiKey := providerCfg.APIKey
	if targetModel.APIKey != "" {
		apiKey = targetModel.APIKey
	}

	baseURL := providerCfg.BaseURL
	if targetModel.BaseURL != "" {
		baseURL = targetModel.BaseURL
	}
	if models.ModelID(modelID) == CustomMainModelID {
		// Empty values are intentional: never reuse platform or provider credentials.
		apiKey = targetModel.APIKey
		baseURL = targetModel.BaseURL
		if baseURL == "" && targetModel.Provider == models.ProviderOpenAI {
			baseURL = "https://api.openai.com/v1"
		}
	}

	effectiveMaxTokens := maxTokens
	if effectiveMaxTokens <= 0 {
		effectiveMaxTokens = targetModel.DefaultMaxTokens
	}
	if effectiveMaxTokens <= 0 {
		effectiveMaxTokens = 8192
	}

	opts := []provider.ProviderClientOption{
		provider.WithAPIKey(apiKey),
		provider.WithModel(targetModel),
		provider.WithMaxTokens(effectiveMaxTokens),
	}
	if systemPrompt != "" {
		opts = append(opts, provider.WithSystemMessage(systemPrompt))
	}
	if baseURL != "" {
		opts = append(opts, provider.WithOpenAIOptions(provider.WithOpenAIBaseURL(baseURL)))
	}

	if (targetModel.Provider == models.ProviderOpenAI || targetModel.Provider == models.ProviderLocal || targetModel.Provider == models.ProviderOllama || targetModel.Provider == models.ProviderCustom) && targetModel.CanReason && reasoningEffort != "" {
		opts = append(opts, provider.WithOpenAIOptions(provider.WithReasoningEffort(reasoningEffort)))
	}
	if targetModel.Provider == models.ProviderAnthropic && targetModel.CanReason && agentName == config.AgentCoder {
		opts = append(opts, provider.WithAnthropicOptions(provider.WithAnthropicShouldThinkFn(provider.DefaultShouldThinkFn)))
	}

	return provider.NewProvider(targetModel.Provider, opts...)
}

type fallbackProvider struct {
	providers []provider.Provider
	modelIDs  []string
}

func newFallbackProvider(providers []provider.Provider, modelIDs []string) provider.Provider {
	trimmedModels := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		trimmedModels = append(trimmedModels, strings.TrimSpace(modelID))
	}
	return &fallbackProvider{providers: providers, modelIDs: trimmedModels}
}

func (p *fallbackProvider) Model() models.Model {
	if len(p.providers) == 0 {
		return models.Model{}
	}
	return p.providers[0].Model()
}

func (p *fallbackProvider) SendMessages(ctx context.Context, msgs []message.Message, tools []llmtools.BaseTool) (*provider.ProviderResponse, error) {
	var lastErr error
	for idx, currentProvider := range p.providers {
		response, err := currentProvider.SendMessages(ctx, msgs, tools)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if idx < len(p.providers)-1 {
			logging.Warn("Fallback provider send failed before completion, trying next model", "model", p.modelAt(idx), "error", err)
		}
	}
	return nil, lastErr
}

func (p *fallbackProvider) StreamResponse(ctx context.Context, msgs []message.Message, tools []llmtools.BaseTool) <-chan provider.ProviderEvent {
	out := make(chan provider.ProviderEvent, 64)
	go func() {
		defer close(out)

		for idx, currentProvider := range p.providers {
			eventChan := currentProvider.StreamResponse(ctx, msgs, tools)
			buffered := make([]provider.ProviderEvent, 0, 8)
			var contentBuffer strings.Builder
			committed := false
			retryNext := false

			for event := range eventChan {
				if committed {
					out <- event
					continue
				}

				buffered = append(buffered, event)
				if shouldCommitFallbackEvent(event, &contentBuffer) {
					committed = true
					for _, pending := range buffered {
						out <- pending
					}
					buffered = nil
					continue
				}

				if event.Type == provider.EventError {
					if idx < len(p.providers)-1 {
						logging.Warn("Fallback provider stream failed before substantive output, trying next model", "model", p.modelAt(idx), "error", event.Error)
						retryNext = true
					}
					break
				}
			}

			if retryNext {
				continue
			}

			if !committed && idx < len(p.providers)-1 {
				logging.Warn("Fallback provider produced no substantive output, trying next model", "model", p.modelAt(idx))
				continue
			}

			if !committed {
				for _, pending := range buffered {
					out <- pending
				}
			}
			return
		}
	}()
	return out
}

func (p *fallbackProvider) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	if len(p.providers) == 0 {
		return "", nil
	}
	return p.providers[0].Transcribe(ctx, audio, filename)
}

func (p *fallbackProvider) GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	if len(p.providers) == 0 {
		return nil, nil
	}
	return p.providers[0].GenerateSpeech(ctx, text, voice)
}

func (p *fallbackProvider) modelAt(index int) string {
	if index < 0 || index >= len(p.modelIDs) {
		return ""
	}
	return p.modelIDs[index]
}

func shouldCommitFallbackEvent(event provider.ProviderEvent, contentBuffer *strings.Builder) bool {
	switch event.Type {
	case provider.EventContentDelta:
		contentBuffer.WriteString(event.Content)
		return strings.TrimSpace(contentBuffer.String()) != ""
	case provider.EventToolUseStart, provider.EventToolUseDelta, provider.EventToolUseStop:
		return true
	case provider.EventComplete:
		if event.Response == nil {
			return false
		}
		if strings.TrimSpace(event.Response.Content) != "" || len(event.Response.ToolCalls) > 0 {
			return true
		}
		return false
	default:
		return false
	}
}
