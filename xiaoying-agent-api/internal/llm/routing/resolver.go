package routing

import (
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"strings"
)

const CustomMainModelID models.ModelID = "custom-main-model"

type ExecutionPlan struct {
	AgentName       config.AgentName
	Mode            string
	StartLSP        bool
	ModelCandidates []string
}

func ResolveHTTPChatPlan(requestedModel string, agents map[config.AgentName]config.Agent) ExecutionPlan {
	plan := ExecutionPlan{AgentName: config.AgentCoder, Mode: "coding", StartLSP: true}
	switch strings.TrimSpace(requestedModel) {
	case "aura-life", "life":
		plan.AgentName, plan.Mode, plan.StartLSP = config.AgentLife, "autonomous", false
	case "aura-normal", "normal":
		plan.AgentName, plan.Mode, plan.StartLSP = config.AgentNormal, "normal", false
	}
	plan.ModelCandidates = ResolveAgentModelCandidates(plan.AgentName, "", agents)
	return plan
}

// Saved sessions and older clients also use the user's configured model.
func ResolveAgentModelCandidates(_ config.AgentName, _ string, _ map[config.AgentName]config.Agent) []string {
	return []string{string(CustomMainModelID)}
}
