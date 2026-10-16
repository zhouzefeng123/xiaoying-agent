package config

import (
	"testing"

	"github.com/auramate/auramate/internal/llm/models"
)

func TestResolveMainModelOverrideInheritsConfiguredModelRoute(t *testing.T) {
	originalModel, existed := models.SupportedModels[models.ModelID("doubao-direct")]
	models.SupportedModels[models.ModelID("doubao-direct")] = models.Model{
		ID:                  models.ModelID("doubao-direct"),
		Name:                "Doubao Direct",
		Provider:            models.ProviderOpenAI,
		APIModel:            "DeepSeek-V4-Pro",
		APIKey:              "direct-key",
		BaseURL:             "https://ark.example.com/v1",
		SupportsAttachments: true,
		CanReason:           false,
		DefaultMaxTokens:    4096,
	}
	defer func() {
		if existed {
			models.SupportedModels[models.ModelID("doubao-direct")] = originalModel
			return
		}
		delete(models.SupportedModels, models.ModelID("doubao-direct"))
	}()

	resolved := resolveMainModelOverride(&MainModelConfig{
		Model:     "doubao-direct",
		MaxTokens: 256000,
	})

	if resolved.ID != mainModelOverrideID {
		t.Fatalf("expected custom main model id, got %s", resolved.ID)
	}
	if resolved.Provider != models.ProviderOpenAI {
		t.Fatalf("expected provider to inherit openai, got %s", resolved.Provider)
	}
	if resolved.APIKey != "direct-key" {
		t.Fatalf("expected api key to inherit direct model route, got %q", resolved.APIKey)
	}
	if resolved.BaseURL != "https://ark.example.com/v1" {
		t.Fatalf("expected base url to inherit direct model route, got %q", resolved.BaseURL)
	}
	if !resolved.SupportsAttachments {
		t.Fatalf("expected attachments capability to be inherited")
	}
	if resolved.ContextWindow != 256000 {
		t.Fatalf("expected context window override, got %d", resolved.ContextWindow)
	}
	if resolved.APIModel != "doubao-direct" {
		t.Fatalf("expected api model to follow configured model id, got %q", resolved.APIModel)
	}
}

func TestResolveMainModelOverrideUsesExplicitRouteOverrides(t *testing.T) {
	originalModel, existed := models.SupportedModels[models.ModelID("doubao-direct")]
	models.SupportedModels[models.ModelID("doubao-direct")] = models.Model{
		ID:               models.ModelID("doubao-direct"),
		Provider:         models.ProviderOpenAI,
		APIModel:         "DeepSeek-V4-Pro",
		APIKey:           "direct-key",
		BaseURL:          "https://ark.example.com/v1",
		DefaultMaxTokens: 4096,
	}
	defer func() {
		if existed {
			models.SupportedModels[models.ModelID("doubao-direct")] = originalModel
			return
		}
		delete(models.SupportedModels, models.ModelID("doubao-direct"))
	}()

	resolved := resolveMainModelOverride(&MainModelConfig{
		Model:    "doubao-direct",
		Provider: models.ProviderCustom,
		APIKey:   "override-key",
		BaseURL:  "https://relay.example.com/v1",
	})

	if resolved.Provider != models.ProviderCustom {
		t.Fatalf("expected explicit provider override, got %s", resolved.Provider)
	}
	if resolved.APIKey != "override-key" {
		t.Fatalf("expected explicit api key override, got %q", resolved.APIKey)
	}
	if resolved.BaseURL != "https://relay.example.com/v1" {
		t.Fatalf("expected explicit base url override, got %q", resolved.BaseURL)
	}
	if resolved.APIModel != "doubao-direct" {
		t.Fatalf("expected api model to stay on configured main model, got %q", resolved.APIModel)
	}
}

func TestResolveMainModelOverrideInfersProviderWithoutBaseModel(t *testing.T) {
	resolved := resolveMainModelOverride(&MainModelConfig{
		Model:   "Qwen3.6-35B-A3B",
		BaseURL: "http://localhost:11434/v1",
	})

	if resolved.Provider != models.ProviderCustom {
		t.Fatalf("expected unknown configured model to keep custom provider from fallback route, got %s", resolved.Provider)
	}
	if resolved.APIModel != "Qwen3.6-35B-A3B" {
		t.Fatalf("expected api model to use configured model name, got %q", resolved.APIModel)
	}
	if resolved.CanReason {
		t.Fatalf("expected ad-hoc main model to default can_reason=false")
	}
}

func TestIsMainModelOverrideEnabled(t *testing.T) {
	tests := []struct {
		name      string
		mainModel *MainModelConfig
		want      bool
	}{
		{name: "nil config", mainModel: nil, want: false},
		{name: "empty block", mainModel: &MainModelConfig{Provider: models.ProviderOpenAI, MaxTokens: 256000}, want: false},
		{name: "model enables override", mainModel: &MainModelConfig{Model: "Qwen3.6-35B-A3B"}, want: true},
		{name: "base url enables override", mainModel: &MainModelConfig{BaseURL: "https://relay.example.com/v1"}, want: true},
		{name: "api key enables override", mainModel: &MainModelConfig{APIKey: "secret"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMainModelOverrideEnabled(tt.mainModel); got != tt.want {
				t.Fatalf("isMainModelOverrideEnabled() = %t, want %t", got, tt.want)
			}
		})
	}
}
