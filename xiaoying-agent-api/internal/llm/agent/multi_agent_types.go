package agent

import (
	"context"
	"time"

	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/message"
)

// MultiAgentEventType defines the type of event in the multi-agent bus
type MultiAgentEventType string

const (
	MAEventMessage      MultiAgentEventType = "message"
	MAEventToolCall     MultiAgentEventType = "tool_call"
	MAEventToolResult   MultiAgentEventType = "tool_result"
	MAEventSystem       MultiAgentEventType = "system"
	MAEventTurnComplete MultiAgentEventType = "turn_complete"
	MAEventHeartbeat    MultiAgentEventType = "heartbeat"
)

// MultiAgentEvent represents a message on the multi-agent bus
type MultiAgentEvent struct {
	ID         string               `json:"ID"`
	Type       MultiAgentEventType  `json:"Type"`
	From       string               `json:"From"` // Agent ID
	To         string               `json:"To"`   // Target Agent ID or "*" or "topic:xxx"
	TaskID     string               `json:"TaskID,omitempty"`
	Content    string               `json:"Content"`
	ToolCalls  []message.ToolCall   `json:"ToolCalls,omitempty"`
	ToolResult *message.ToolResult  `json:"ToolResult,omitempty"`
	Usage      *provider.TokenUsage `json:"Usage,omitempty"`
	Timestamp  time.Time            `json:"Timestamp"`
}

// SubAgent represents an autonomous agent in the swarm
type SubAgent interface {
	ID() string
	Role() string
	Run(ctx context.Context) error
	Stop()
}

// MultiAgentOrchestrator manages the agent swarm
type MultiAgentOrchestrator interface {
	Start(ctx context.Context) error
	SpawnAgent(role string, systemPrompt string) (string, error)
	Broadcast(content string)
	Send(to string, content string)
	Shutdown()
}
