package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/auramate/auramate/internal/pubsub"
)

const (
	SendNotificationToolName    = "send_notification"
	sendNotificationDescription = `Send a proactive notification to the user.
Use this tool when you want to alert the user about something important without waiting for them to ask.
The notification will appear in the user's UI.
`
)

type SendNotificationParams struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Type     string `json:"type"`     // "info", "success", "warning", "error"
	Priority string `json:"priority"` // "low", "medium", "high"
}

type sendNotificationTool struct {
	broker *pubsub.Broker[interface{}] // We need a way to send events to the server/frontend
}

// We need a way to emit the notification event.
// The AgentEvent struct has a "Type" but it's specific to Agent events.
// The Server listens to Agent events.
// We should define a new AgentEventType for Notification.

// Let's check how Agent events are propagated.
// Agent emits AgentEvent -> Server.go handles it -> Sends to WebSocket.

// So we just need this tool to return a specific result or side-effect that the Agent converts to an event?
// OR, we can make the tool return a special string and the Agent detects it?
// Better: The Agent has a `Broker`. But the Tool doesn't have access to the Agent's broker directly unless passed.

// ALTERNATIVE: The tool returns a structured response, and the Agent (in agent.go) detects this tool usage and emits a notification event.
// OR, we pass a callback to the tool.

// Let's stick to the standard Tool interface.
// If the tool returns "Notification sent: ...", that's just text.
// We need the Frontend to receive a "notification" event.

// In `server.go`, it handles `AgentEventTypeResponse`.
// We might need to add `AgentEventTypeNotification` to `agent.go`.

func NewSendNotificationTool() BaseTool {
	return &sendNotificationTool{}
}

func (t *sendNotificationTool) Info() ToolInfo {
	return ToolInfo{
		Name:        SendNotificationToolName,
		Description: sendNotificationDescription,
		Parameters: map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "The title of the notification",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "The content of the notification",
			},
			"type": map[string]any{
				"type":        "string",
				"description": "Type of notification: info, success, warning, error",
				"enum":        []string{"info", "success", "warning", "error"},
			},
		},
		Required: []string{"title", "message"},
	}
}

func (t *sendNotificationTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params SendNotificationParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	// For now, we return a special metadata tag that the Agent or Server can parse.
	// But a cleaner way is to use the context to send a signal if possible,
	// or rely on the Agent to see "send_notification" was called and emit an event.

	// Actually, `agent.go` iterates over tool results.
	// We can define a convention or add a field to ToolResponse?
	// ToolResponse has `Metadata`.

	metadata := map[string]string{
		"action":   "notification",
		"title":    params.Title,
		"message":  params.Message,
		"type":     params.Type,
		"priority": params.Priority,
	}
	metaJson, _ := json.Marshal(metadata)

	return ToolResponse{
		Type:     ToolResponseTypeText,
		Content:  fmt.Sprintf("Notification sent: %s - %s", params.Title, params.Message),
		Metadata: string(metaJson),
	}, nil
}
