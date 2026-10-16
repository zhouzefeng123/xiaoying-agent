package agent

import (
	"context"
	"testing"

	llmtools "github.com/auramate/auramate/internal/llm/tools"
)

type alwaysSimilarEmbedder struct{}

func (alwaysSimilarEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = []float32{1}
	}
	return vecs, nil
}

func collectToolNames(toolset []llmtools.BaseTool) map[string]bool {
	names := make(map[string]bool, len(toolset))
	for _, tool := range toolset {
		names[tool.Info().Name] = true
	}
	return names
}

func TestCoderAgentToolsIncludesMemoryTool(t *testing.T) {
	toolset := CoderAgentTools(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, true, nil)

	for _, tool := range toolset {
		if tool.Info().Name == llmtools.MemoryToolName {
			return
		}
	}

	t.Fatalf("expected coder agent tools to include %q", llmtools.MemoryToolName)
}

func TestAgentToolsExposeDiagnosticsTool(t *testing.T) {
	tests := []struct {
		name    string
		toolset []llmtools.BaseTool
	}{
		{
			name:    "coder",
			toolset: CoderAgentTools(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, true, nil),
		},
		{
			name:    "normal",
			toolset: NormalAgentTools(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, true, nil),
		},
		{
			name:    "task",
			toolset: TaskAgentTools(nil, nil, nil, nil, nil, nil, nil, nil),
		},
		{
			name:    "life",
			toolset: LifeAgentTools(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			names := collectToolNames(tt.toolset)
			if !names[llmtools.DiagnosticsToolName] {
				t.Fatalf("expected %s agent tools to include %q", tt.name, llmtools.DiagnosticsToolName)
			}
		})
	}
}

func TestCoderAgentToolsIncludesSubagentToolsWhenOrchestratorEnabled(t *testing.T) {
	toolset := CoderAgentTools(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &Orchestrator{}, true, nil)
	names := collectToolNames(toolset)

	for _, expected := range []string{"agent", "delegate_task", "batch_delegate_task", "manage_worker", "wait_for_workers"} {
		if !names[expected] {
			t.Fatalf("expected coder agent tools to include %q when orchestrator is enabled", expected)
		}
	}
}

func TestCoderAgentToolsOmitsSubagentToolsWhenDisabled(t *testing.T) {
	toolset := CoderAgentTools(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &Orchestrator{}, false, nil)
	names := collectToolNames(toolset)

	for _, forbidden := range []string{"agent", "delegate_task", "batch_delegate_task", "manage_worker", "wait_for_workers"} {
		if names[forbidden] {
			t.Fatalf("expected coder agent tools to omit %q when subagents are disabled", forbidden)
		}
	}
}

func TestNormalAgentToolsIncludesSubagentToolsWhenOrchestratorEnabled(t *testing.T) {
	toolset := NormalAgentTools(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &Orchestrator{}, nil, true, nil)
	names := collectToolNames(toolset)

	for _, expected := range []string{"agent", "delegate_task", "batch_delegate_task", "manage_worker", "wait_for_workers"} {
		if !names[expected] {
			t.Fatalf("expected normal agent tools to include %q when orchestrator is enabled", expected)
		}
	}
}

func TestNormalAgentToolsOmitsSubagentToolsWhenDisabled(t *testing.T) {
	toolset := NormalAgentTools(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &Orchestrator{}, nil, false, nil)
	names := collectToolNames(toolset)

	for _, forbidden := range []string{"agent", "delegate_task", "batch_delegate_task", "manage_worker", "wait_for_workers"} {
		if names[forbidden] {
			t.Fatalf("expected normal agent tools to omit %q when subagents are disabled", forbidden)
		}
	}
}

func TestTaskAgentToolsIncludesCoreExecutionTools(t *testing.T) {
	toolset := TaskAgentTools(nil, nil, nil, nil, nil, nil, nil, nil)
	names := collectToolNames(toolset)

	for _, expected := range []string{"read", "write", "edit", "patch", "grep", "ls", llmtools.RunCommandToolName} {
		if !names[expected] {
			t.Fatalf("expected task agent tools to include %q", expected)
		}
	}
}

func TestDeriveTaskStateHintsWithSemanticKeepsReadOnlyForNegatedWriteCue(t *testing.T) {
	ag := &agent{}

	state := ag.deriveTaskStateHintsWithSemantic(context.Background(), nil, "审查一下，不要修改代码")
	if state == nil || !state.ReadOnlyMode {
		t.Fatalf("expected negated write cue to keep read-only mode, got %#v", state)
	}

	state = ag.deriveTaskStateHintsWithSemantic(context.Background(), state, "退出只读，现在可以修改")
	if state == nil || state.ReadOnlyMode {
		t.Fatalf("expected explicit write override to leave read-only mode, got %#v", state)
	}
}

func TestDeriveTaskStateHintsWithSemanticDoesNotUseSemanticMatchForReadOnly(t *testing.T) {
	ag := &agent{embedder: alwaysSimilarEmbedder{}}

	state := ag.deriveTaskStateHintsWithSemantic(context.Background(), nil, "今天天气怎么样")
	if state == nil {
		t.Fatalf("expected state")
	}
	if state.ReadOnlyMode {
		t.Fatalf("expected generic question not to enter read-only mode, got %#v", state)
	}
}
