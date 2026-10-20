package config

import (
	"context"
	"testing"
)

func TestCognitiveStateKeyIsPerUser(t *testing.T) {
	ctxA := ContextWithUserEmail(context.Background(), "alice@qq.com")
	ctxB := ContextWithUserEmail(context.Background(), "bob@qq.com")

	keyA := CognitiveStateKey(ctxA, "daily_token_usage")
	keyB := CognitiveStateKey(ctxB, "daily_token_usage")
	if keyA == keyB {
		t.Fatalf("expected different namespaced keys, got %q", keyA)
	}
	if keyA == "daily_token_usage" || keyB == "daily_token_usage" {
		t.Fatalf("expected keys to be namespaced: %q %q", keyA, keyB)
	}
}

func TestEffectiveOwnerEmailFallback(t *testing.T) {
	ctx := ContextWithUserEmail(context.Background(), "alice@qq.com")
	if got := EffectiveOwnerEmail(ctx); got != "alice@qq.com" {
		t.Fatalf("user owner = %q", got)
	}

	t.Setenv("AURAMATE_SYSTEM_OWNER_EMAIL", "system")
	if got := EffectiveOwnerEmail(context.Background()); got != "system" {
		t.Fatalf("system owner = %q", got)
	}
}

func TestWorkspaceIDDiffersByUser(t *testing.T) {
	if WorkspaceID("alice@qq.com") == WorkspaceID("bob@qq.com") {
		t.Fatal("expected different workspace IDs")
	}
}
