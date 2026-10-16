package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/lsp"
)

func TestLookupSymbolReturnsExplicitLSPStatusWhenUnavailable(t *testing.T) {
	registry := lsp.NewRegistry()
	if allowed, _ := registry.TryBeginStart("python", "pyright-langserver"); !allowed {
		t.Fatalf("expected first startup attempt to be allowed")
	}
	registry.MarkFailure("python", "pyright-langserver", errors.New("network down"))

	tool := NewLookupSymbolTool(registry)
	response, err := tool.Run(context.Background(), ToolCall{
		ID:    "call-1",
		Name:  LookupSymbolToolName,
		Input: `{"query":"MySymbol"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected error response when no ready LSP is available")
	}
	if !strings.Contains(response.Content, "no ready LSP clients available") {
		t.Fatalf("expected explicit unavailable summary, got %q", response.Content)
	}
	if !strings.Contains(response.Content, "python LSP failed to start") {
		t.Fatalf("expected language-specific LSP failure summary, got %q", response.Content)
	}
}

func TestLSPStatusNoteForFileReportsFailedServer(t *testing.T) {
	registry := lsp.NewRegistry()
	if allowed, _ := registry.TryBeginStart("python", "pyright-langserver"); !allowed {
		t.Fatalf("expected first startup attempt to be allowed")
	}
	registry.MarkFailure("python", "pyright-langserver", errors.New("network down"))

	note := lspStatusNoteForFile("demo.py", registry)
	if !strings.Contains(note, "python LSP failed to start") {
		t.Fatalf("expected failed LSP status note, got %q", note)
	}
}
