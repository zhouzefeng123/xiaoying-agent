package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
)

type EventType string

const maxRetries = 8

const (
	EventContentStart  EventType = "content_start"
	EventToolUseStart  EventType = "tool_use_start"
	EventToolUseDelta  EventType = "tool_use_delta"
	EventToolUseStop   EventType = "tool_use_stop"
	EventContentDelta  EventType = "content_delta"
	EventThinkingDelta EventType = "thinking_delta"
	EventContentStop   EventType = "content_stop"
	EventUsage         EventType = "usage"
	EventComplete      EventType = "complete"
	EventError         EventType = "error"
	EventWarning       EventType = "warning"
)

type TokenUsage struct {
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
}

func (u TokenUsage) PromptTokens() int64 {
	return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens
}

func (u TokenUsage) TotalTokens() int64 {
	return u.PromptTokens() + u.OutputTokens
}

func UsageDelta(previous TokenUsage, current TokenUsage) TokenUsage {
	delta := TokenUsage{
		InputTokens:         current.InputTokens - previous.InputTokens,
		OutputTokens:        current.OutputTokens - previous.OutputTokens,
		CacheCreationTokens: current.CacheCreationTokens - previous.CacheCreationTokens,
		CacheReadTokens:     current.CacheReadTokens - previous.CacheReadTokens,
	}
	if delta.InputTokens < 0 {
		delta.InputTokens = 0
	}
	if delta.OutputTokens < 0 {
		delta.OutputTokens = 0
	}
	if delta.CacheCreationTokens < 0 {
		delta.CacheCreationTokens = 0
	}
	if delta.CacheReadTokens < 0 {
		delta.CacheReadTokens = 0
	}
	return delta
}

type ProviderResponse struct {
	Content      string
	Thinking     string
	ToolCalls    []message.ToolCall
	Usage        TokenUsage
	FinishReason message.FinishReason
}

type ProviderEvent struct {
	Type EventType

	Content  string
	Thinking string
	Usage    *TokenUsage
	Response *ProviderResponse
	ToolCall *message.ToolCall
	Error    error
}
type Provider interface {
	SendMessages(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error)

	StreamResponse(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent

	Model() models.Model

	Transcribe(ctx context.Context, audio []byte, filename string) (string, error)
	GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error)
}

type providerClientOptions struct {
	apiKey        string
	model         models.Model
	maxTokens     int64
	systemMessage string

	anthropicOptions []AnthropicOption
	openaiOptions    []OpenAIOption
	geminiOptions    []GeminiOption
	bedrockOptions   []BedrockOption
	copilotOptions   []CopilotOption
}

type ProviderClientOption func(*providerClientOptions)

type ProviderClient interface {
	send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error)
	stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent
	transcribe(ctx context.Context, audio []byte, filename string) (string, error)
	generateSpeech(ctx context.Context, text string, voice string) ([]byte, error)
}

type baseProvider[C ProviderClient] struct {
	options providerClientOptions
	client  C
}

func (p *baseProvider[C]) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	return p.client.transcribe(ctx, audio, filename)
}

func (p *baseProvider[C]) GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	return p.client.generateSpeech(ctx, text, voice)
}

func NewProvider(providerName models.ModelProvider, opts ...ProviderClientOption) (Provider, error) {
	clientOptions := providerClientOptions{}
	for _, o := range opts {
		o(&clientOptions)
	}
	switch providerName {
	case models.ProviderCopilot:
		return &baseProvider[CopilotClient]{
			options: clientOptions,
			client:  newCopilotClient(clientOptions),
		}, nil
	case models.ProviderAnthropic:
		return &baseProvider[AnthropicClient]{
			options: clientOptions,
			client:  newAnthropicClient(clientOptions),
		}, nil
	case models.ProviderMock:
		return &baseProvider[*MockClient]{
			options: clientOptions,
			client:  newMockClient(clientOptions),
		}, nil
	case models.ProviderOpenAI:
		return &baseProvider[OpenAIClient]{
			options: clientOptions,
			client:  newOpenAIClient(clientOptions),
		}, nil
	case models.ProviderGemini:
		return &baseProvider[GeminiClient]{
			options: clientOptions,
			client:  newGeminiClient(clientOptions),
		}, nil
	case models.ProviderBedrock:
		return &baseProvider[BedrockClient]{
			options: clientOptions,
			client:  newBedrockClient(clientOptions),
		}, nil
	case models.ProviderGROQ:
		clientOptions.openaiOptions = append(clientOptions.openaiOptions,
			WithOpenAIBaseURL("https://api.groq.com/openai/v1"),
		)
		return &baseProvider[OpenAIClient]{
			options: clientOptions,
			client:  newOpenAIClient(clientOptions),
		}, nil
	case models.ProviderAzure:
		return &baseProvider[AzureClient]{
			options: clientOptions,
			client:  newAzureClient(clientOptions),
		}, nil
	case models.ProviderVertexAI:
		return &baseProvider[VertexAIClient]{
			options: clientOptions,
			client:  newVertexAIClient(clientOptions),
		}, nil
	case models.ProviderOpenRouter:
		clientOptions.openaiOptions = append(clientOptions.openaiOptions,
			WithOpenAIBaseURL("https://openrouter.ai/api/v1"),
			WithOpenAIExtraHeaders(map[string]string{
				"HTTP-Referer": "opencode.ai",
				"X-Title":      "OpenCode",
			}),
		)
		return &baseProvider[OpenAIClient]{
			options: clientOptions,
			client:  newOpenAIClient(clientOptions),
		}, nil
	case models.ProviderXAI:
		clientOptions.openaiOptions = append(clientOptions.openaiOptions,
			WithOpenAIBaseURL("https://api.x.ai/v1"),
		)
		return &baseProvider[OpenAIClient]{
			options: clientOptions,
			client:  newOpenAIClient(clientOptions),
		}, nil
	case models.ProviderDeepSeek:
		baseURL := "https://api.deepseek.com"
		if clientOptions.openaiOptions != nil {
			// Check if base URL is already set in options
			// Note: This logic assumes options are processed in order
		}
		clientOptions.openaiOptions = append(clientOptions.openaiOptions,
			WithOpenAIBaseURL(baseURL),
		)
		return &baseProvider[DeepSeekClient]{
			options: clientOptions,
			client:  newDeepSeekClient(clientOptions),
		}, nil
	case models.ProviderAliyun:
		// Default Aliyun Bailian endpoint
		baseURL := "https://dashscope.aliyuncs.com/compatible-mode/v1"
		clientOptions.openaiOptions = append(clientOptions.openaiOptions,
			WithOpenAIBaseURL(baseURL),
		)

		// Set Provider explicitly to ensure internal checks (like in generateSpeech) work correctly
		clientOptions.model.Provider = models.ProviderAliyun

		// Ensure model is set in client options if provided
		if clientOptions.model.APIModel == "" {
			// This shouldn't happen if config loading is correct, but as a safeguard
			logging.Debug("Aliyun provider initialized with empty APIModel, falling back to qwen3.6-plus")
			clientOptions.model.APIModel = "qwen3.6-plus"
		}

		return &baseProvider[OpenAIClient]{
			options: clientOptions,
			client:  newOpenAIClient(clientOptions),
		}, nil
	case models.ProviderOllama:
		// Default Ollama endpoint, but allow override via options
		baseURL := "http://localhost:11434/v1"
		clientOptions.openaiOptions = append(clientOptions.openaiOptions,
			WithOpenAIBaseURL(baseURL),
		)
		return &baseProvider[OpenAIClient]{
			options: clientOptions,
			client:  newOpenAIClient(clientOptions),
		}, nil
	case models.ProviderCustom:
		// Custom provider is just OpenAI compatible with no default BaseURL (must be provided)
		return &baseProvider[OpenAIClient]{
			options: clientOptions,
			client:  newOpenAIClient(clientOptions),
		}, nil
	case models.ProviderLocal:
		clientOptions.openaiOptions = append(clientOptions.openaiOptions,
			WithOpenAIBaseURL(os.Getenv("LOCAL_ENDPOINT")),
		)
		return &baseProvider[OpenAIClient]{
			options: clientOptions,
			client:  newOpenAIClient(clientOptions),
		}, nil
	}
	return nil, fmt.Errorf("provider not supported: %s", providerName)
}

func (p *baseProvider[C]) cleanMessages(messages []message.Message) (cleaned []message.Message) {
	for _, msg := range messages {
		// The message has no content
		if len(msg.Parts) == 0 {
			continue
		}
		cleaned = append(cleaned, msg)
	}
	return
}

func (p *baseProvider[C]) SendMessages(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error) {
	return p.client.send(ctx, messages, tools)
}

func (p *baseProvider[C]) Model() models.Model {
	return p.options.model
}

func (p *baseProvider[C]) StreamResponse(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	return p.client.stream(ctx, messages, tools)
}

func WithAPIKey(apiKey string) ProviderClientOption {
	return func(options *providerClientOptions) {
		options.apiKey = apiKey
	}
}

func WithModel(model models.Model) ProviderClientOption {
	return func(options *providerClientOptions) {
		options.model = model
	}
}

func WithMaxTokens(maxTokens int64) ProviderClientOption {
	return func(options *providerClientOptions) {
		options.maxTokens = maxTokens
	}
}

func WithSystemMessage(systemMessage string) ProviderClientOption {
	return func(options *providerClientOptions) {
		options.systemMessage = systemMessage
	}
}

func WithAnthropicOptions(anthropicOptions ...AnthropicOption) ProviderClientOption {
	return func(options *providerClientOptions) {
		options.anthropicOptions = anthropicOptions
	}
}

func WithOpenAIOptions(openaiOptions ...OpenAIOption) ProviderClientOption {
	return func(options *providerClientOptions) {
		options.openaiOptions = append(options.openaiOptions, openaiOptions...)
	}
}

func WithGeminiOptions(geminiOptions ...GeminiOption) ProviderClientOption {
	return func(options *providerClientOptions) {
		options.geminiOptions = geminiOptions
	}
}

func WithBedrockOptions(bedrockOptions ...BedrockOption) ProviderClientOption {
	return func(options *providerClientOptions) {
		options.bedrockOptions = bedrockOptions
	}
}

func WithCopilotOptions(copilotOptions ...CopilotOption) ProviderClientOption {
	return func(options *providerClientOptions) {
		options.copilotOptions = copilotOptions
	}
}
