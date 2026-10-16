package routing

import (
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
)

const CustomMainModelID models.ModelID = "custom-main-model"

var defaultAutoModelPriority = []models.ModelID{
	models.DeepSeekV4Flash,
	models.DeepSeekV4Pro,
	models.KimiK26,
	models.GLM51,
	models.MiniMaxM27,
}

var visualAutoModelPriority = []models.ModelID{
	models.DoubaoSeed20Pro,
}

type ExecutionPlan struct {
	AgentName       config.AgentName
	Mode            string
	StartLSP        bool
	ModelCandidates []string
}

func ResolveHTTPChatPlan(requestedModel string, agents map[config.AgentName]config.Agent) ExecutionPlan {
	trimmed := strings.TrimSpace(requestedModel)
	plan := ExecutionPlan{
		AgentName: config.AgentCoder,
		Mode:      "coding",
		StartLSP:  true,
	}

	switch trimmed {
	case "aura-life", "life":
		plan.AgentName = config.AgentLife
		plan.Mode = "autonomous"
		plan.StartLSP = false
		plan.ModelCandidates = ResolveAgentModelCandidates(plan.AgentName, "", agents)
		return plan
	case "aura-normal", "normal":
		plan.AgentName = config.AgentNormal
		plan.Mode = "normal"
		plan.StartLSP = false
		plan.ModelCandidates = ResolveAgentModelCandidates(plan.AgentName, "", agents)
		return plan
	case "", "aura-coder", "coder":
		plan.ModelCandidates = ResolveAgentModelCandidates(plan.AgentName, "", agents)
		return plan
	default:
		if _, ok := models.SupportedModels[models.ModelID(trimmed)]; ok {
			plan.ModelCandidates = ResolveAgentModelCandidates(plan.AgentName, trimmed, agents)
			return plan
		}
		plan.ModelCandidates = ResolveAgentModelCandidates(plan.AgentName, "", agents)
		return plan
	}
}

func ResolveAgentModelCandidates(agentName config.AgentName, explicitModel string, agents map[config.AgentName]config.Agent) []string {
	trimmedExplicit := strings.TrimSpace(explicitModel)
	if trimmedExplicit != "" {
		if _, ok := models.SupportedModels[models.ModelID(trimmedExplicit)]; ok {
			return []string{trimmedExplicit}
		}
	}

	candidates := make([]string, 0, len(defaultAutoModelPriority)+2)
	for _, candidate := range defaultAutoCandidates(agentName) {
		if supported := firstSupportedModel(candidate); supported != "" {
			candidates = appendUniqueModel(candidates, supported)
		}
	}

	if len(candidates) == 0 {
		configured := configuredAgentModel(agents, agentName)
		if configured != "" {
			candidates = appendUniqueModel(candidates, configured)
		}
	}

	return candidates
}

func configuredAgentModel(agents map[config.AgentName]config.Agent, agentName config.AgentName) string {
	if agents == nil {
		return ""
	}
	agentCfg, ok := agents[agentName]
	if !ok || agentCfg.Model == "" {
		return ""
	}
	return string(agentCfg.Model)
}

func firstSupportedModel(candidates ...models.ModelID) string {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := models.SupportedModels[candidate]; ok {
			return string(candidate)
		}
	}
	return ""
}

func appendUniqueModel(candidates []string, candidate string) []string {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return candidates
	}
	for _, existing := range candidates {
		if existing == trimmed {
			return candidates
		}
	}
	return append(candidates, trimmed)
}

func defaultAutoCandidates(agentName config.AgentName) []models.ModelID {
	switch agentName {
	case config.AgentVisual:
		return visualAutoModelPriority
	case config.AgentLife, config.AgentNormal, config.AgentCoder, config.AgentTask:
		return defaultAutoModelPriority
	default:
		return nil
	}
}
