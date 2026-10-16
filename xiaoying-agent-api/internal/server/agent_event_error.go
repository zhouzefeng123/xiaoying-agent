package server

import (
	"strings"

	"github.com/auramate/auramate/internal/llm/agent"
)

func agentEventErrorText(event agent.AgentEvent) string {
	if message := strings.TrimSpace(event.Content); message != "" {
		return message
	}
	if event.Error != nil {
		if message := strings.TrimSpace(event.Error.Error()); message != "" {
			return message
		}
	}
	return "未提供具体错误信息"
}
