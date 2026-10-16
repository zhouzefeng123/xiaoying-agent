package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	ManageCognitiveStateToolName    = "manage_cognitive_state"
	manageCognitiveStateDescription = `Manages the cognitive state of the Life Agent.
Usage:
- action: "get_state", "set_state".
- state: The target state to transition to (required for 'set_state').
  Valid states: 'interactive' (Chat), 'reflective' (Summary), 'exploratory' (Learning), 'dreaming' (Consolidation), 'free_activity' (Autonomous Action), 'dormant' (Sleep/Tool Mode).

Use this tool to:
1. Check your current state and budget.
2. Decide to sleep (dormant) if budget is low.
3. Decide to reflect, explore, or engage in free activity if you have free time.
`
)

type CognitiveController interface {
	GetState(ctx context.Context) (string, int64, int64, error) // state, usage, limit
	TransitionTo(ctx context.Context, newState string)
}

type ManageCognitiveStateParams struct {
	Action string `json:"action"` // "get_state", "set_state"
	State  string `json:"state,omitempty"`
}

type manageCognitiveStateTool struct {
	controller CognitiveController
}

func NewManageCognitiveStateTool(controller CognitiveController) BaseTool {
	return &manageCognitiveStateTool{
		controller: controller,
	}
}

func (t *manageCognitiveStateTool) Info() ToolInfo {
	return ToolInfo{
		Name:        ManageCognitiveStateToolName,
		Description: manageCognitiveStateDescription,
		Parameters: map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action to perform: 'get_state' or 'set_state'",
				"enum":        []string{"get_state", "set_state"},
			},
			"state": map[string]any{
				"type":        "string",
				"description": "Target state (required for 'set_state'): 'interactive', 'reflective', 'exploratory', 'dreaming', 'dormant'",
				"enum":        []string{"interactive", "reflective", "exploratory", "dreaming", "dormant"},
			},
		},
		Required: []string{"action"},
	}
}

func (t *manageCognitiveStateTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params ManageCognitiveStateParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	switch params.Action {
	case "get_state":
		state, usage, limit, err := t.controller.GetState(ctx)
		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("failed to get state: %v", err)), nil
		}
		return NewTextResponse(fmt.Sprintf("Current State: %s\nDaily Token Usage: %d\nDaily Token Limit: %d\nRemaining: %d", state, usage, limit, limit-usage)), nil

	case "set_state":
		if params.State == "" {
			return NewTextErrorResponse("state is required for 'set_state'"), nil
		}

		validStates := map[string]bool{
			"interactive":   true,
			"reflective":    true,
			"exploratory":   true,
			"dreaming":      true,
			"free_activity": true,
			"dormant":       true,
			"waking":        true,
		}

		if !validStates[params.State] {
			return NewTextErrorResponse(fmt.Sprintf("Invalid state: %s. Valid states are: interactive, reflective, exploratory, dreaming, free_activity, dormant", params.State)), nil
		}

		t.controller.TransitionTo(ctx, params.State)
		return NewTextResponse(fmt.Sprintf("Transitioned to state: %s", params.State)), nil

	default:
		return NewTextErrorResponse("Invalid action. Must be 'get_state' or 'set_state'."), nil
	}
}
