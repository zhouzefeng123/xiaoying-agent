package cognitive

import "testing"

func TestNextStateAfterReflection_WithEpisodicFragments_GoesToDreaming(t *testing.T) {
	if got := nextStateAfterReflection(true); got != StateDreaming {
		t.Fatalf("unexpected next state: want %q, got %q", StateDreaming, got)
	}
}

func TestNextStateAfterReflection_WithoutEpisodicFragments_GoesToStandby(t *testing.T) {
	if got := nextStateAfterReflection(false); got != StateStandby {
		t.Fatalf("unexpected next state: want %q, got %q", StateStandby, got)
	}
}

func TestNextStateAfterExploration_WithEpisodicFragments_GoesToDreaming(t *testing.T) {
	if got := nextStateAfterExploration(true); got != StateDreaming {
		t.Fatalf("unexpected next state: want %q, got %q", StateDreaming, got)
	}
}

func TestNextStateAfterExploration_WithoutEpisodicFragments_GoesToStandby(t *testing.T) {
	if got := nextStateAfterExploration(false); got != StateStandby {
		t.Fatalf("unexpected next state: want %q, got %q", StateStandby, got)
	}
}
