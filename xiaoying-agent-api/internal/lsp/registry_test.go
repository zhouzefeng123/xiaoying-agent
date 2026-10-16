package lsp

import (
	"errors"
	"testing"
	"time"
)

func TestRegistryBackoffBlocksImmediateRetry(t *testing.T) {
	registry := NewRegistry()

	allowed, _ := registry.TryBeginStart("python", "pyright-langserver")
	if !allowed {
		t.Fatalf("expected first startup attempt to be allowed")
	}

	status := registry.MarkFailure("python", "pyright-langserver", errors.New("network down"))
	if status.Phase != RuntimePhaseFailed {
		t.Fatalf("expected failed status, got %s", status.Phase)
	}
	if status.NextRetryAt.IsZero() {
		t.Fatalf("expected next retry time to be recorded")
	}
	if !status.NextRetryAt.After(time.Now()) {
		t.Fatalf("expected next retry time in the future, got %s", status.NextRetryAt)
	}

	allowed, blockedStatus := registry.TryBeginStart("python", "pyright-langserver")
	if allowed {
		t.Fatalf("expected retry attempt to be blocked by backoff")
	}
	if blockedStatus.Phase != RuntimePhaseBackoff {
		t.Fatalf("expected backoff phase, got %s", blockedStatus.Phase)
	}
}
