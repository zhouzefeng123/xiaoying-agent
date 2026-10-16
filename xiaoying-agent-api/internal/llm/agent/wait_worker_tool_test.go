package agent

import "testing"

func TestIsWorkerFailureResultIgnoresNarrativeFallbackText(t *testing.T) {
	input := "本报告分析了失败回退策略、诊断流程和恢复建议。"
	if isWorkerFailureResult(input) {
		t.Fatalf("expected narrative fallback text to not be treated as worker failure")
	}
}

func TestIsWorkerFailureResultMatchesHardErrors(t *testing.T) {
	for _, input := range []string{
		"Error: worker execution failed: timeout",
		"连接被拒绝，任务终止",
		"tool not found: write",
	} {
		if !isWorkerFailureResult(input) {
			t.Fatalf("expected hard error to be detected: %q", input)
		}
	}
}
