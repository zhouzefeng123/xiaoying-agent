package models

import "maps"

type (
	ModelID       string
	ModelProvider string
)

type Model struct {
	ID                  ModelID       `json:"id" mapstructure:"id"`
	Name                string        `json:"name" mapstructure:"name"`
	Provider            ModelProvider `json:"provider" mapstructure:"provider"`
	APIModel            string        `json:"api_model" mapstructure:"api_model"`
	CostPer1MIn         float64       `json:"cost_per_1m_in" mapstructure:"cost_per_1m_in"`
	CostPer1MOut        float64       `json:"cost_per_1m_out" mapstructure:"cost_per_1m_out"`
	CostPer1MInCached   float64       `json:"cost_per_1m_in_cached" mapstructure:"cost_per_1m_in_cached"`
	CostPer1MOutCached  float64       `json:"cost_per_1m_out_cached" mapstructure:"cost_per_1m_out_cached"`
	ContextWindow       int64         `json:"context_window" mapstructure:"context_window"`
	DefaultMaxTokens    int64         `json:"default_max_tokens" mapstructure:"default_max_tokens"`
	DefaultTemperature  *float64      `json:"default_temperature,omitempty" mapstructure:"default_temperature"`
	CanReason           bool          `json:"can_reason" mapstructure:"can_reason"`
	SupportsAttachments bool          `json:"supports_attachments" mapstructure:"supports_attachments"`
	APIKey              string        `json:"api_key,omitempty" mapstructure:"api_key"`
	BaseURL             string        `json:"base_url,omitempty" mapstructure:"base_url"`
}

// Model IDs
const ( // GEMINI
	// Bedrock
	BedrockClaude37Sonnet ModelID = "bedrock.claude-3.7-sonnet"

	// Mock
	MockGPT ModelID = "mock-gpt"
)

const (
	ProviderBedrock  ModelProvider = "bedrock"
	ProviderDeepSeek ModelProvider = "deepseek"
	ProviderAliyun   ModelProvider = "aliyun"
	ProviderOllama   ModelProvider = "ollama"
	ProviderCustom   ModelProvider = "custom"
	// ForTests
	ProviderMock ModelProvider = "__mock"
)

// Providers in order of popularity
var ProviderPopularity = map[ModelProvider]int{
	ProviderCopilot:    1,
	ProviderAnthropic:  2,
	ProviderOpenAI:     3,
	ProviderDeepSeek:   4,
	ProviderAliyun:     5,
	ProviderGemini:     6,
	ProviderGROQ:       7,
	ProviderOpenRouter: 8,
	ProviderBedrock:    9,
	ProviderAzure:      10,
	ProviderVertexAI:   11,
}

var SupportedModels = make(map[ModelID]Model)

func init() {
	maps.Copy(SupportedModels, AnthropicModels)
	maps.Copy(SupportedModels, OpenAIModels)
	maps.Copy(SupportedModels, DeepSeekModels)
	maps.Copy(SupportedModels, AliyunModels)
	maps.Copy(SupportedModels, AuraAuthProxyModels)
	maps.Copy(SupportedModels, GeminiModels)
	maps.Copy(SupportedModels, GroqModels)
	maps.Copy(SupportedModels, AzureModels)
	maps.Copy(SupportedModels, OpenRouterModels)
	maps.Copy(SupportedModels, XAIModels)
	maps.Copy(SupportedModels, VertexAIGeminiModels)
	maps.Copy(SupportedModels, CopilotModels)
}
