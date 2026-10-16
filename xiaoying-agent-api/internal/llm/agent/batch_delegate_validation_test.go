package agent

import (
	"strings"
	"testing"
)

func TestBuildNormalizedBatchPlanRejectsInvalidDAGBeforeDispatch(t *testing.T) {
	cases := []struct {
		name    string
		tasks   []batchTaskItem
		wantErr string
	}{
		{
			name: "duplicate trimmed id",
			tasks: []batchTaskItem{
				{ID: " step ", Task: "one"},
				{ID: "step", Task: "two"},
			},
			wantErr: "duplicate task id: step",
		},
		{
			name: "unknown dependency",
			tasks: []batchTaskItem{
				{ID: "consumer", Task: "two", DependsOn: batchDependencyList{{TaskID: "missing"}}},
			},
			wantErr: "depends_on unknown id missing",
		},
		{
			name: "self dependency",
			tasks: []batchTaskItem{
				{ID: "same", Task: "one", DependsOn: batchDependencyList{{TaskID: "same"}}},
			},
			wantErr: "cannot depend on itself",
		},
		{
			name: "cycle",
			tasks: []batchTaskItem{
				{ID: "a", Task: "one", DependsOn: batchDependencyList{{TaskID: "b"}}},
				{ID: "b", Task: "two", DependsOn: batchDependencyList{{TaskID: "a"}}},
			},
			wantErr: "cycle",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildNormalizedBatchPlan(tc.tasks)
			if err == nil {
				t.Fatal("expected invalid DAG to fail before worker dispatch")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error to contain %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestTopologicalLayersKeepsReadyTasksTogether(t *testing.T) {
	plan, err := buildNormalizedBatchPlan([]batchTaskItem{
		{ID: "alpha", Task: "alpha"},
		{ID: "beta", Task: "beta"},
		{ID: "gamma", Task: "gamma", DependsOn: batchDependencyList{{TaskID: "alpha"}, {TaskID: "beta"}}},
	})
	if err != nil {
		t.Fatalf("expected valid DAG, got %v", err)
	}
	if len(plan.Layers) != 2 {
		t.Fatalf("expected 2 dependency layers, got %#v", plan.Layers)
	}
	if strings.Join(plan.Layers[0], ",") != "alpha,beta" {
		t.Fatalf("expected independent tasks in first ready layer, got %#v", plan.Layers[0])
	}
	if strings.Join(plan.Layers[1], ",") != "gamma" {
		t.Fatalf("expected dependent task in second layer, got %#v", plan.Layers[1])
	}
}
