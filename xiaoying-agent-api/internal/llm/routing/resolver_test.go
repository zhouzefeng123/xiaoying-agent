package routing

import (
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"testing"
)

func TestAllAgentsAndLegacySelectionsUseConfiguredModel(t *testing.T) {
	agents := map[config.AgentName]config.Agent{config.AgentTitle: {Model: models.GLM51}}
	for _, name := range []config.AgentName{config.AgentCoder, config.AgentNormal, config.AgentLife, config.AgentTask, config.AgentTitle, config.AgentSummarizer, config.AgentVisual} {
		for _, selected := range []string{"", "auto", "custom-main-model", "deepseek-v4-flash", "glm-5.1"} {
			candidates := ResolveAgentModelCandidates(name, selected, agents)
			if len(candidates) != 1 || candidates[0] != string(CustomMainModelID) {
				t.Fatalf("%s/%s routed to %v", name, selected, candidates)
			}
		}
	}
}

func TestHTTPChatPreservesAgentModeWithConfiguredModel(t *testing.T) {
	for _, test := range []struct {
		model string
		agent config.AgentName
		mode  string
		lsp   bool
	}{
		{"aura-normal", config.AgentNormal, "normal", false},
		{"life", config.AgentLife, "autonomous", false},
		{"custom-main-model", config.AgentCoder, "coding", true},
		{"deepseek-v4-flash", config.AgentCoder, "coding", true},
	} {
		plan := ResolveHTTPChatPlan(test.model, nil)
		if plan.AgentName != test.agent || plan.Mode != test.mode || plan.StartLSP != test.lsp || len(plan.ModelCandidates) != 1 || plan.ModelCandidates[0] != string(CustomMainModelID) {
			t.Fatalf("unexpected plan: %+v", plan)
		}
	}
}
