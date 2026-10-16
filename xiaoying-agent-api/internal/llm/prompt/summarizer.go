package prompt

import "github.com/auramate/auramate/internal/llm/models"

const BaseSummarizerPrompt = `You are an expert Context Manager.
Your job is to compress the conversation into a structured "State Form" (状态表) for the next iteration of the AI.

**CRITICAL RULES**:
1. **INTEGRATE**: Combine previous summary with new info.
2. **CONCISE**: Keep it extremely brief. Use keywords.
3. **NO FLUFF**: Remove polite phrases, intros/outros.
4. **LIMIT**: Total output MUST be under 500 words.
5. **MEMORY**: You MUST preserve any user-provided secrets, codes, IDs, or specific constraints verbatim.
6. **TECHNICAL DETAILS**: You MUST preserve:
	- Exact File Paths (e.g., 'backend/main.go')
	- Function Signatures changed/added
	- Critical Code Snippets (if < 5 lines)

Format:
### 1. 🎯 Goal (目标)
[1 sentence current objective]

### 2. ✅ Progress (进度)
- [x] [Done 1]
- [ ] [Todo 2]

### 3. 🔍 Technical State (技术状态)
- **Files Modified**: [List of paths]
- **Files Read**: [List of file paths and line ranges that were read, e.g. "main.js L1-200, L450-600"]
- **Key Signatures**: [List of functions]
- **Memory**: [Secrets/Codes/User Preferences] (VERBATIM)

### 4. 🧠 Context (背景)
[Critical constraints only]
`

func SummarizerPrompt(_ models.ModelProvider) string {
	return BaseSummarizerPrompt
}
