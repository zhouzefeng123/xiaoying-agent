package server

import (
	"errors"
	"testing"

	"github.com/auramate/auramate/internal/llm/agent"
)

func TestAgentEventErrorTextUsesContentFirst(t *testing.T) {
	event := agent.AgentEvent{Content: "显式错误内容", Error: errors.New("底层错误")}
	if got := agentEventErrorText(event); got != "显式错误内容" {
		t.Fatalf("expected explicit content, got %q", got)
	}
}

func TestAgentEventErrorTextFallsBackToError(t *testing.T) {
	event := agent.AgentEvent{Error: errors.New("真实失败原因")}
	if got := agentEventErrorText(event); got != "真实失败原因" {
		t.Fatalf("expected fallback error text, got %q", got)
	}
}

func TestAgentEventErrorTextProvidesDefaultMessage(t *testing.T) {
	event := agent.AgentEvent{}
	if got := agentEventErrorText(event); got != "未提供具体错误信息" {
		t.Fatalf("expected default error text, got %q", got)
	}
}
