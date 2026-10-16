package routing

import (
	"testing"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
)

func TestResolveAgentModelCandidatesUsesPresetAutoChain(t *testing.T) {
	autoModelID := models.DeepSeekV4Flash
	originalModel, existed := models.SupportedModels[autoModelID]
	models.SupportedModels[autoModelID] = models.Model{
		ID:       autoModelID,
		Provider: models.ProviderOpenAI,
		APIModel: string(autoModelID),
	}
	defer func() {
		if existed {
			models.SupportedModels[autoModelID] = originalModel
			return
		}
		delete(models.SupportedModels, autoModelID)
	}()

	agents := map[config.AgentName]config.Agent{
		config.AgentLife: {Model: models.DeepSeekV4Pro},
		config.AgentTask: {Model: models.DeepSeekV4Pro},
	}

	for _, agentName := range []config.AgentName{config.AgentLife, config.AgentTask} {
		candidates := ResolveAgentModelCandidates(agentName, "", agents)
		if len(candidates) != len(defaultAutoModelPriority) {
			t.Fatalf("expected preset auto candidates for %s, got %v", agentName, candidates)
		}
		if candidates[0] != string(autoModelID) {
			t.Fatalf("expected %s to prefer preset auto chain, got %v", agentName, candidates)
		}
	}
}

func TestResolveAgentModelCandidatesKeepsExplicitModel(t *testing.T) {
	candidates := ResolveAgentModelCandidates(config.AgentCoder, "glm-5.1", nil)
	if len(candidates) != 1 || candidates[0] != "glm-5.1" {
		t.Fatalf("expected explicit model to bypass auto chain, got %v", candidates)
	}
}

func TestResolveAgentModelCandidatesDoesNotFallbackToCustomMainModel(t *testing.T) {
	configuredModelID := models.GLM51
	customMainModelID := models.ModelID("custom-main-model")

	originalConfigured, hadConfigured := models.SupportedModels[configuredModelID]
	originalCustomMain, hadCustomMain := models.SupportedModels[customMainModelID]
	models.SupportedModels[configuredModelID] = models.Model{ID: configuredModelID, Provider: models.ProviderCustom, APIModel: string(configuredModelID)}
	models.SupportedModels[customMainModelID] = models.Model{ID: customMainModelID, Provider: models.ProviderCustom, APIModel: "Qwen3.6-35B-A3B"}
	defer func() {
		if hadConfigured {
			models.SupportedModels[configuredModelID] = originalConfigured
		} else {
			delete(models.SupportedModels, configuredModelID)
		}
		if hadCustomMain {
			models.SupportedModels[customMainModelID] = originalCustomMain
		} else {
			delete(models.SupportedModels, customMainModelID)
		}
	}()

	agents := map[config.AgentName]config.Agent{
		config.AgentTitle: {Model: configuredModelID},
	}

	candidates := ResolveAgentModelCandidates(config.AgentTitle, "", agents)
	if len(candidates) != 1 || candidates[0] != string(configuredModelID) {
		t.Fatalf("expected title agent to stay on configured model without custom-main fallback, got %v", candidates)
	}
}

func TestResolveAgentModelCandidatesUsesOnlyDoubaoForVisual(t *testing.T) {
	autoModelID := models.DoubaoSeed20Pro
	configuredModelID := models.GLM51

	originalAuto, hadAuto := models.SupportedModels[autoModelID]
	originalConfigured, hadConfigured := models.SupportedModels[configuredModelID]
	models.SupportedModels[autoModelID] = models.Model{ID: autoModelID, Provider: models.ProviderCustom, APIModel: string(autoModelID), SupportsAttachments: true}
	models.SupportedModels[configuredModelID] = models.Model{ID: configuredModelID, Provider: models.ProviderCustom, APIModel: string(configuredModelID)}
	defer func() {
		if hadAuto {
			models.SupportedModels[autoModelID] = originalAuto
		} else {
			delete(models.SupportedModels, autoModelID)
		}
		if hadConfigured {
			models.SupportedModels[configuredModelID] = originalConfigured
		} else {
			delete(models.SupportedModels, configuredModelID)
		}
	}()

	agents := map[config.AgentName]config.Agent{
		config.AgentVisual: {Model: configuredModelID},
	}

	candidates := ResolveAgentModelCandidates(config.AgentVisual, "", agents)
	if len(candidates) != len(visualAutoModelPriority) || candidates[0] != string(autoModelID) {
		t.Fatalf("expected visual agent to default to doubao vision route only, got %v", candidates)
	}
}

func TestResolveAgentModelCandidatesDoesNotUseVisionModelForNonVisualAgents(t *testing.T) {
	candidates := ResolveAgentModelCandidates(config.AgentCoder, "", nil)
	for _, candidate := range candidates {
		if candidate == string(models.DoubaoSeed20Pro) {
			t.Fatalf("expected non-visual agent to avoid doubao vision model, got %v", candidates)
		}
	}
}
