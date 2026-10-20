package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/auramate/auramate/internal/config"
)

func TestSafeWorkspacePath(t *testing.T) {
	root := t.TempDir()
	ctx := config.ContextWithWorkspace(context.Background(), root)

	inside, err := safeWorkspacePath(ctx, "sub/file.txt")
	if err != nil {
		t.Fatalf("inside path rejected: %v", err)
	}
	if !pathWithinRoot(root, inside) {
		t.Fatalf("resolved path %q is outside %q", inside, root)
	}

	for _, raw := range []string{
		"../escape.txt",
		"../../escape.txt",
		filepath.Join(filepath.Dir(root), "escape.txt"),
	} {
		if _, err := safeWorkspacePath(ctx, raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}
