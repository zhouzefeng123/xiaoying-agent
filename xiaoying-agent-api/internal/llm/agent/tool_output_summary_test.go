package agent

import (
	"strings"
	"testing"

	llmtools "github.com/auramate/auramate/internal/llm/tools"
)

func TestShouldAutoSummarizeToolOutputSkipsWordReader(t *testing.T) {
	info := llmtools.ToolInfo{Name: "auramate-word-reader", DisableAutoSummary: true}
	if shouldAutoSummarizeToolOutput(info, strings.Repeat("a", 10001)) {
		t.Fatalf("expected word reader output to bypass auto summarization")
	}
}

func TestShouldAutoSummarizeToolOutputStillSummarizesLargeGenericOutput(t *testing.T) {
	info := llmtools.ToolInfo{Name: "run_command"}
	if !shouldAutoSummarizeToolOutput(info, strings.Repeat("a", 10001)) {
		t.Fatalf("expected large generic tool output to be auto summarized")
	}
}
