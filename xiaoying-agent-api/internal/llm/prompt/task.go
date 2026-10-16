package prompt

import (
	"github.com/auramate/auramate/internal/llm/models"
)

func TaskPrompt(_ models.ModelProvider) string {
	// Shared KV Cache Optimization
	base := GetBaseSystemPrompt()
	base += "\n\n" + ToolingAndSafety
	base += "\n\n" + CodingStandards
	base += "\n\n" + VerificationProtocol
	base += "\n\n" + DebuggingProtocol
	base += "\n\n" + MemoryProtocol
	base += "\n\n" + LongTaskProtocol
	return base
}
