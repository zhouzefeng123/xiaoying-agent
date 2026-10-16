package prompt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
)

func TestPromptVerification(t *testing.T) {
	config.InitializeMock()
	provider := models.ModelProvider("test-provider")

	tests := []struct {
		name     string
		promptFn func(models.ModelProvider) string
		mustHave []string
		mustNot  []string
	}{
		{
			name:     "CoderPrompt",
			promptFn: CoderPrompt,
			mustHave: []string{
				"Role: 小英（XiaoYing）",
				"# Thinking",
				"Loyalty",
				"Standards",
			},
			mustNot: []string{
				"Role: auramate",
			},
		},
		{
			name:     "NormalPrompt",
			promptFn: NormalPrompt,
			mustHave: []string{
				"Role: 小英（XiaoYing）",
				"# Thinking",
				"Loyalty",
				"Tool Usage", // Specific to Normal
				"Manager Mode",
				"Delegation Protocol",
			},
			mustNot: []string{
				"Role: auramate",
				"Standards", // Coder specific? Wait, Coder has "CodingStandards". Normal shouldn't have it?
				// Actually StandardSystemPrompt doesn't have "Standards". Coder appends it.
				// Normal appends "Tool Usage".
			},
		},
		{
			name:     "LifePrompt",
			promptFn: LifePrompt,
			mustHave: []string{
				"Role: 小英（XiaoYing）",
				"**Tone**: Warm", // Fixed expectation
				"# Thinking",     // Shared protocol
			},
			mustNot: []string{
				"Role: auramate",
				"Tooling & Safety", // Coder specific
			},
		},
		{
			name:     "TaskPrompt",
			promptFn: TaskPrompt,
			mustHave: []string{
				"Role: 小英（XiaoYing）", // Uses StandardSystemPrompt
				"Long Tasks",
				"Memory",
			},
			mustNot: []string{
				"Role: auramate",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.promptFn(provider)

			// Visual Inspection (First 200 chars)
			fmt.Printf("\n=== %s Head ===\n%s\n...\n", tt.name, got[:min(len(got), 300)])

			for _, s := range tt.mustHave {
				if !strings.Contains(got, s) {
					t.Errorf("%s missing required string: %q", tt.name, s)
				}
			}

			for _, s := range tt.mustNot {
				if strings.Contains(got, s) {
					t.Errorf("%s contains forbidden string: %q", tt.name, s)
				}
			}
		})
	}
}

func TestStaticPromptKeepsXiaoYingIdentityAcrossModes(t *testing.T) {
	config.InitializeMock()
	provider := models.ModelProvider("test-provider")
	for _, mode := range []string{"normal", "coding", "autonomous"} {
		t.Run(mode, func(t *testing.T) {
			got := GetStaticBasePrompt(mode, provider, "")
			if !strings.Contains(got, "小英（XiaoYing）") {
				t.Fatalf("expected fixed XiaoYing identity in %q mode prompt", mode)
			}
			if strings.Contains(strings.ToLower(got), "role: auramate") {
				t.Fatalf("unexpected legacy assistant identity in %q mode prompt", mode)
			}
		})
	}
}

func TestAutonomousPersonaOverrideCannotReplaceXiaoYingIdentity(t *testing.T) {
	config.InitializeMock()
	provider := models.ModelProvider("test-provider")
	personaOverride := "# Role: Another Agent\nYou have a different name."

	staticPrompt := GetStaticBasePrompt("autonomous", provider, personaOverride)
	if !strings.Contains(staticPrompt, "小英（XiaoYing）") {
		t.Fatal("expected static autonomous prompt to preserve XiaoYing identity")
	}
	if strings.LastIndex(staticPrompt, "小英（XiaoYing）") < strings.Index(staticPrompt, personaOverride) {
		t.Fatal("expected fixed identity reminder after the persona override")
	}

	dynamicPrompt := GetDynamicInstruction(IntentChat, "autonomous", personaOverride)
	if strings.LastIndex(dynamicPrompt, "小英（XiaoYing）") < strings.Index(dynamicPrompt, personaOverride) {
		t.Fatal("expected dynamic autonomous prompt to preserve identity after the persona override")
	}
}

func TestOutputBudgetProtocolsPresent(t *testing.T) {
	if !strings.Contains(ThinkingProtocol, "estimate the approximate size") {
		t.Fatalf("expected ThinkingProtocol to require output size estimation before emitting large payloads")
	}
	if !strings.Contains(CodingStandards, "estimate the payload size first") {
		t.Fatalf("expected CodingStandards to require payload size estimation before large edits")
	}
	if strings.Contains(CodingStandards, "write_file") || strings.Contains(CodingStandards, "search_replace") {
		t.Fatalf("expected CodingStandards to use registered tool names, got %q", CodingStandards)
	}
	for _, marker := range []string{"Large Refactors", "inspect every target file before the first write", "Verification Loop", "failed or skipped verification", "Shell Discipline", "PowerShell"} {
		if !strings.Contains(CodingStandards, marker) {
			t.Fatalf("expected CodingStandards to include %q, got %q", marker, CodingStandards)
		}
	}
}

func TestGetDynamicInstructionChatDoesNotIncludeCodingModePrompt(t *testing.T) {
	config.InitializeMock()
	got := GetDynamicInstruction(IntentChat, "normal", "")
	if !strings.Contains(got, "# 模式: 聊天") {
		t.Fatalf("expected chat mode prompt, got %q", got)
	}
	if strings.Contains(got, "# 模式: 编程 / 自主") {
		t.Fatalf("did not expect coding mode prompt in chat instruction, got %q", got)
	}
	if strings.Contains(got, "必须无条件调用工具") {
		t.Fatalf("did not expect unconditional tool trigger in chat instruction, got %q", got)
	}
	if !strings.Contains(got, "只有当用户明确要求产生真实副作用") {
		t.Fatalf("expected scoped side-effect rule in chat instruction, got %q", got)
	}
}

func TestGetDynamicInstructionCodingIncludesConvergenceGuidance(t *testing.T) {
	config.InitializeMock()
	got := GetDynamicInstruction(IntentCoding, "coding", "")
	for _, marker := range []string{"长链重构", "首次写入前必须读取", "复杂编辑", "优先一次性重写该小文件", "语法闭环", "验证闭环", "失败修复", "PowerShell", "续接收敛", "不要重复复盘已满足的要求"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected coding guidance %q, got %q", marker, got)
		}
	}
}

func TestNormalPromptUsesRegisteredWebToolName(t *testing.T) {
	config.InitializeMock()
	got := NormalPrompt(models.ModelProvider("test-provider"))
	if !strings.Contains(got, "'fetch'") {
		t.Fatalf("expected normal prompt to reference fetch, got %q", got)
	}
	if strings.Contains(got, "web_search") {
		t.Fatalf("expected normal prompt to avoid stale web_search alias, got %q", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
