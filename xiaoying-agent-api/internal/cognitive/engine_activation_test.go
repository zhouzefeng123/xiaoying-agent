package cognitive

import (
	"context"
	"testing"
)

func TestTransitionToWakingWithoutEpisodicFragmentsEntersStandby(t *testing.T) {
	engine, q, conn := newReflectionTestEngine(t)
	defer conn.Close()

	setBudgetState(t, q, 0, 100)
	engine.TransitionTo(context.Background(), string(StateWaking))

	state, _, _, err := engine.GetState(context.Background())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != string(StateStandby) {
		t.Fatalf("expected standby after waking without episodic fragments, got %s", state)
	}
}
