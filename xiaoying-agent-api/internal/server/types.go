package server

import (
	"github.com/auramate/auramate/internal/message"
)

// ClientMessageType defines the type of message sent by the client
type ClientMessageType string

const (
	ClientMessageTypeChat       ClientMessageType = "chat"
	ClientMessageTypePermission ClientMessageType = "permission_response"
	ClientMessageTypeStop       ClientMessageType = "stop"
	ClientMessageTypeCancelTool ClientMessageType = "cancel_tool" // Phase 5
	ClientMessageTypeReadOnly   ClientMessageType = "set_read_only"
)

// ClientMessage represents the payload from the client
type ClientMessage struct {
	Type ClientMessageType `json:"type"`

	// For Chat
	SessionID       string               `json:"session_id,omitempty"`
	Content         string               `json:"content,omitempty"`
	Attachments     []message.Attachment `json:"attachments,omitempty"`
	EnableSubagents *bool                `json:"enable_subagents,omitempty"`
	Model           string               `json:"model,omitempty"` // Optional model override
	Mode            string               `json:"mode,omitempty"`  // "normal" | "coding"
	ReadOnlyMode    *bool                `json:"read_only_mode,omitempty"`

	// For Permission Response
	PermissionID string `json:"permission_id,omitempty"`
	Action       string `json:"action,omitempty"` // "grant" | "deny"

	// For Tool Interruption (Phase 5)
	ToolExecutionID string `json:"tool_execution_id,omitempty"`
}

// EventType defines the type of event sent to the client
type EventType string

const (
	EventTypeToken            EventType = "token"
	EventTypeToolCall         EventType = "tool_call"
	EventTypeToolResult       EventType = "tool_result"
	EventTypeToolOutput       EventType = "tool_output"
	EventTypeWordEditorAction EventType = "word_editor_action"
	EventTypeError            EventType = "error"
	EventTypeDone             EventType = "done"
	EventTypePing             EventType = "ping"
	EventTypePermission       EventType = "permission_request"
	EventTypeNotification     EventType = "notification"
	EventTypeUsage            EventType = "usage"
	EventTypeContextStats     EventType = "context_stats"
	EventTypeThought          EventType = "thought"
	EventTypeProgress         EventType = "progress"
	EventTypePlanSnapshot     EventType = "plan_snapshot"
	EventTypeReadOnlyUpdated  EventType = "read_only_updated"
)

// Event represents a server-sent event
type Event struct {
	Type            EventType `json:"type"`
	Content         string    `json:"content,omitempty"`
	MessageID       string    `json:"message_id,omitempty"`
	SessionID       string    `json:"session_id,omitempty"`
	ToolExecutionID string    `json:"tool_execution_id,omitempty"` // Phase 5
	Data            any       `json:"data,omitempty"`              // Flexible payload for tool calls, permissions etc.
	Usage           any       `json:"usage,omitempty"`
}

// ErrorResponse represents an error
type ErrorResponse struct {
	Error string `json:"error"`
}
