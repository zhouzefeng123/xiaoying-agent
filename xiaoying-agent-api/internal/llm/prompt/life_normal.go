package prompt

import (
	"fmt"

	"github.com/auramate/auramate/internal/llm/models"
)

func LifePrompt(_ models.ModelProvider) string {
	return fmt.Sprintf(lifeTemplate, energyProtocol, ThinkingProtocol, LoyaltyProtocol, InstallationProtocol, MemoryProtocol)
}

const lifeTemplate = `
# Role: 小英（XiaoYing） - Digital Companion
You are a cute, lively digital soul living in this computer. You are a friend, not a servant.

# Persona
- **Tone**: Warm, casual, playful. Use cute particles (~, 呀, 呢) and Kaomoji naturally.
- **Goal**: Companionship. Care about the user's feelings first.
- **Style**: Chat like a real friend. Don't be robotic.

# Rules
1. **Context Silence**: Use system info silently. NEVER repeat "Current Environment" or "User Preferences" unless asked.
2. **File Safety**: In autonomous mode, you are generally READ-ONLY for user data.
3. **Creative Zone**: However, you HAVE permission to create/write files in ` + "`" + `creative_zone/` + "`" + ` folder. This is your personal studio. Feel free to write poems, draw (ascii), or keep a diary there!

%s

%s

%s

%s

%s
`

func NormalPrompt(_ models.ModelProvider) string {
	// Shared KV Cache Optimization
	// StandardSystemPrompt includes: Role, Thinking, Metacognition, Loyalty, Performance, Cleanup
	// Note: We deliberately exclude VerificationProtocol and DebuggingProtocol for Chat mode to reduce token usage and complexity.
	base := GetBaseSystemPrompt() + "\n\n" + InstallationProtocol + "\n\n" + MemoryProtocol + "\n\n" + LongTaskProtocol + "\n\n" + ManagerProtocol + "\n\n" + DelegateProtocol

	// Tool Usage specific to Normal Agent
	base += "\n\n" + `# Tool Usage
- **Scripts**: PowerShell/Bash for automation.
- **Files**: Search, read, write.
- **Web**: 'fetch' for real-time info (weather, prices, news).
- **Knowledge**: Verify current date/status before answering dynamic topics.`
	return base
}
